//go:build k8s

package k8s

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// repoRoot returns the directory of go.mod.
func repoRoot(t testing.TB) string {
	t.Helper()
	wd, _ := os.Getwd()
	for d := wd; d != "/"; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	t.Fatal("go.mod not found")
	return ""
}

// exampleFiles reads examples/elt/namespace as path to content.
func exampleFiles(t testing.TB) map[string]string {
	t.Helper()
	root := filepath.Join(repoRoot(t), "examples", "elt", "namespace")
	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		files[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil || len(files) == 0 {
		t.Fatalf("read the example: %d files, %v", len(files), err)
	}
	return files
}

// TestSCN_EX_002_Kubernetes runs the ELT example with the kubernetes executor and the image
// sluice-uv (REQ-EX-002). The kind Postgres holds a warehouse database for the example.
func TestSCN_EX_002_Kubernetes(t *testing.T) {
	c := adminClient(t, newForward(t))
	id := uniq()
	user, db, ns := "elt_"+id, "warehouse_"+id, "elt-"+id
	psql := func(database, sql string) string {
		t.Helper()
		return kubectl(t, "exec", "deploy/postgres", "--", "psql", "-v", "ON_ERROR_STOP=1", "-U", "sluice", "-d", database, "-tAc", sql)
	}
	psql("sluice", "CREATE USER "+user+" PASSWORD 'elt-password-1'")
	psql("sluice", "CREATE DATABASE "+db+" OWNER "+user)
	psql(db, `CREATE SCHEMA source AUTHORIZATION `+user+`;
CREATE TABLE source.customers (id integer PRIMARY KEY, name text NOT NULL);
CREATE TABLE source.orders (id integer PRIMARY KEY, customer_id integer NOT NULL, amount numeric(10,2) NOT NULL);
INSERT INTO source.customers VALUES (1, 'Ada'), (2, 'Bob'), (3, 'Cy');
INSERT INTO source.orders VALUES (1, 1, 10.00), (2, 1, 5.50), (3, 2, 7.25), (4, 2, 1.00), (5, 2, 2.25);
ALTER TABLE source.customers OWNER TO `+user+`;
ALTER TABLE source.orders OWNER TO `+user+`;`)

	files := exampleFiles(t)
	files["namespace.yaml"] = strings.TrimRight(files["namespace.yaml"], "\n") + "\ndefaults:\n  " + k8sExecutor
	saveFiles(t, c, ns, files)
	for k, v := range map[string]string{"PG_HOST": "postgres", "PG_PORT": "5432", "PG_DATABASE": db, "PG_USER": user} {
		c.do(t, http.MethodPut, "/api/v1/namespaces/"+ns+"/variables/"+k, map[string]string{"value": v}, http.StatusOK, nil)
	}
	c.do(t, http.MethodPut, "/api/v1/namespaces/"+ns+"/secrets/ELT_PG_PASSWORD", map[string]string{"value": "elt-password-1"}, http.StatusOK, nil)

	d := trigger(t, c, ns, "elt")
	if job := waitJob(t, d.ID, 3*time.Minute); job == "" {
		t.Fatal("the example got no Job")
	}
	d = waitTerminal(t, c, d.ID, 25*time.Minute)
	if d.State != "SUCCESS" {
		t.Fatalf("the example ended %s: %s\n%s", d.State, d.Error, logText(t, c, d.ID))
	}
	var metrics struct {
		Items []struct {
			TaskKey string            `json:"task_key"`
			Name    string            `json:"name"`
			Value   float64           `json:"value"`
			Tags    map[string]string `json:"tags"`
		} `json:"items"`
	}
	c.do(t, http.MethodGet, "/api/v1/executions/"+d.ID+"/metrics", nil, http.StatusOK, &metrics)
	loaded := map[string]float64{}
	for _, m := range metrics.Items {
		if m.Name == "rows_loaded" {
			loaded[m.Tags["table"]] = m.Value
		}
	}
	if loaded["customers"] != 3 || loaded["orders"] != 5 {
		t.Fatalf("rows_loaded %v, want customers 3 and orders 5", loaded)
	}
	got := psql(db, `SELECT customer_name || '|' || order_count || '|' || round(order_amount::numeric, 2) FROM analytics.customer_orders ORDER BY customer_id`)
	if want := "Ada|2|15.50\nBob|3|10.50\nCy|0|0.00"; got != want {
		t.Fatalf("model rows %q, want %q", got, want)
	}
}
