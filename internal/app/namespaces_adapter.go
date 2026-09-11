package app

import (
	"context"

	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/execution"
	"github.com/alternayte/sluice/internal/namespace"
	platformdb "github.com/alternayte/sluice/internal/platform/db"
	"github.com/alternayte/sluice/internal/snapshot"
)

// namespacesAdapter makes *namespace.Service satisfy execution.Namespaces.
// The two features generate separate sqlc packages (D-10), so their row
// types differ even for the same table. The adapter converts the fields
// execution needs into the small types execution owns (carry ruling 3).
type namespacesAdapter struct {
	*namespace.Service
}

func (a namespacesAdapter) Get(ctx context.Context, name string) (execution.NamespaceInfo, error) {
	ns, err := a.Service.Get(ctx, name)
	if err != nil {
		return execution.NamespaceInfo{}, err
	}
	return execution.NamespaceInfo{ID: ns.ID, HeadSnapshotID: ns.HeadSnapshotID}, nil
}

func (a namespacesAdapter) GetFlow(ctx context.Context, ns, flowID string) (execution.FlowInfo, error) {
	f, err := a.Service.GetFlow(ctx, ns, flowID)
	if err != nil {
		return execution.FlowInfo{}, err
	}
	return execution.FlowInfo{ID: f.ID, NamespaceID: f.NamespaceID, Valid: f.Valid, CurrentRevisionID: f.CurrentRevisionID}, nil
}

func (a namespacesAdapter) Manifest(ctx context.Context, db platformdb.DBTX, snapshotID uuid.UUID) (snapshot.Manifest, error) {
	return a.Service.Manifest(ctx, db, snapshotID)
}
