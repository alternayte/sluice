//go:build k8s

package k8s

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestSCN_EXR_005_KubernetesJob runs a kubernetes task to SUCCESS with logs and outputs.
// The Job has the required fields and labels and only emptyDir volumes. The Job and Pod
// JSON do not contain the secret canary (REQ-EXR-005, SI-01).
func TestSCN_EXR_005_KubernetesJob(t *testing.T) {
	f := newForward(t)
	c := adminClient(t, f)
	canary := "k8s-canary-" + uniq()
	c.do(t, http.MethodPut, "/api/v1/secrets/K8S_CANARY", map[string]any{"value": canary}, http.StatusOK, nil)
	ns := "k8s5-" + uniq()
	saveFiles(t, c, ns, map[string]string{
		"job.sh": "echo running in kubernetes\ntest -n \"$CANARY\" && echo canary present\nsleep 8\n" +
			"echo '{\"type\":\"output\",\"key\":\"n\",\"value\":7}' >> \"$SLUICE_OUTPUTS\"\n",
		"j.flow.yaml": "id: j\n" + k8sExecutor + "env:\n  CANARY: \"${{ secret('K8S_CANARY') }}\"\n" +
			"tasks:\n  - {id: t, type: script, file: job.sh, timeout: 10m}\n",
	})
	d := trigger(t, c, ns, "j")
	job := waitJob(t, d.ID, 2*time.Minute)
	jobJSON := kubectl(t, "get", "job", job, "-o", "json")
	var j struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
		Spec struct {
			BackoffLimit            *int `json:"backoffLimit"`
			ActiveDeadlineSeconds   *int `json:"activeDeadlineSeconds"`
			TTLSecondsAfterFinished *int `json:"ttlSecondsAfterFinished"`
			Template                struct {
				Spec struct {
					RestartPolicy  string `json:"restartPolicy"`
					InitContainers []struct {
						Image   string   `json:"image"`
						Command []string `json:"command"`
					} `json:"initContainers"`
					Volumes []map[string]any `json:"volumes"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(jobJSON), &j); err != nil {
		t.Fatal(err)
	}
	s := j.Spec
	if s.BackoffLimit == nil || *s.BackoffLimit != 0 || s.Template.Spec.RestartPolicy != "Never" {
		t.Fatalf("backoffLimit %v restartPolicy %q", s.BackoffLimit, s.Template.Spec.RestartPolicy)
	}
	if s.ActiveDeadlineSeconds == nil || *s.ActiveDeadlineSeconds != 600 || s.TTLSecondsAfterFinished == nil || *s.TTLSecondsAfterFinished != 600 {
		t.Fatalf("activeDeadlineSeconds %v ttlSecondsAfterFinished %v", s.ActiveDeadlineSeconds, s.TTLSecondsAfterFinished)
	}
	l := j.Metadata.Labels
	if l["sluice.dev/execution-id"] != d.ID || l["sluice.dev/task-run-id"] == "" || l["sluice.dev/pool"] != "default" {
		t.Fatalf("labels %v", l)
	}
	if inits := s.Template.Spec.InitContainers; len(inits) != 1 || inits[0].Image != "sluice:dev" || !strings.Contains(strings.Join(inits[0].Command, " "), "runner-install") {
		t.Fatalf("init containers %+v", inits)
	}
	if len(s.Template.Spec.Volumes) != 2 {
		t.Fatalf("volumes %v", s.Template.Spec.Volumes)
	}
	for _, v := range s.Template.Spec.Volumes {
		if _, ok := v["emptyDir"]; !ok || len(v) != 2 {
			t.Fatalf("a volume is not only an emptyDir: %v", v)
		}
	}
	var podJSON string
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(time.Second) {
		podJSON = kubectl(t, "get", "pods", "-l", "sluice.dev/execution-id="+d.ID, "-o", "json")
		if strings.Contains(podJSON, `"name": "task"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no pod of the Job")
		}
	}
	if strings.Contains(jobJSON, canary) || strings.Contains(podJSON, canary) {
		t.Fatal("the Job or Pod JSON contains the secret canary")
	}
	d = waitTerminal(t, c, d.ID, 5*time.Minute)
	if d.State != "SUCCESS" {
		t.Fatalf("execution %s: %s", d.State, d.Error)
	}
	logs := logText(t, c, d.ID)
	if !strings.Contains(logs, "running in kubernetes") || !strings.Contains(logs, "canary present") {
		t.Fatalf("logs:\n%s", logs)
	}
	if v, _ := d.last("t").Outputs["n"].(float64); v != 7 {
		t.Fatalf("outputs %v", d.last("t").Outputs)
	}
}

// TestSCN_EXR_006_KubernetesCancel cancels a kubernetes task. The Job is deleted and the
// Pod is gone within 30 s (REQ-EXR-009).
func TestSCN_EXR_006_KubernetesCancel(t *testing.T) {
	f := newForward(t)
	c := adminClient(t, f)
	ns := "k8s6-" + uniq()
	saveFiles(t, c, ns, map[string]string{
		"long.sh":     "echo started\nsleep 600\n",
		"l.flow.yaml": "id: l\n" + k8sExecutor + "tasks:\n  - {id: t, type: script, file: long.sh, timeout: 20m}\n",
	})
	d := trigger(t, c, ns, "l")
	job := waitJob(t, d.ID, 2*time.Minute)
	waitExec(t, c, d.ID, 3*time.Minute, func(x execDetail) bool {
		return x.last("t").State == "RUNNING" && strings.Contains(logText(t, c, d.ID), "started")
	})
	start := time.Now()
	c.do(t, http.MethodPost, "/api/v1/executions/"+d.ID+"/cancel", nil, http.StatusAccepted, nil)
	if d = waitTerminal(t, c, d.ID, time.Minute); d.State != "CANCELLED" {
		t.Fatalf("execution %s", d.State)
	}
	for {
		_, jobErr := kubectlErr(t, "get", "job", job)
		pods := kubectl(t, "get", "pods", "-l", "sluice.dev/execution-id="+d.ID, "-o", "name")
		if jobErr != nil && pods == "" {
			break
		}
		if time.Since(start) > 30*time.Second {
			t.Fatalf("after 30 s: job deleted %v, pods %q", jobErr != nil, pods)
		}
		time.Sleep(time.Second)
	}
}

// TestSCN_EXR_007_Reconciler checks the kubernetes reconciler and restarts (REQ-EXR-006).
func TestSCN_EXR_007_Reconciler(t *testing.T) {
	f := newForward(t)
	c := adminClient(t, f)
	ns := "k8s7-" + uniq()
	saveFiles(t, c, ns, map[string]string{
		"slow.sh":        "echo slow start\nsleep 30\necho slow end\n",
		"once.sh":        "if [ \"$SLUICE_ATTEMPT\" = 1 ]; then echo first attempt; sleep 600; fi\necho attempt $SLUICE_ATTEMPT ok\n",
		"slow.flow.yaml": "id: slow\n" + k8sExecutor + "tasks:\n  - {id: t, type: script, file: slow.sh}\n",
		"once.flow.yaml": "id: once\n" + k8sExecutor + "retry: {max_attempts: 2, initial: 1s}\ntasks:\n  - {id: t, type: script, file: once.sh}\n",
		"pending.flow.yaml": "id: pending\nexecutor: {type: kubernetes, image: \"sluice-uv:dev\", kubernetes: {node_selector: {sluice-e2e: never}}}\n" +
			"tasks:\n  - {id: t, type: command, command: [\"true\"]}\n",
		"badimage.flow.yaml": "id: badimage\nexecutor: {type: kubernetes, image: \"sluice-e2e-missing/image:does-not-exist\"}\n" +
			"tasks:\n  - {id: t, type: command, command: [\"true\"]}\n",
	})

	t.Run("server restart", func(t *testing.T) {
		d := trigger(t, c, ns, "slow")
		waitJob(t, d.ID, 2*time.Minute)
		waitExec(t, c, d.ID, 3*time.Minute, func(x execDetail) bool { return strings.Contains(logText(t, c, d.ID), "slow start") })
		kubectl(t, "delete", "pod", "-l", "app.kubernetes.io/instance="+env(t, "SLUICE_K8S_RELEASE"), "--wait=false")
		time.Sleep(5 * time.Second)
		f.restart()
		if d = waitTerminal(t, c, d.ID, 6*time.Minute); d.State != "SUCCESS" || !strings.Contains(logText(t, c, d.ID), "slow end") {
			t.Fatalf("after the server restart: %s %s", d.State, d.Error)
		}
	})

	t.Run("orphan job", func(t *testing.T) {
		name := "sluice-orphan-" + uniq()
		manifest := fmt.Sprintf(`{"apiVersion":"batch/v1","kind":"Job","metadata":{"name":%q,"labels":{"sluice.dev/managed-by":"sluice","sluice.dev/pool":"default","sluice.dev/task-run-id":"00000000-0000-7000-8000-000000000000"}},
"spec":{"backoffLimit":0,"template":{"metadata":{"labels":{"sluice.dev/managed-by":"sluice","sluice.dev/pool":"default"}},"spec":{"restartPolicy":"Never","containers":[{"name":"task","image":"sluice-uv:dev","command":["sleep","600"]}]}}}}`, name)
		createFromJSON(t, manifest)
		for deadline := time.Now().Add(150 * time.Second); ; time.Sleep(2 * time.Second) {
			if _, err := kubectlErr(t, "get", "job", name); err != nil {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("the orphan Job was not deleted within 150 s")
			}
		}
	})

	t.Run("deleted job", func(t *testing.T) {
		d := trigger(t, c, ns, "once")
		job := waitJob(t, d.ID, 2*time.Minute)
		waitExec(t, c, d.ID, 3*time.Minute, func(x execDetail) bool { return strings.Contains(logText(t, c, d.ID), "first attempt") })
		kubectl(t, "delete", "job", job, "--wait=false")
		d = waitTerminal(t, c, d.ID, 6*time.Minute)
		var first, second taskRun
		for _, tr := range d.TaskRuns {
			switch tr.Attempt {
			case 1:
				first = tr
			case 2:
				second = tr
			}
		}
		if d.State != "SUCCESS" || first.State != "FAILED" || first.Reason != "lost" || second.State != "SUCCESS" {
			t.Fatalf("execution %s, attempt 1 %s %s, attempt 2 %s", d.State, first.State, first.Reason, second.State)
		}
	})

	t.Run("pending timeout", func(t *testing.T) {
		d := waitTerminal(t, c, trigger(t, c, ns, "pending").ID, 4*time.Minute)
		if tr := d.last("t"); d.State != "FAILED" || tr.Reason != "pod_pending_timeout" {
			t.Fatalf("unschedulable pod: %s, task %s %q", d.State, tr.Reason, tr.Error)
		}
	})

	t.Run("bad image", func(t *testing.T) {
		d := waitTerminal(t, c, trigger(t, c, ns, "badimage").ID, 4*time.Minute)
		if tr := d.last("t"); d.State != "FAILED" || tr.Reason != "image_pull_failed" {
			t.Fatalf("bad image: %s, task %s %q", d.State, tr.Reason, tr.Error)
		}
	})
}

// createFromJSON creates an object from a JSON manifest in the test namespace.
func createFromJSON(t testing.TB, manifest string) {
	t.Helper()
	cmd := kubectlCmd(t, "create", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("kubectl create: %v\n%s", err, out)
	}
}
