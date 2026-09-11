package executor

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/alternayte/sluice/internal/runnerproto"
)

// Docker labels of task containers (REQ-EXR-004).
const (
	LabelExecutionID = "sluice.dev/execution-id"
	LabelTaskRunID   = "sluice.dev/task-run-id"
	LabelPool        = "sluice.dev/pool"
	LabelManagedBy   = "sluice.dev/managed-by"
)

// ReasonImagePullFailed fails a task whose image cannot be pulled (REQ-EXR-004).
const ReasonImagePullFailed = "image_pull_failed"

// runnerPath is the path of the injected runner in a task container (D-04).
const runnerPath = "/sluice-bin/sluice"

// NewDockerClient returns an Engine API client from DOCKER_HOST and the other standard variables.
func NewDockerClient() (*client.Client, error) {
	return client.New(client.FromEnv)
}

// DockerAvailable reports whether the Docker API answers within 2 s (REQ-EXR-002).
func DockerAvailable(ctx context.Context) bool {
	c, err := NewDockerClient()
	if err != nil {
		return false
	}
	defer func() { _ = c.Close() }()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err = c.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true})
	return err == nil
}

// DockerExecutor runs `sluice exec` in a task container through the Engine API (REQ-EXR-004).
type DockerExecutor struct {
	Client *client.Client
	// RunnerImage holds the runner binary at /usr/local/bin/sluice (SLUICE_RUNNER_IMAGE).
	RunnerImage string
	// Keep keeps task containers after completion (SLUICE_DOCKER_KEEP_CONTAINERS).
	Keep bool
	Log  *slog.Logger

	mu         sync.Mutex
	runnerTar  string // file with a tar of sluice-bin/sluice
	runnerFrom string // RunnerImage of runnerTar
}

// Type returns "docker".
func (d *DockerExecutor) Type() string { return Docker }

// Start pulls the image by policy, creates the container without volumes, injects the
// runner and starts it. The reference is the container ID.
func (d *DockerExecutor) Start(ctx context.Context, t Task) (string, error) {
	e := t.Executor
	if e.Image == "" {
		return "", errors.New("the docker executor needs an image")
	}
	if err := d.ensureImage(ctx, e.Image, e.Pull); err != nil {
		return "", &ReasonError{Reason: ReasonImagePullFailed, Err: err}
	}
	inject := e.InjectRunner == nil || *e.InjectRunner
	entrypoint := []string{"sluice", "exec"}
	if inject {
		entrypoint = []string{runnerPath, "exec"}
	}
	keys := make([]string, 0, len(t.Env))
	for k := range t.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, k := range keys {
		env = append(env, k+"="+t.Env[k])
	}
	res := container.Resources{}
	if e.Resources != nil && e.Resources.Limits != nil {
		cpu, err := ParseCPU(e.Resources.Limits.CPU)
		if err != nil {
			return "", err
		}
		mem, err := ParseMemory(e.Resources.Limits.Memory)
		if err != nil {
			return "", err
		}
		res.NanoCPUs, res.Memory = cpu, mem
	}
	init := true
	hc := &container.HostConfig{
		NetworkMode: container.NetworkMode(e.Network),
		// The runner calls the server through host.docker.internal; Linux engines need the mapping.
		ExtraHosts: []string{"host.docker.internal:host-gateway"},
		Resources:  res,
		Init:       &init,
	}
	cfg := &container.Config{
		Image:      e.Image,
		Entrypoint: entrypoint,
		Cmd:        []string{},
		Env:        env,
		Labels: map[string]string{LabelExecutionID: t.ExecutionID.String(), LabelTaskRunID: t.TaskRunID.String(),
			LabelPool: t.Pool, LabelManagedBy: "sluice"},
	}
	created, err := d.Client.ContainerCreate(ctx, client.ContainerCreateOptions{Config: cfg, HostConfig: hc,
		Name: fmt.Sprintf("sluice-%s", t.TaskRunID)})
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}
	id := created.ID
	fail := func(err error) (string, error) {
		d.remove(context.WithoutCancel(ctx), id)
		return "", err
	}
	if inject {
		if err := d.injectRunner(ctx, id); err != nil {
			return fail(fmt.Errorf("inject runner: %w", err))
		}
	}
	if _, err := d.Client.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
		return fail(fmt.Errorf("start container: %w", err))
	}
	return id, nil
}

// ensureImage applies the pull policy: always, if_not_present (default) or never.
func (d *DockerExecutor) ensureImage(ctx context.Context, image, policy string) error {
	if policy != "always" {
		_, err := d.Client.ImageInspect(ctx, image)
		if err == nil {
			return nil
		}
		if !cerrdefs.IsNotFound(err) {
			return err
		}
		if policy == "never" {
			return fmt.Errorf("image %s is not present and the pull policy is never", image)
		}
	}
	rc, err := d.Client.ImagePull(ctx, image, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull %s: %w", image, err)
	}
	defer func() { _ = rc.Close() }()
	if err := rc.Wait(ctx); err != nil {
		return fmt.Errorf("pull %s: %w", image, err)
	}
	return nil
}

// injectRunner copies the runner binary into a created container through the API (D-04).
func (d *DockerExecutor) injectRunner(ctx context.Context, id string) error {
	path, err := d.runnerArchive(ctx)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = d.Client.CopyToContainer(ctx, id, client.CopyToContainerOptions{DestinationPath: "/", Content: f})
	return err
}

// runnerArchive returns a tar file with sluice-bin/sluice, copied once from the runner image.
func (d *DockerExecutor) runnerArchive(ctx context.Context) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.runnerTar != "" && d.runnerFrom == d.RunnerImage {
		return d.runnerTar, nil
	}
	if err := d.ensureImage(ctx, d.RunnerImage, "if_not_present"); err != nil {
		return "", fmt.Errorf("runner image: %w", err)
	}
	tmp, err := d.Client.ContainerCreate(ctx, client.ContainerCreateOptions{Config: &container.Config{Image: d.RunnerImage,
		Entrypoint: []string{"/usr/local/bin/sluice"}, Cmd: []string{"version"}, Labels: map[string]string{LabelManagedBy: "sluice"}}})
	if err != nil {
		return "", fmt.Errorf("runner image: %w", err)
	}
	defer d.remove(context.WithoutCancel(ctx), tmp.ID)
	src, err := d.Client.CopyFromContainer(ctx, tmp.ID, client.CopyFromContainerOptions{SourcePath: "/usr/local/bin/sluice"})
	if err != nil {
		return "", fmt.Errorf("copy the runner from %s: %w", d.RunnerImage, err)
	}
	defer func() { _ = src.Content.Close() }()
	out, err := os.CreateTemp("", "sluice-runner-*.tar")
	if err != nil {
		return "", err
	}
	if err := repackRunner(src.Content, out); err != nil {
		_ = out.Close()
		_ = os.Remove(out.Name())
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	if d.runnerTar != "" {
		_ = os.Remove(d.runnerTar)
	}
	d.runnerTar, d.runnerFrom = out.Name(), d.RunnerImage
	return d.runnerTar, nil
}

// repackRunner reads the single file of a CopyFromContainer tar and writes a tar with
// the directory sluice-bin and the executable sluice-bin/sluice.
func repackRunner(r io.Reader, w io.Writer) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return errors.New("the runner image has no /usr/local/bin/sluice")
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		tw := tar.NewWriter(w)
		if err := tw.WriteHeader(&tar.Header{Name: "sluice-bin/", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: time.Now()}); err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: "sluice-bin/sluice", Typeflag: tar.TypeReg, Mode: 0o755, Size: h.Size, ModTime: time.Now()}); err != nil {
			return err
		}
		if _, err := io.CopyN(tw, tr, h.Size); err != nil {
			return err
		}
		return tw.Close()
	}
}

// Wait blocks until the container stops, then removes it unless Keep is set.
func (d *DockerExecutor) Wait(ctx context.Context, ref string) Result {
	w := d.Client.ContainerWait(ctx, ref, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	var res Result
	select {
	case r := <-w.Result:
		res.ExitCode = int(r.StatusCode)
		if r.Error != nil && r.Error.Message != "" {
			res.Err = errors.New(r.Error.Message)
		}
	case err := <-w.Error:
		if cerrdefs.IsNotFound(err) {
			return Result{ExitCode: -1, Err: ErrUnknownRef}
		}
		res = Result{ExitCode: -1, Err: err}
	}
	if !d.Keep {
		d.remove(context.WithoutCancel(ctx), ref)
	}
	return res
}

// Cancel stops the container (SIGTERM, SIGKILL after 10 s) and removes it (REQ-EXR-009).
func (d *DockerExecutor) Cancel(ctx context.Context, ref string) error {
	timeout := int(runnerproto.KillAfter / time.Second)
	_, err := d.Client.ContainerStop(ctx, ref, client.ContainerStopOptions{Timeout: &timeout})
	if cerrdefs.IsNotFound(err) {
		return ErrUnknownRef
	}
	if err != nil {
		return err
	}
	d.remove(ctx, ref)
	return nil
}

// Status reports whether the container still runs.
func (d *DockerExecutor) Status(ctx context.Context, ref string) (Status, error) {
	r, err := d.Client.ContainerInspect(ctx, ref, client.ContainerInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return StatusGone, nil
	}
	if err != nil {
		return StatusUnknown, err
	}
	if r.Container.State != nil && r.Container.State.Running {
		return StatusRunning, nil
	}
	return StatusGone, nil
}

// Sweep removes stopped task containers that an earlier server left, for example after a
// restart during a run. It does nothing when Keep is set.
func (d *DockerExecutor) Sweep(ctx context.Context) {
	if d.Keep {
		return
	}
	list, err := d.Client.ContainerList(ctx, client.ContainerListOptions{All: true,
		Filters: client.Filters{}.Add("label", LabelManagedBy+"=sluice").Add("status", "exited", "created", "dead")})
	if err != nil {
		if d.Log != nil {
			d.Log.Warn("docker sweep", "err", err)
		}
		return
	}
	for _, c := range list.Items {
		d.remove(ctx, c.ID)
	}
}

func (d *DockerExecutor) remove(ctx context.Context, id string) {
	_, err := d.Client.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true})
	if err != nil && !cerrdefs.IsNotFound(err) && d.Log != nil {
		d.Log.Warn("remove container", "container", id, "err", err)
	}
}
