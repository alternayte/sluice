package execution

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	platformdb "github.com/alternayte/sluice/internal/platform/db"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/snapshot"
)

// NamespaceInfo is the part of a namespace record the execution feature
// needs. Execution owns this type: it does not use the namespace feature's
// generated row type (carry ruling 3).
type NamespaceInfo struct {
	ID             uuid.UUID
	HeadSnapshotID *uuid.UUID
}

// FlowInfo is the part of a flow record the execution feature needs.
// Execution owns this type for the same reason as NamespaceInfo.
type FlowInfo struct {
	ID                uuid.UUID
	NamespaceID       uuid.UUID
	Valid             bool
	CurrentRevisionID *uuid.UUID
}

// Namespaces is what the engine needs from the namespace feature.
// internal/app adapts *namespace.Service to this interface.
type Namespaces interface {
	EnsureBundle(ctx context.Context, m snapshot.Manifest) (string, error)
	Get(ctx context.Context, name string) (NamespaceInfo, error)
	GetFlow(ctx context.Context, namespace, flowID string) (FlowInfo, error)
	Manifest(ctx context.Context, db platformdb.DBTX, snapshotID uuid.UUID) (snapshot.Manifest, error)
	ReadBlob(ctx context.Context, hash string) ([]byte, error)
}

// ErrFileNotFound is returned when the file to run is not in the head snapshot.
// It has the same status, code and message as the error of the namespace feature.
var ErrFileNotFound = httpx.Errorf(http.StatusNotFound, "file_not_found", "file not found")
