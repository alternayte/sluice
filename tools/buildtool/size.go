package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Initial route JavaScript limit (NFR-003).
const maxInitialJSGzip = 350 * 1024

const (
	uiDist       = "ui/dist"
	viteManifest = "ui/dist/.vite/manifest.json"
)

type manifestChunk struct {
	File           string   `json:"file"`
	IsEntry        bool     `json:"isEntry"`
	IsDynamicEntry bool     `json:"isDynamicEntry"`
	Imports        []string `json:"imports"`
	DynamicImports []string `json:"dynamicImports"`
}

// initialJS returns the JS files loaded by the entry: the entry chunk and its static imports.
func initialJS(manifest map[string]manifestChunk) []string {
	seen := map[string]bool{}
	var files []string
	var visit func(key string)
	visit = func(key string) {
		if seen[key] {
			return
		}
		seen[key] = true
		c, ok := manifest[key]
		if !ok {
			return
		}
		if filepath.Ext(c.File) == ".js" {
			files = append(files, c.File)
		}
		for _, imp := range c.Imports {
			visit(imp)
		}
	}
	keys := make([]string, 0, len(manifest))
	for k := range manifest {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if manifest[k].IsEntry {
			visit(k)
		}
	}
	sort.Strings(files)
	return files
}

func gzipSize(b []byte) int {
	var buf bytes.Buffer
	w, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Len()
}

// checkSize returns the gzip size of the initial JS and an error above the limit.
func checkSize(distDir string, manifestJSON []byte, limit int) (int, []string, error) {
	var manifest map[string]manifestChunk
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return 0, nil, fmt.Errorf("parse vite manifest: %w", err)
	}
	files := initialJS(manifest)
	if len(files) == 0 {
		return 0, nil, fmt.Errorf("vite manifest has no entry chunk")
	}
	total := 0
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(distDir, f))
		if err != nil {
			return 0, nil, err
		}
		total += gzipSize(b)
	}
	if total > limit {
		return total, files, fmt.Errorf("initial route JavaScript is %d bytes gzip, limit %d", total, limit)
	}
	return total, files, nil
}

func cmdSizeCheck() error {
	b, err := os.ReadFile(viteManifest)
	if err != nil {
		return fmt.Errorf("size-check: %w (build the UI with manifest: true)", err)
	}
	total, files, err := checkSize(uiDist, b, maxInitialJSGzip)
	for _, f := range files {
		fmt.Println("initial:", f)
	}
	if err != nil {
		return err
	}
	fmt.Printf("size-check: initial JS %.1f KiB gzip (limit %d KiB)\n", float64(total)/1024, maxInitialJSGzip/1024)
	return nil
}
