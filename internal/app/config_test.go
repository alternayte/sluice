package app

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/sluice/internal/storage"
)

func envOf(m map[string]string) map[string]string { return m }

func TestConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig(LoadOptions{Server: true, Version: "1.2.3", Env: envOf(map[string]string{
		"SLUICE_DATABASE_URL": "postgres://x/y",
		"SLUICE_PUBLIC_URL":   "https://sluice.example.com",
		"SLUICE_LISTEN_ADDR":  ":9090",
	})})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkerSlots != 8 || cfg.SessionTTL != 168*time.Hour || cfg.MaxFileBytes != 10<<20 || cfg.MaxBundleBytes != 200<<20 {
		t.Fatalf("defaults: %+v", cfg)
	}
	if cfg.InternalURL != "http://127.0.0.1:9090" || cfg.RunnerImage != "ghcr.io/alternayte/sluice:1.2.3" || !cfg.SecureCookies() {
		t.Fatalf("computed defaults: %s %s", cfg.InternalURL, cfg.RunnerImage)
	}
	if len(cfg.Pools) != 1 || cfg.Pools[0] != "default" || cfg.StorageType != "postgres" {
		t.Fatalf("pools/storage: %v %s", cfg.Pools, cfg.StorageType)
	}
}

func TestConfigListsAllErrors(t *testing.T) {
	_, err := LoadConfig(LoadOptions{Server: true, Env: envOf(map[string]string{
		"SLUICE_WORKER_SLOTS": "-1",
		"SLUICE_STORAGE_TYPE": "s4",
		"SLUICE_MASTER_KEYS":  "k1:short",
	})})
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("want ConfigError, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"SLUICE_DATABASE_URL", "SLUICE_PUBLIC_URL", "SLUICE_WORKER_SLOTS", "SLUICE_STORAGE_TYPE", "SLUICE_MASTER_KEYS"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not name %s:\n%s", want, msg)
		}
	}
}

func TestConfigS3SizeLimitCeiling(t *testing.T) {
	ceiling := strconv.FormatInt(storage.S3MaxObjectBytes, 10)
	above := strconv.FormatInt(storage.S3MaxObjectBytes+1, 10)
	env := func(artifact, bundle string) map[string]string {
		return map[string]string{
			"SLUICE_DATABASE_URL":       "postgres://x/y",
			"SLUICE_STORAGE_TYPE":       "s3",
			"SLUICE_S3_BUCKET":          "b",
			"SLUICE_MAX_ARTIFACT_BYTES": artifact,
			"SLUICE_MAX_BUNDLE_BYTES":   bundle,
		}
	}
	if _, err := LoadConfig(LoadOptions{Env: envOf(env(ceiling, ceiling))}); err != nil {
		t.Fatalf("limits at the ceiling: %v", err)
	}
	_, err := LoadConfig(LoadOptions{Env: envOf(env(above, above))})
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("want ConfigError, got %v", err)
	}
	for _, want := range []string{"SLUICE_MAX_ARTIFACT_BYTES", "SLUICE_MAX_BUNDLE_BYTES"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %s:\n%s", want, err)
		}
	}
	// Other drivers have no such ceiling.
	fs := env(above, above)
	fs["SLUICE_STORAGE_TYPE"] = "fs"
	fs["SLUICE_FS_ROOT"] = t.TempDir()
	if _, err := LoadConfig(LoadOptions{Env: envOf(fs)}); err != nil {
		t.Fatalf("fs with a large limit: %v", err)
	}
}

func TestConfigNamesParseErrors(t *testing.T) {
	_, err := LoadConfig(LoadOptions{Env: envOf(map[string]string{
		"SLUICE_DATABASE_URL": "postgres://x/y",
		"SLUICE_SESSION_TTL":  "soon",
		"SLUICE_WORKER_SLOTS": "many",
	})})
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("want ConfigError, got %v", err)
	}
	for _, want := range []string{"SLUICE_SESSION_TTL", "SLUICE_WORKER_SLOTS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %s:\n%s", want, err)
		}
	}
}

func TestParseByteSize(t *testing.T) {
	cases := map[string]int64{"1024": 1024, "10MiB": 10 << 20, "2 KiB": 2048, "1GiB": 1 << 30}
	for in, want := range cases {
		got, err := ParseByteSize(in)
		if err != nil || got != want {
			t.Errorf("%s: got %d %v", in, got, err)
		}
	}
	if _, err := ParseByteSize("-1"); err == nil {
		t.Error("negative accepted")
	}
}

// TestSCN_DOC_001_EnvDocGenerated checks that every config field has a description and a
// row on the env page of the docs site. `just docs-ref-check` checks that the page is current.
func TestSCN_DOC_001_EnvDocGenerated(t *testing.T) {
	want, err := EnvDoc()
	if err != nil {
		t.Fatal(err)
	}
	const page = "../../site/src/content/docs/reference/env.md"
	got, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(want, "\n") {
		if key, _, ok := strings.Cut(strings.TrimPrefix(line, "| "), " |"); ok && strings.HasPrefix(line, "| `") &&
			!strings.Contains(string(got), strings.NewReplacer("<", "&lt;", ">", "&gt;").Replace(key)) && !strings.Contains(string(got), key) {
			t.Errorf("%s misses %s: run `just gen`", page, key)
		}
	}
	for _, name := range []string{"SLUICE_DATABASE_URL", "SLUICE_SECRET_<KEY>", "SLUICE_AI_MAX_CONTEXT_CHARS"} {
		if !strings.Contains(want, name) {
			t.Errorf("env.md misses %s", name)
		}
	}
}

// TestRunnerImageDefault checks that the default runner image exists: a release version names
// the published image, and any other build names the image of `just build-images`.
func TestRunnerImageDefault(t *testing.T) {
	for version, want := range map[string]string{
		"v0.1.2":                  "ghcr.io/alternayte/sluice:0.1.2",
		"v1.4.0-rc.1":             "ghcr.io/alternayte/sluice:1.4.0-rc.1",
		"1.2.3":                   "ghcr.io/alternayte/sluice:1.2.3",
		"v0.1.2-1-g222619e-dirty": "sluice:dev",
		"222619e":                 "sluice:dev",
		"dev":                     "sluice:dev",
		"":                        "sluice:dev",
	} {
		cfg, err := LoadConfig(LoadOptions{Server: true, Version: version, Env: envOf(map[string]string{
			"SLUICE_DATABASE_URL": "postgres://x/y",
			"SLUICE_PUBLIC_URL":   "https://sluice.example.com",
		})})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.RunnerImage != want {
			t.Errorf("version %q: runner image %s, want %s", version, cfg.RunnerImage, want)
		}
	}
}
