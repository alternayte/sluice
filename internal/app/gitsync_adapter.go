package app

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alternayte/sluice/internal/gitsync"
	"github.com/alternayte/sluice/internal/namespace"
	"github.com/alternayte/sluice/internal/secret"
)

// gitNamespaces adapts the namespace service to gitsync.Namespaces.
type gitNamespaces struct{ s *namespace.Service }

func (g gitNamespaces) CreateGitNamespace(ctx context.Context, tx pgx.Tx, name string, sourceID uuid.UUID) (uuid.UUID, error) {
	return g.s.CreateGitNamespace(ctx, tx, name, sourceID)
}

func (g gitNamespaces) CommitGit(ctx context.Context, nsID uuid.UUID, sha, message string, files []gitsync.File) (bool, error) {
	out := make([]namespace.GitFile, len(files))
	for i, f := range files {
		out[i] = namespace.GitFile(f)
	}
	return g.s.CommitGit(ctx, nsID, sha, message, out)
}

// globalSecrets adapts the secret service to gitsync.Secrets: git credentials and webhook
// secrets are global secret keys (REQ-GIT-001).
type globalSecrets struct{ s *secret.Service }

func (g globalSecrets) Global(ctx context.Context, key string) (string, error) {
	return g.s.Resolve(ctx, "", key)
}
