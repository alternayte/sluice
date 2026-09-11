package app

import (
	"context"
	"fmt"
	"io"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/db"
	"github.com/alternayte/sluice/internal/secret"
)

// runSecretsRekey implements `sluice secrets rekey`: it encrypts all builtin secrets with
// the active master key, the first key of SLUICE_MASTER_KEYS (REQ-SEC-006).
func runSecretsRekey(ctx context.Context, _ []string, stdout, stderr io.Writer) int {
	cfg, code := loadOrExit(false, stderr)
	if cfg == nil {
		return code
	}
	keys, err := masterKeyring(cfg)
	if err != nil {
		fmt.Fprintln(stderr, "secrets rekey:", err)
		return exitConfig
	}
	if !keys.Enabled() {
		fmt.Fprintln(stderr, "secrets rekey: SLUICE_MASTER_KEYS is empty")
		return exitConfig
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(stderr, "secrets rekey:", err)
		return exitFail
	}
	defer pool.Close()
	if _, err := db.Migrate(ctx, pool); err != nil {
		fmt.Fprintln(stderr, "secrets rekey:", err)
		return exitFail
	}
	clk := clock.Real{}
	svc := &secret.Service{Pool: pool, Clock: clk, Audit: &audit.Writer{Pool: pool, Clock: clk}, Keys: keys}
	n, err := svc.Rekey(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "secrets rekey:", err)
		return exitFail
	}
	fmt.Fprintf(stdout, "re-encrypted %d secrets with key %s\n", n, keys.ActiveID())
	return exitOK
}
