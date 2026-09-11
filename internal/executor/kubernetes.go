package executor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/alternayte/sluice/internal/flow"
)

// Task reasons of the kubernetes executor (REQ-EXR-006).
const (
	ReasonPodPendingTimeout = "pod_pending_timeout"
	ReasonLost              = "lost"
)

// Volume and mount names of task pods (REQ-EXR-005, D-04).
const (
	binVolume     = "sluice-bin"
	workVolume    = "workdir"
	binMountPath  = "/sluice-bin"
	workMountPath = "/workdir"
	labelJobName  = "job-name"
)

// imagePullReasons are waiting reasons of a container whose image cannot be pulled.
var imagePullReasons = map[string]bool{"ErrImagePull": true, "ImagePullBackOff": true, "InvalidImageName": true, "ErrImageNeverPull": true}

// NewKubeClient returns a client from in-cluster config, or from kubeconfig when it is set.
func NewKubeClient(kubeconfig string) (kubernetes.Interface, error) {
	var cfg *rest.Config
	var err error
	if kubeconfig != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		cfg, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}

// KubernetesAvailable reports whether the cluster config works and a Job create dry run is
// allowed in the namespace (REQ-EXR-002).
func KubernetesAvailable(ctx context.Context, kubeconfig, namespace string) bool {
	c, err := NewKubeClient(kubeconfig)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{GenerateName: "sluice-dry-run-"}, Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{Name: "task", Image: "busybox"}}}}}}
	_, err = c.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
	return err == nil
}

// KubernetesExecutor runs `sluice exec` in one Job per attempt (REQ-EXR-005).
type KubernetesExecutor struct {
	Client    kubernetes.Interface
	Namespace string
	// RunnerImage holds the runner binary for the init container (SLUICE_RUNNER_IMAGE).
	RunnerImage string
	// JobTTL is ttlSecondsAfterFinished (SLUICE_K8S_JOB_TTL).
	JobTTL time.Duration
	Log    *slog.Logger
	// PollEvery is the Wait poll interval. Default 2 s.
	PollEvery time.Duration
}

// Type returns "kubernetes".
func (k *KubernetesExecutor) Type() string { return Kubernetes }

func jobName(t Task) string { return "sluice-" + t.TaskRunID.String() }

// JobFor builds the Job of a task attempt. It holds no secret values: the runner gets the
// resolved environment from the API at run time (REQ-EXR-005, SI-01, D-03).
func (k *KubernetesExecutor) JobFor(t Task) (*batchv1.Job, error) {
	e := t.Executor
	if e.Image == "" {
		return nil, errors.New("the kubernetes executor needs an image")
	}
	inject := e.InjectRunner == nil || *e.InjectRunner
	labels := map[string]string{}
	annotations := map[string]string{}
	if kc := e.Kubernetes; kc != nil {
		for key, v := range kc.Labels {
			labels[key] = v
		}
		for key, v := range kc.Annotations {
			annotations[key] = v
		}
	}
	labels[LabelExecutionID] = t.ExecutionID.String()
	labels[LabelTaskRunID] = t.TaskRunID.String()
	labels[LabelPool] = t.Pool
	labels[LabelManagedBy] = "sluice"

	keys := make([]string, 0, len(t.Env))
	for key := range t.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]corev1.EnvVar, 0, len(keys)+1)
	for _, key := range keys {
		env = append(env, corev1.EnvVar{Name: key, Value: t.Env[key]})
	}
	env = append(env, corev1.EnvVar{Name: "SLUICE_WORKDIR", Value: workMountPath})

	res, err := resources(e.Resources)
	if err != nil {
		return nil, err
	}
	command := []string{"sluice", "exec"}
	mounts := []corev1.VolumeMount{{Name: workVolume, MountPath: workMountPath}}
	volumes := []corev1.Volume{{Name: workVolume, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}
	var inits []corev1.Container
	if inject {
		command = []string{binMountPath + "/sluice", "exec"}
		mounts = append(mounts, corev1.VolumeMount{Name: binVolume, MountPath: binMountPath})
		volumes = append(volumes, corev1.Volume{Name: binVolume, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}})
		inits = []corev1.Container{{
			Name:         "sluice-runner",
			Image:        k.RunnerImage,
			Command:      []string{"/usr/local/bin/sluice", "runner-install", binMountPath},
			VolumeMounts: []corev1.VolumeMount{{Name: binVolume, MountPath: binMountPath}},
		}}
	}
	pod := corev1.PodSpec{
		RestartPolicy:  corev1.RestartPolicyNever,
		InitContainers: inits,
		Containers: []corev1.Container{{
			Name:         "task",
			Image:        e.Image,
			Command:      command,
			Env:          env,
			VolumeMounts: mounts,
			WorkingDir:   workMountPath,
			Resources:    res,
		}},
		Volumes: volumes,
	}
	if kc := e.Kubernetes; kc != nil {
		pod.ServiceAccountName = kc.ServiceAccount
		pod.NodeSelector = kc.NodeSelector
		for _, tol := range kc.Tolerations {
			pod.Tolerations = append(pod.Tolerations, corev1.Toleration{Key: tol.Key, Operator: corev1.TolerationOperator(tol.Operator),
				Value: tol.Value, Effect: corev1.TaintEffect(tol.Effect), TolerationSeconds: tol.TolerationSeconds})
		}
		for _, s := range kc.ImagePullSecrets {
			pod.ImagePullSecrets = append(pod.ImagePullSecrets, corev1.LocalObjectReference{Name: s})
		}
	}
	backoff := int32(0)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: jobName(t), Namespace: k.Namespace, Labels: labels, Annotations: annotations},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoff,
			Template:     corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels, Annotations: annotations}, Spec: pod},
		},
	}
	if t.Timeout > 0 {
		d := int64(t.Timeout / time.Second)
		job.Spec.ActiveDeadlineSeconds = &d
	}
	if k.JobTTL > 0 {
		ttl := int32(k.JobTTL / time.Second)
		job.Spec.TTLSecondsAfterFinished = &ttl
	}
	return job, nil
}

// resources converts §6.4 requests and limits.
func resources(r *flow.Resources) (corev1.ResourceRequirements, error) {
	out := corev1.ResourceRequirements{}
	if r == nil {
		return out, nil
	}
	conv := func(l *flow.ResourceList) (corev1.ResourceList, error) {
		if l == nil {
			return nil, nil
		}
		list := corev1.ResourceList{}
		if l.CPU != "" {
			q, err := resource.ParseQuantity(l.CPU)
			if err != nil {
				return nil, fmt.Errorf("cpu %q: %w", l.CPU, err)
			}
			list[corev1.ResourceCPU] = q
		}
		if l.Memory != "" {
			q, err := resource.ParseQuantity(l.Memory)
			if err != nil {
				return nil, fmt.Errorf("memory %q: %w", l.Memory, err)
			}
			list[corev1.ResourceMemory] = q
		}
		return list, nil
	}
	var err error
	if out.Requests, err = conv(r.Requests); err != nil {
		return out, err
	}
	out.Limits, err = conv(r.Limits)
	return out, err
}

// Start creates the Job of the attempt. The reference is the Job name.
func (k *KubernetesExecutor) Start(ctx context.Context, t Task) (string, error) {
	job, err := k.JobFor(t)
	if err != nil {
		return "", err
	}
	created, err := k.Client.BatchV1().Jobs(k.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("create job: %w", err)
	}
	return created.Name, nil
}

func (k *KubernetesExecutor) poll() time.Duration {
	if k.PollEvery > 0 {
		return k.PollEvery
	}
	return 2 * time.Second
}

// Wait polls the Job until it ends. An image that cannot be pulled ends the wait with
// image_pull_failed and deletes the Job.
func (k *KubernetesExecutor) Wait(ctx context.Context, ref string) Result {
	t := time.NewTicker(k.poll())
	defer t.Stop()
	for {
		job, err := k.Client.BatchV1().Jobs(k.Namespace).Get(ctx, ref, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			return Result{ExitCode: -1, Err: ErrUnknownRef}
		case err == nil && (job.Status.Succeeded > 0 || job.Status.Failed > 0):
			return Result{ExitCode: k.exitCode(ctx, ref)}
		case err == nil:
			if reason, msg := k.pullFailure(ctx, ref); reason != "" {
				_ = k.Cancel(context.WithoutCancel(ctx), ref)
				return Result{ExitCode: -1, Reason: ReasonImagePullFailed, Err: errors.New(msg)}
			}
		}
		select {
		case <-ctx.Done():
			return Result{ExitCode: -1, Err: ctx.Err()}
		case <-t.C:
		}
	}
}

func (k *KubernetesExecutor) pods(ctx context.Context, ref string) []corev1.Pod {
	list, err := k.Client.CoreV1().Pods(k.Namespace).List(ctx, metav1.ListOptions{LabelSelector: labelJobName + "=" + ref})
	if err != nil {
		return nil
	}
	return list.Items
}

// exitCode returns the exit code of the task container of the Job pod.
func (k *KubernetesExecutor) exitCode(ctx context.Context, ref string) int {
	for _, p := range k.pods(ctx, ref) {
		for _, cs := range p.Status.ContainerStatuses {
			if cs.Name == "task" && cs.State.Terminated != nil {
				return int(cs.State.Terminated.ExitCode)
			}
		}
	}
	return -1
}

// pullFailure returns image_pull_failed and a message when a container of the Job pod
// cannot pull its image.
func (k *KubernetesExecutor) pullFailure(ctx context.Context, ref string) (string, string) {
	for _, p := range k.pods(ctx, ref) {
		if r, msg := podPullFailure(p); r != "" {
			return r, msg
		}
	}
	return "", ""
}

func podPullFailure(p corev1.Pod) (string, string) {
	statuses := append(append([]corev1.ContainerStatus{}, p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...)
	for _, cs := range statuses {
		if w := cs.State.Waiting; w != nil && imagePullReasons[w.Reason] {
			return ReasonImagePullFailed, fmt.Sprintf("%s: %s: %s", cs.Image, w.Reason, w.Message)
		}
	}
	return "", ""
}

// Cancel deletes the Job with background propagation (REQ-EXR-009).
func (k *KubernetesExecutor) Cancel(ctx context.Context, ref string) error {
	policy := metav1.DeletePropagationBackground
	err := k.Client.BatchV1().Jobs(k.Namespace).Delete(ctx, ref, metav1.DeleteOptions{PropagationPolicy: &policy})
	if apierrors.IsNotFound(err) {
		return ErrUnknownRef
	}
	return err
}

// Status reports whether the Job still runs.
func (k *KubernetesExecutor) Status(ctx context.Context, ref string) (Status, error) {
	job, err := k.Client.BatchV1().Jobs(k.Namespace).Get(ctx, ref, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return StatusGone, nil
	}
	if err != nil {
		return StatusUnknown, err
	}
	if job.Status.Succeeded > 0 || job.Status.Failed > 0 {
		return StatusGone, nil
	}
	return StatusRunning, nil
}

// JobInfo is one Sluice Job of a pool, for the reconciler (REQ-EXR-006).
type JobInfo struct {
	Ref       string
	TaskRunID string
	// Finished is true when the Job succeeded or failed.
	Finished bool
	// PendingSince is the creation time of a pod that is still pending, or zero.
	PendingSince time.Time
	// PullFailure is the message of an image that cannot be pulled, or empty.
	PullFailure string
}

// Jobs lists the Sluice Jobs of a pool with the state of their pods.
func (k *KubernetesExecutor) Jobs(ctx context.Context, pool string) ([]JobInfo, error) {
	sel := LabelManagedBy + "=sluice," + LabelPool + "=" + pool
	jobs, err := k.Client.BatchV1().Jobs(k.Namespace).List(ctx, metav1.ListOptions{LabelSelector: sel})
	if err != nil {
		return nil, err
	}
	pods, err := k.Client.CoreV1().Pods(k.Namespace).List(ctx, metav1.ListOptions{LabelSelector: sel})
	if err != nil {
		return nil, err
	}
	byJob := map[string][]corev1.Pod{}
	for _, p := range pods.Items {
		byJob[p.Labels[labelJobName]] = append(byJob[p.Labels[labelJobName]], p)
	}
	out := make([]JobInfo, 0, len(jobs.Items))
	for _, j := range jobs.Items {
		info := JobInfo{Ref: j.Name, TaskRunID: j.Labels[LabelTaskRunID], Finished: j.Status.Succeeded > 0 || j.Status.Failed > 0}
		for _, p := range byJob[j.Name] {
			if p.Status.Phase == corev1.PodPending && (info.PendingSince.IsZero() || p.CreationTimestamp.Time.Before(info.PendingSince)) {
				info.PendingSince = p.CreationTimestamp.Time
			}
			if _, msg := podPullFailure(p); msg != "" {
				info.PullFailure = msg
			}
		}
		out = append(out, info)
	}
	return out, nil
}
