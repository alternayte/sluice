//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// exampleDir is the namespace directory of the ELT example (REQ-EX-001).
func exampleDir() string { return filepath.Join(repoRoot(), "examples", "elt", "namespace") }

// validateResultSchema compiles schemas/validate-result.schema.json.
func validateResultSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(), "schemas", "validate-result.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("validate-result.json", doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile("validate-result.json")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSCN_FLOW_006_ValidateExample(t *testing.T) {
	schema := validateResultSchema(t)
	invalid := filepath.Join(repoRoot(), "tests", "fixtures", "flows", "invalid", "cycle", "ns")
	for _, tc := range []struct {
		name, dir string
		code      int
		valid     bool
	}{{"the example", exampleDir(), 0, true}, {"an invalid fixture", invalid, 1, false}} {
		if out, stderr, code := runCLI(t, nil, "validate", tc.dir); code != tc.code {
			t.Fatalf("validate %s: exit %d, want %d\n%s%s", tc.name, code, tc.code, out, stderr)
		}
		out, stderr, code := runCLI(t, nil, "validate", "--json", tc.dir)
		if code != tc.code {
			t.Fatalf("validate --json %s: exit %d, want %d\n%s", tc.name, code, tc.code, stderr)
		}
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(out))
		if err != nil {
			t.Fatalf("%s: the --json output is not JSON: %v\n%s", tc.name, err, out)
		}
		if err := schema.Validate(inst); err != nil {
			t.Fatalf("%s: the --json output does not match the schema: %v", tc.name, err)
		}
		var res struct {
			Valid bool `json:"valid"`
		}
		_ = json.Unmarshal([]byte(out), &res)
		if res.Valid != tc.valid {
			t.Fatalf("%s: valid %v, want %v", tc.name, res.Valid, tc.valid)
		}
	}
}

// exampleFiles reads the example namespace as path to content.
func exampleFiles(t testing.TB) map[string]string {
	t.Helper()
	files := map[string]string{}
	root := exampleDir()
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

// exampleSeed creates the source schema: 3 customers and 5 orders.
const exampleSeed = `CREATE SCHEMA source AUTHORIZATION elt;
CREATE TABLE source.customers (id integer PRIMARY KEY, name text NOT NULL);
CREATE TABLE source.orders (id integer PRIMARY KEY, customer_id integer NOT NULL, amount numeric(10,2) NOT NULL);
INSERT INTO source.customers VALUES (1, 'Ada'), (2, 'Bob'), (3, 'Cy');
INSERT INTO source.orders VALUES (1, 1, 10.00), (2, 1, 5.50), (3, 2, 7.25), (4, 2, 1.00), (5, 2, 2.25);
ALTER TABLE source.customers OWNER TO elt;
ALTER TABLE source.orders OWNER TO elt;
`

// exampleModelQuery reads the SQLMesh model table in customer order.
const exampleModelQuery = `SELECT customer_name || '|' || order_count || '|' || round(order_amount::numeric, 2) FROM analytics.customer_orders ORDER BY customer_id`

var exampleModelRows = []string{"Ada|2|15.50", "Bob|3|10.50", "Cy|0|0.00"}

// checkExampleMetrics checks rows_loaded per table against the source counts and that the
// transform task reported its run time.
func checkExampleMetrics(t testing.TB, c *client, execID string) {
	t.Helper()
	var metrics struct {
		Items []struct {
			TaskKey string            `json:"task_key"`
			Name    string            `json:"name"`
			Value   float64           `json:"value"`
			Tags    map[string]string `json:"tags"`
		} `json:"items"`
	}
	c.do(t, http.MethodGet, "/api/v1/executions/"+execID+"/metrics", nil, http.StatusOK, &metrics)
	loaded := map[string]float64{}
	var runSeconds bool
	for _, m := range metrics.Items {
		switch {
		case m.Name == "rows_loaded" && m.TaskKey == "extract":
			loaded[m.Tags["table"]] = m.Value
		case m.Name == "sqlmesh_run_seconds" && m.TaskKey == "transform" && m.Value > 0:
			runSeconds = true
		}
	}
	if loaded["customers"] != 3 || loaded["orders"] != 5 || len(loaded) != 2 {
		t.Fatalf("rows_loaded %v, want customers 3 and orders 5", loaded)
	}
	if !runSeconds {
		t.Fatalf("no sqlmesh_run_seconds metric: %+v", metrics.Items)
	}
}

// TestSCN_EX_001_SingleContainer runs the ELT example in sluice-uv with the process executor
// (REQ-EX-001, REQ-EX-002). Postgres holds the Sluice database and the warehouse.
func TestSCN_EX_001_SingleContainer(t *testing.T) {
	requireImages(t)
	suffix := time.Now().Format("150405.000000")
	suffix = strings.ReplaceAll(suffix, ".", "")
	net, pg, app := "sluice-ex001-"+suffix, "sluice-ex001-pg-"+suffix, "sluice-ex001-app-"+suffix
	docker(t, "network", "create", net)
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", app, pg).Run()
		_ = exec.Command("docker", "network", "rm", net).Run()
	})
	docker(t, "run", "-d", "--name", pg, "--network", net, "-e", "POSTGRES_USER=sluice", "-e", "POSTGRES_PASSWORD=sluice",
		"-e", "POSTGRES_DB=sluice", "postgres:17-alpine")
	for deadline := time.Now().Add(60 * time.Second); exec.Command("docker", "exec", pg, "pg_isready", "-U", "sluice", "-h", "127.0.0.1").Run() != nil; time.Sleep(500 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("postgres not ready")
		}
	}
	psql := func(db, sql string) string {
		t.Helper()
		return docker(t, "exec", pg, "psql", "-v", "ON_ERROR_STOP=1", "-U", "sluice", "-d", db, "-tAc", sql)
	}
	psql("sluice", "CREATE USER elt PASSWORD 'elt-password-1'")
	psql("sluice", "CREATE DATABASE warehouse OWNER elt")
	psql("warehouse", exampleSeed)

	docker(t, "run", "-d", "--name", app, "--network", net, "-p", "127.0.0.1::8080",
		"-e", "SLUICE_DATABASE_URL=postgres://sluice:sluice@"+pg+":5432/sluice?sslmode=disable",
		"-e", "SLUICE_PUBLIC_URL=http://localhost:8080", "-e", "SLUICE_EXECUTORS=process",
		"-e", "SLUICE_MASTER_KEYS="+masterKey("k1", 1),
		"-e", "SLUICE_BOOTSTRAP_ADMIN_EMAIL="+adminEmail, "-e", "SLUICE_BOOTSTRAP_ADMIN_PASSWORD="+adminPassword,
		imageSluiceUV)
	c := adminClient(t, &Proc{URL: waitContainerReady(t, app)})
	saveFiles(t, c, "elt", exampleFiles(t))
	for k, v := range map[string]string{"PG_HOST": pg, "PG_PORT": "5432", "PG_DATABASE": "warehouse", "PG_USER": "elt"} {
		c.do(t, http.MethodPut, "/api/v1/namespaces/elt/variables/"+k, map[string]string{"value": v}, http.StatusOK, nil)
	}
	c.do(t, http.MethodPut, "/api/v1/namespaces/elt/secrets/ELT_PG_PASSWORD", map[string]string{"value": "elt-password-1"}, http.StatusOK, nil)

	d := waitTerminal(t, c, triggerFlow(t, c, "elt", "elt", nil, nil).ID, 20*time.Minute)
	if d.State != "SUCCESS" {
		t.Fatalf("the example ended %s: %s\n%s", d.State, d.Error, lastLines(logText(allLogs(t, c, d.ID, "")), 60))
	}
	if rows, _ := d.Outputs["rows"].(float64); rows != 8 {
		t.Fatalf("output rows %v, want 8", d.Outputs["rows"])
	}
	checkExampleMetrics(t, c, d.ID)
	if got := strings.Split(psql("warehouse", exampleModelQuery), "\n"); strings.Join(got, ",") != strings.Join(exampleModelRows, ",") {
		t.Fatalf("model rows %q, want %q", got, exampleModelRows)
	}
}
