package api

import (
	"context"

	"github.com/alternayte/sluice/internal/api/apigen"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/instance"
)

// Server implements apigen.StrictServerInterface by embedding the feature handlers.
type Server struct {
	System
}

var _ apigen.StrictServerInterface = (*Server)(nil)

// System serves instance and schema operations.
type System struct {
	Instances *instance.Registry
	Clock     clock.Clock
}

// ListInstances lists instances with online state (REQ-CORE-007).
func (h System) ListInstances(ctx context.Context, _ apigen.ListInstancesRequestObject) (apigen.ListInstancesResponseObject, error) {
	list, err := h.Instances.List(ctx)
	if err != nil {
		return nil, err
	}
	now := h.Clock.Now()
	items := make([]apigen.Instance, 0, len(list))
	for _, i := range list {
		items = append(items, apigen.Instance{
			Id:          i.ID,
			Hostname:    i.Hostname,
			Version:     i.Version,
			Pools:       i.Pools,
			Executors:   i.Executors,
			StartedAt:   i.StartedAt,
			HeartbeatAt: i.HeartbeatAt,
			Online:      i.Online(now),
		})
	}
	return apigen.ListInstances200JSONResponse{Items: items}, nil
}
