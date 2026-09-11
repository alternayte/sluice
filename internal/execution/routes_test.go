package execution

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

type envelope struct {
	Error struct {
		Code    string `json:"code"`
		Details []struct {
			Field string `json:"field"`
		} `json:"details"`
	} `json:"error"`
}

func newTestMux(t *testing.T) *chi.Mux {
	t.Helper()
	mux := chi.NewRouter()
	api := httpx.NewAPI(mux)
	Routes(api, mux, nil)
	RunnerRoutes(api, mux, nil)
	return mux
}

func serve(t *testing.T, mux http.Handler, req *http.Request, role kernel.Role) (int, envelope) {
	t.Helper()
	if role != kernel.RoleNone {
		req = req.WithContext(kernel.WithPrincipal(req.Context(), &kernel.Principal{Role: role, Kind: "token"}))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var env envelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	return rec.Code, env
}

func fieldsOf(env envelope) []string {
	var out []string
	for _, d := range env.Error.Details {
		out = append(out, d.Field)
	}
	return out
}

// TestListExecutionsLabelExplode checks that repeated ?label=a&label=b binds to two values,
// as the old exploded label parameter did. The UI sends the repeated form.
func TestListExecutionsLabelExplode(t *testing.T) {
	mux := chi.NewRouter()
	api := httpx.NewAPI(mux)
	var got []string
	huma.Register(api, httpx.Op("labels", http.MethodGet, "/labels", httpx.Public),
		func(_ context.Context, in *listExecutionsIn) (*struct{}, error) {
			got = in.Label
			return nil, nil
		})
	req := httptest.NewRequest(http.MethodGet, "/labels?label=team%3Ddata&label=run%3Dnightly,x", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code >= 300 {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	if want := []string{"team=data", "run=nightly,x"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("labels %q, want %q", got, want)
	}
}

// TestTriggerLabelsSchema checks the TriggerRequest label limits: 20 labels and 256
// characters for each value. The handler rejects them before it uses the engine.
func TestTriggerLabelsSchema(t *testing.T) {
	mux := newTestMux(t)
	many := map[string]string{}
	for i := range 21 {
		many[strings.Repeat("k", i+1)] = "v"
	}
	cases := map[string]map[string]string{
		"long value":  {"team": strings.Repeat("x", 257)},
		"many labels": many,
	}
	for name, labels := range cases {
		t.Run(name, func(t *testing.T) {
			b, _ := json.Marshal(map[string]any{"labels": labels})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/flows/ns/f/executions", strings.NewReader(string(b)))
			req.Header.Set("Content-Type", "application/json")
			status, env := serve(t, mux, req, kernel.Operator)
			fields := fieldsOf(env)
			if status != http.StatusUnprocessableEntity || env.Error.Code != "validation_failed" || len(fields) == 0 || !strings.HasPrefix(fields[0], "labels") {
				t.Fatalf("status %d, envelope %+v", status, env)
			}
		})
	}
}

// TestRawRoutesCheckParams checks the UUID path parameters of the Raw routes. The old
// validator answered 422 validation_failed and named the parameter.
func TestRawRoutesCheckParams(t *testing.T) {
	mux := newTestMux(t)
	cases := map[string]string{
		"/api/v1/executions/nope/logs/stream":    "executionId",
		"/api/v1/executions/nope/logs/download":  "executionId",
		"/api/v1/executions/nope/events":         "executionId",
		"/api/v1/executions/nope/artifacts/nope": "executionId",
		"/api/runner/v1/task-runs/nope/bundle":   "taskRunId",
	}
	for path, field := range cases {
		t.Run(path, func(t *testing.T) {
			status, env := serve(t, mux, httptest.NewRequest(http.MethodGet, path, nil), kernel.Viewer)
			fields := fieldsOf(env)
			if status != http.StatusUnprocessableEntity || len(fields) == 0 || fields[0] != field {
				t.Fatalf("status %d, envelope %+v", status, env)
			}
		})
	}
}

// TestRunnerPutArtifactRequiresBody checks the three body cases that the old validator
// rejected with 422 validation_failed and the field "body".
func TestRunnerPutArtifactRequiresBody(t *testing.T) {
	mux := newTestMux(t)
	cases := []struct {
		name, body, contentType string
	}{
		{"empty file", "", "application/octet-stream"},
		{"no Content-Type", "data", ""},
		{"JSON Content-Type", `{"a":1}`, "application/json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/api/runner/v1/task-runs/0190b8a0-0000-7000-8000-000000000001/artifacts/a.txt",
				strings.NewReader(c.body))
			if c.contentType != "" {
				req.Header.Set("Content-Type", c.contentType)
			}
			status, env := serve(t, mux, req, kernel.RoleNone)
			fields := fieldsOf(env)
			if status != http.StatusUnprocessableEntity || env.Error.Code != "validation_failed" || len(fields) != 1 || fields[0] != "body" {
				t.Fatalf("status %d, envelope %+v", status, env)
			}
		})
	}
}
