package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/alternayte/sluice/internal/platform/db"
	"github.com/alternayte/sluice/internal/platform/logging"
)

// Exit codes.
const (
	exitOK     = 0
	exitFail   = 1
	exitConfig = 2
)

// command is one CLI command.
type command struct {
	name    string
	summary string
	run     func(ctx context.Context, args []string, stdout, stderr io.Writer) int
}

func commands() []command {
	return []command{
		{"server", "Run the HTTP server, scheduler and executors.", runServer},
		{"exec", "Run one task run (the runner). Reads SLUICE_API_URL, SLUICE_RUN_TOKEN, SLUICE_TASK_RUN_ID.", runExec},
		{"runner-install", "Copy this binary into a directory (runner injection).", runRunnerInstall},
		{"migrate", "Apply database migrations and exit.", runMigrate},
		{"user create", "Create a user in the database.", runUserCreate},
		{"user reset-password", "Set a new password for a user.", runUserResetPassword},
		{"validate", "Validate a namespace directory offline.", runValidate},
		{"version", "Print version, commit and build date.", runVersion},
	}
}

// Main runs the CLI and returns the exit code.
func Main(args []string) int {
	return MainIO(args, os.Stdout, os.Stderr)
}

// MainIO runs the CLI with explicit output streams.
func MainIO(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage(stdout)
		if len(args) == 0 {
			return exitConfig
		}
		return exitOK
	}
	for _, c := range commands() {
		words := strings.Fields(c.name)
		if len(args) < len(words) || strings.Join(args[:len(words)], " ") != c.name {
			continue
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return c.run(ctx, args[len(words):], stdout, stderr)
	}
	fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
	usage(stderr)
	return exitConfig
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: sluice <command> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	for _, c := range commands() {
		fmt.Fprintf(w, "  %-18s %s\n", c.name, c.summary)
	}
}

func runVersion(_ context.Context, _ []string, stdout, _ io.Writer) int {
	fmt.Fprintf(stdout, "version: %s\ncommit: %s\nbuild_date: %s\n", Version, Commit, BuildDate)
	return exitOK
}

func loadOrExit(server bool, stderr io.Writer) (*Config, int) {
	cfg, err := LoadConfig(LoadOptions{Server: server, Version: Version})
	if err != nil {
		var ce *ConfigError
		if errors.As(err, &ce) {
			fmt.Fprintln(stderr, err.Error())
			return nil, exitConfig
		}
		fmt.Fprintln(stderr, err.Error())
		return nil, exitConfig
	}
	return cfg, exitOK
}

func runServer(ctx context.Context, _ []string, _, stderr io.Writer) int {
	cfg, code := loadOrExit(true, stderr)
	if cfg == nil {
		return code
	}
	log := logging.New(stderr, cfg.LogLevel, cfg.LogFormat)
	s, err := NewServer(ctx, cfg, log)
	if err != nil {
		log.Error("startup failed", "err", err)
		return exitFail
	}
	if err := s.Run(ctx); err != nil {
		log.Error("server failed", "err", err)
		return exitFail
	}
	return exitOK
}

func runMigrate(ctx context.Context, _ []string, stdout, stderr io.Writer) int {
	cfg, code := loadOrExit(false, stderr)
	if cfg == nil {
		return code
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFail
	}
	defer pool.Close()
	applied, err := db.Migrate(ctx, pool)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFail
	}
	fmt.Fprintf(stdout, "applied %d migrations\n", len(applied))
	return exitOK
}
