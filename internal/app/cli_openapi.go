package app

import (
	"context"
	"fmt"
	"io"

	"github.com/go-chi/chi/v5"

	"github.com/alternayte/sluice/internal/platform/httpx"
)

// runOpenAPI prints the OpenAPI document. It needs no database and no server (REQ-API-001).
func runOpenAPI(_ context.Context, _ []string, stdout, stderr io.Writer) int {
	r := chi.NewMux()
	api := httpx.NewAPI(r)
	registerRoutes(api, r, services{})
	if err := httpx.CheckAccess(api); err != nil {
		fmt.Fprintln(stderr, err)
		return exitFail
	}
	b, err := api.OpenAPI().YAML()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFail
	}
	_, _ = stdout.Write(b)
	return exitOK
}
