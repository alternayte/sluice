package app

import (
	"bytes"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/alternayte/sluice/internal/platform/httpx"
)

// TestSCN_API_001_GeneratedSpecMatches builds the API from the registered operations and
// compares its OpenAPI document byte for byte with the committed api/openapi.yaml
// (REQ-API-001). `just gen-check` compares the generated UI client in ui/src/api.
func TestSCN_API_001_GeneratedSpecMatches(t *testing.T) {
	r := chi.NewMux()
	api := httpx.NewAPI(r)
	registerRoutes(api, r, services{})
	got, err := api.OpenAPI().YAML()
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("api/openapi.yaml is out of date: run `just gen`")
	}
}
