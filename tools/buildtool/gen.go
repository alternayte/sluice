package main

import (
	"fmt"
	"os"

	"github.com/alternayte/sluice/internal/app"
)

// cmdGen writes generated files that come from Go definitions.
func cmdGen() error {
	env, err := app.EnvDoc()
	if err != nil {
		return err
	}
	if err := writeFile("docs/reference/env.md", env); err != nil {
		return err
	}
	for _, g := range extraGenerators {
		if err := g(); err != nil {
			return err
		}
	}
	return nil
}

// extraGenerators are added by later features (flow schema, validate-result schema, flow.md).
var extraGenerators []func() error

func writeFile(path, content string) error {
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Println("gen: wrote", path)
	return nil
}
