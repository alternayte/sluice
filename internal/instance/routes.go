package instance

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// Instance describes one instance with its online state.
type Instance struct {
	ID          uuid.UUID `json:"id"`
	Hostname    string    `json:"hostname"`
	Version     string    `json:"version"`
	Pools       []string  `json:"pools"`
	Executors   []string  `json:"executors"`
	StartedAt   time.Time `json:"started_at"`
	HeartbeatAt time.Time `json:"heartbeat_at"`
	Online      bool      `json:"online"`
}

// InstanceList is the list of instances.
type InstanceList struct {
	Items []Instance `json:"items"`
}

// Routes registers the instance list operation (REQ-CORE-007).
func Routes(api huma.API, reg *Registry, clk clock.Clock) {
	huma.Register(api, httpx.Op("listInstances", http.MethodGet, "/api/v1/instances", httpx.MinRole(kernel.Admin)),
		func(ctx context.Context, _ *struct{}) (*struct{ Body InstanceList }, error) {
			list, err := reg.List(ctx)
			if err != nil {
				return nil, err
			}
			now := clk.Now()
			out := &struct{ Body InstanceList }{}
			out.Body.Items = make([]Instance, 0, len(list))
			for _, i := range list {
				out.Body.Items = append(out.Body.Items, Instance{
					ID: i.ID, Hostname: i.Hostname, Version: i.Version, Pools: i.Pools, Executors: i.Executors,
					StartedAt: i.StartedAt, HeartbeatAt: i.HeartbeatAt, Online: i.Online(now),
				})
			}
			return out, nil
		})
}
