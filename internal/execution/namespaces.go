package execution

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/platform/dbq"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/snapshot"
)

// Namespaces is what the engine needs from the namespace feature.
// internal/app passes *namespace.Service.
type Namespaces interface {
	EnsureBundle(ctx context.Context, m snapshot.Manifest) (string, error)
	Get(ctx context.Context, name string) (dbq.GetNamespaceRow, error)
	GetFlow(ctx context.Context, namespace, flowID string) (dbq.GetFlowRow, error)
	Manifest(ctx context.Context, db dbq.DBTX, snapshotID uuid.UUID) (snapshot.Manifest, error)
	ReadBlob(ctx context.Context, hash string) ([]byte, error)
}

// ErrFileNotFound is returned when the file to run is not in the head snapshot.
// It has the same status, code and message as the error of the namespace feature.
var ErrFileNotFound = httpx.Errorf(http.StatusNotFound, "file_not_found", "file not found")
