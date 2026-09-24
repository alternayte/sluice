package main

import (
	"fmt"
	"os"

	"github.com/alternayte/sluice/internal/flow"
)

// cmdGen writes generated files that come from Go definitions: the schemas and the generated
// Reference pages of the docs site.
func cmdGen() error {
	schema, err := flow.FlowSchema()
	if err != nil {
		return err
	}
	if err := writeFile("schemas/flow.schema.json", string(schema)); err != nil {
		return err
	}
	nsSchema, err := flow.NamespaceSchema()
	if err != nil {
		return err
	}
	if err := writeFile("schemas/namespace.schema.json", string(nsSchema)); err != nil {
		return err
	}
	vr, err := flow.ValidateResultSchema()
	if err != nil {
		return err
	}
	if err := writeFile("schemas/validate-result.schema.json", string(vr)); err != nil {
		return err
	}
	return writeReferencePages()
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
