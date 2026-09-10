package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/alternayte/sluice/internal/runner"
)

// runExec implements `sluice exec`, the task runner (REQ-RUN-001). It exits with the
// exit code of the task command.
func runExec(ctx context.Context, _ []string, _, stderr io.Writer) int {
	o, err := runner.FromEnv()
	if err != nil {
		fmt.Fprintln(stderr, "sluice exec:", err)
		return exitConfig
	}
	o.Stderr = stderr
	return runner.Run(ctx, o)
}

// runRunnerInstall implements `sluice runner-install <dir>`: it copies this binary to
// <dir>/sluice. Kubernetes init containers use it (D-04).
func runRunnerInstall(_ context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: sluice runner-install <dir>")
		return exitConfig
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, "runner-install:", err)
		return exitFail
	}
	if err := os.MkdirAll(args[0], 0o755); err != nil {
		fmt.Fprintln(stderr, "runner-install:", err)
		return exitFail
	}
	dst := filepath.Join(args[0], "sluice")
	if err := copyExecutable(self, dst); err != nil {
		fmt.Fprintln(stderr, "runner-install:", err)
		return exitFail
	}
	fmt.Fprintf(stdout, "installed %s\n", dst)
	return exitOK
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
