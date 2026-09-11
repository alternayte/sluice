//go:build k8s

package k8s

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSCN_DEP_002_Helm checks the Helm release in kind: 2 replicas, no PVC, a read-only
// root file system, /readyz 200 and a clean helm lint (REQ-DEP-002).
func TestSCN_DEP_002_Helm(t *testing.T) {
	release := env(t, "SLUICE_K8S_RELEASE")
	if ready := kubectl(t, "get", "deploy", release, "-o", "jsonpath={.status.readyReplicas}"); ready != "2" {
		t.Fatalf("ready replicas %q, want 2", ready)
	}
	if pvcs := kubectl(t, "get", "pvc", "-o", "name"); pvcs != "" {
		t.Fatalf("the namespace has PVCs: %s", pvcs)
	}
	ro := kubectl(t, "get", "pods", "-l", "app.kubernetes.io/instance="+release, "-o",
		`jsonpath={range .items[*]}{.spec.containers[0].securityContext.readOnlyRootFilesystem}{"\n"}{end}`)
	lines := strings.Fields(ro)
	if len(lines) < 2 {
		t.Fatalf("server pods: %q", ro)
	}
	for _, l := range lines {
		if l != "true" {
			t.Fatalf("a server pod has readOnlyRootFilesystem %q", l)
		}
	}
	f := newForward(t)
	resp, err := http.Get(f.URL() + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/readyz %d", resp.StatusCode)
	}
	chart, _ := filepath.Abs("../../deploy/helm/sluice")
	if out, err := exec.Command("helm", "lint", chart).CombinedOutput(); err != nil {
		t.Fatalf("helm lint: %v\n%s", err, out)
	}
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// TestSCN_SEC_012_KubernetesProviders resolves a Kubernetes Secret key with the kubernetes
// provider and a Vault value with Vault Kubernetes auth (REQ-SEC-001).
func TestSCN_SEC_012_KubernetesProviders(t *testing.T) {
	f := newForward(t)
	c := adminClient(t, f)
	c.do(t, http.MethodPost, "/api/v1/secret-providers", map[string]any{"name": "kube", "type": "kubernetes",
		"config": map[string]string{"namespace": env(t, "SLUICE_K8S_TEST_NAMESPACE")}}, http.StatusCreated, nil)
	c.do(t, http.MethodPost, "/api/v1/secret-providers", map[string]any{"name": "vault-k8s", "type": "vault"}, http.StatusCreated, nil)
	c.do(t, http.MethodPut, "/api/v1/secrets/K8S_API_KEY", map[string]any{"provider": "kube", "ref": "app-credentials/api-key"}, http.StatusOK, nil)
	c.do(t, http.MethodPut, "/api/v1/secrets/VAULT_K8S", map[string]any{"provider": "vault-k8s", "ref": "k8s-auth#password"}, http.StatusOK, nil)
	for _, key := range []string{"K8S_API_KEY", "VAULT_K8S"} {
		var res struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		}
		c.do(t, http.MethodPost, "/api/v1/secrets/"+key+"/check", nil, http.StatusOK, &res)
		if res.Status != "ok" {
			t.Fatalf("check %s: %s %s", key, res.Status, res.Message)
		}
	}
	ns := "sec12-" + uniq()
	saveFiles(t, c, ns, map[string]string{
		"hash.sh": "echo \"kube $(printf '%s' \"$A\" | sha256sum | cut -c1-64)\"\necho \"vault $(printf '%s' \"$B\" | sha256sum | cut -c1-64)\"\n",
		"s.flow.yaml": "id: s\n" + k8sExecutor + "env:\n  A: \"${{ secret('K8S_API_KEY') }}\"\n  B: \"${{ secret('VAULT_K8S') }}\"\n" +
			"tasks:\n  - {id: t, type: script, file: hash.sh}\n",
	})
	d := waitTerminal(t, c, trigger(t, c, ns, "s").ID, 5*time.Minute)
	if d.State != "SUCCESS" {
		t.Fatalf("execution %s: %s", d.State, d.Error)
	}
	logs := logText(t, c, d.ID)
	if !strings.Contains(logs, "kube "+sha("k8s-secret-value")) || !strings.Contains(logs, "vault "+sha("vault-k8s-value")) {
		t.Fatalf("the task did not get both values:\n%s", logs)
	}
}
