package main

import (
	"fmt"
	"os"

	"github.com/alternayte/sluice/internal/app"
	"github.com/alternayte/sluice/internal/flow"
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
	schema, err := flow.FlowSchema()
	if err != nil {
		return err
	}
	if err := writeFile("schemas/flow.schema.json", string(schema)); err != nil {
		return err
	}
	vr, err := flow.ValidateResultSchema()
	if err != nil {
		return err
	}
	if err := writeFile("schemas/validate-result.schema.json", string(vr)); err != nil {
		return err
	}
	doc, err := flow.ReferenceDoc()
	if err != nil {
		return err
	}
	return writeFile("docs/reference/flow.md", doc)
}

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
