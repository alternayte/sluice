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
	group   string
}

// Command groups of the usage text, in order.
const (
	groupClient = "Client commands (read SLUICE_URL and SLUICE_TOKEN; --output json)"
	groupLocal  = "Local commands"
	groupServer = "Server commands"
)

func commands() []command {
	return []command{
		{"run", "Start a flow as <namespace>/<flow>. --wait streams the logs and exits with the end state.", runRun, groupClient},
		{"executions list", "List executions.", runExecutionsList, groupClient},
		{"executions get", "Show one execution with its task runs.", runExecutionsGet, groupClient},
		{"executions logs", "Print the logs of an execution.", runExecutionsLogs, groupClient},
		{"executions cancel", "Cancel an execution.", runExecutionAction("cancel"), groupClient},
		{"executions rerun", "Run an execution again with the same snapshot and inputs.", runExecutionAction("rerun"), groupClient},
		{"executions restart", "Run the failed tasks of an execution again.", runExecutionAction("restart"), groupClient},
		{"flows list", "List flows.", runFlowsList, groupClient},
		{"flows get", "Show a flow as <namespace>/<flow> with its source.", runFlowsGet, groupClient},
		{"namespaces push", "Upload a namespace directory as one new version.", runNamespacesPush, groupClient},
		{"validate", "Validate a namespace directory offline.", runValidate, groupLocal},
		{"init", "Write the Sluice agent skill and an AGENTS.md section into a repository.", runInit, groupLocal},
		{"openapi", "Print the OpenAPI document of the API.", runOpenAPI, groupLocal},
		{"version", "Print version, commit and build date.", runVersion, groupLocal},
		{"server", "Run the HTTP server, scheduler and executors.", runServer, groupServer},
		{"exec", "Run one task run (the runner). Reads SLUICE_API_URL, SLUICE_RUN_TOKEN, SLUICE_TASK_RUN_ID.", runExec, groupServer},
		{"runner-install", "Copy this binary into a directory (runner injection).", runRunnerInstall, groupServer},
		{"migrate", "Apply database migrations and exit.", runMigrate, groupServer},
		{"user create", "Create a user in the database.", runUserCreate, groupServer},
		{"user reset-password", "Set a new password for a user.", runUserResetPassword, groupServer},
		{"secrets rekey", "Re-encrypt all builtin secrets with the active master key.", runSecretsRekey, groupServer},
	}
}

// Commands returns the name and summary of each command, in usage order, for the reference docs.
func Commands() [][2]string {
	var out [][2]string
	for _, c := range commands() {
		out = append(out, [2]string{c.name, c.summary})
	}
	return out
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
	fmt.Fprintf(stderr, "unknown command %q\n\n", strings.Join(args[:min(len(args), 2)], " "))
	usage(stderr)
	return exitConfig
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: sluice <command> [flags]")
	for _, g := range []string{groupClient, groupLocal, groupServer} {
		fmt.Fprintf(w, "\n%s:\n", g)
		for _, c := range commands() {
			if c.group == g {
				fmt.Fprintf(w, "  %-20s %s\n", c.name, c.summary)
			}
		}
	}
	fmt.Fprintln(w, "\nExit codes:")
	for _, e := range ExitCodes {
		fmt.Fprintf(w, "  %-3d %s\n", e.Code, strings.ReplaceAll(e.Meaning, "`", ""))
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
