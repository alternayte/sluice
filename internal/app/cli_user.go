package app

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/auth"
	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/db"
	"github.com/alternayte/sluice/internal/platform/logging"
)

// cliAuth opens the database and returns an auth service for CLI commands.
func cliAuth(ctx context.Context, stderr io.Writer) (*auth.Service, func(), int) {
	cfg, code := loadOrExit(false, stderr)
	if cfg == nil {
		return nil, nil, code
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, nil, exitFail
	}
	if _, err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		fmt.Fprintln(stderr, err)
		return nil, nil, exitFail
	}
	clk := clock.Real{}
	log := logging.New(stderr, "warn", "text")
	svc := newAuthService(cfg, pool, clk, &audit.Writer{Pool: pool, Clock: clk}, log)
	return svc, pool.Close, exitOK
}

func passwordArg(value string, fromStdin bool) (string, error) {
	if !fromStdin {
		return value, nil
	}
	b, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
	if err != nil {
		return "", err
	}
	s := string(b)
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s, nil
}

// runUserCreate implements `sluice user create` (REQ-AUTH-002).
func runUserCreate(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("user create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	email := fs.String("email", "", "user email (required)")
	name := fs.String("name", "", "display name")
	role := fs.String("role", "viewer", "viewer, operator, editor or admin")
	password := fs.String("password", "", "password")
	stdin := fs.Bool("password-stdin", false, "read the password from stdin")
	temporary := fs.Bool("temporary", false, "force a password change at first login")
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	r, ok := kernel.ParseRole(*role)
	if *email == "" || !ok {
		fmt.Fprintln(stderr, "user create: --email and a valid --role are required")
		return exitConfig
	}
	pw, err := passwordArg(*password, *stdin)
	if err != nil || pw == "" {
		fmt.Fprintln(stderr, "user create: --password or --password-stdin is required")
		return exitConfig
	}
	svc, closeFn, code := cliAuth(ctx, stderr)
	if svc == nil {
		return code
	}
	defer closeFn()
	ctx = audit.WithActor(ctx, audit.Actor{Type: audit.ActorSystem})
	u, err := svc.CreateUser(ctx, *email, *name, r, pw, *temporary)
	if err != nil {
		fmt.Fprintln(stderr, "user create:", err)
		return exitFail
	}
	fmt.Fprintf(stdout, "created user %s (%s) with role %s\n", u.Email, u.ID, u.Role)
	return exitOK
}

// runUserResetPassword implements `sluice user reset-password` (REQ-AUTH-002).
func runUserResetPassword(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("user reset-password", flag.ContinueOnError)
	fs.SetOutput(stderr)
	email := fs.String("email", "", "user email (required)")
	password := fs.String("password", "", "new password")
	stdin := fs.Bool("password-stdin", false, "read the password from stdin")
	temporary := fs.Bool("temporary", false, "force a password change at next login")
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	pw, err := passwordArg(*password, *stdin)
	if *email == "" || err != nil || pw == "" {
		fmt.Fprintln(stderr, "user reset-password: --email and --password (or --password-stdin) are required")
		return exitConfig
	}
	svc, closeFn, code := cliAuth(ctx, stderr)
	if svc == nil {
		return code
	}
	defer closeFn()
	ctx = audit.WithActor(ctx, audit.Actor{Type: audit.ActorSystem})
	u, err := svc.UserByEmail(ctx, *email)
	if err != nil {
		fmt.Fprintln(stderr, "user reset-password:", err)
		return exitFail
	}
	if err := svc.ResetPassword(ctx, u.ID, pw, *temporary); err != nil {
		fmt.Fprintln(stderr, "user reset-password:", err)
		return exitFail
	}
	fmt.Fprintf(stdout, "password reset for %s\n", u.Email)
	return exitOK
}
