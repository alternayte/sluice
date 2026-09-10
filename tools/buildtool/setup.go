package main

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// cmdSetup checks the tools of SDD §10.1 and prints the missing ones.
func cmdSetup() error {
	tools := []string{"go", "bun", "uv", "docker", "kind", "kubectl", "helm", "just", "golangci-lint", "git"}
	var missing []string
	for _, t := range tools {
		if _, err := exec.LookPath(t); err != nil {
			missing = append(missing, t)
			fmt.Printf("missing: %s\n", t)
		} else {
			fmt.Printf("ok: %s\n", t)
		}
	}
	// sqlc, oapi-codegen and gotestsum are Go tools pinned in go.mod.
	for _, t := range []string{"sqlc", "oapi-codegen", "gotestsum"} {
		if err := exec.Command("go", "tool", "-n", t).Run(); err != nil {
			missing = append(missing, t)
			fmt.Printf("missing: go tool %s\n", t)
		} else {
			fmt.Printf("ok: go tool %s\n", t)
		}
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		missing = append(missing, "docker daemon")
		fmt.Println("missing: docker daemon is not reachable")
	}
	if len(missing) > 0 {
		return errors.New("setup: missing " + strings.Join(missing, ", "))
	}
	fmt.Println("setup: all tools present")
	return nil
}
