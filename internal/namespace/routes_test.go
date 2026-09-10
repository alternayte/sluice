package namespace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// TestUploadFileRequiresBody checks the three body cases that the old validator rejected with
// 422 validation_failed and the field "body". The handler rejects them before it uses the service.
func TestUploadFileRequiresBody(t *testing.T) {
	mux := chi.NewRouter()
	api := httpx.NewAPI(mux)
	Routes(api, mux, nil)

	cases := []struct {
		name, body, contentType string
	}{
		{"empty file", "", "application/octet-stream"},
		{"no Content-Type", "data", ""},
		{"JSON Content-Type", `{"a":1}`, "application/json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/api/v1/namespaces/ns/file?path=a.txt", strings.NewReader(c.body))
			if c.contentType != "" {
				req.Header.Set("Content-Type", c.contentType)
			}
			req = req.WithContext(kernel.WithPrincipal(req.Context(), &kernel.Principal{Role: kernel.Editor, Kind: "token"}))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status %d, body %s", rec.Code, rec.Body)
			}
			var env struct {
				Error struct {
					Code    string `json:"code"`
					Details []struct {
						Field string `json:"field"`
					} `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatal(err)
			}
			if env.Error.Code != "validation_failed" || len(env.Error.Details) != 1 || env.Error.Details[0].Field != "body" {
				t.Fatalf("envelope %s", rec.Body)
			}
		})
	}
}
