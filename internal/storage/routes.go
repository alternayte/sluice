package storage

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// Status is the storage status of the settings page (REQ-UI-009).
type Status struct {
	Driver  string `json:"driver" enum:"postgres,fs,s3,azblob"`
	Healthy bool   `json:"healthy" doc:"The result of a put, get and delete round trip."`
	Error   string `json:"error,omitempty"`
}

// Routes registers the storage status operation. It needs admin (Appendix B: storage view).
// check runs a storage round trip.
func Routes(api huma.API, store func() Store, check func(context.Context) error) {
	huma.Register(api, httpx.Op("getStorageStatus", http.MethodGet, "/api/v1/storage", httpx.MinRole(kernel.Admin)),
		func(ctx context.Context, _ *struct{}) (*struct{ Body Status }, error) {
			out := Status{Driver: store().Driver(), Healthy: true}
			if err := check(ctx); err != nil {
				out.Healthy, out.Error = false, err.Error()
			}
			return &struct{ Body Status }{Body: out}, nil
		})
}
