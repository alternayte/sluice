# Architecture refactor (slice R) implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Sluice lean before slice S5: vertical slices, chi with huma, a generated spec, caarlos0/env config, an easy dev setup, a feature-based UI and a reduced build process, with no change to behaviour.

**Architecture:** Each feature package holds its routes, service, store and SQL. Shared code is only `kernel`, `platform`, `flow`, `storage`, `audit`, `snapshot` and `runnerproto`. The HTTP contract stays the same, so `tests/e2e` proves the refactor. The old generated handler stays on the chi router as a fallback while features move one at a time.

**Tech Stack:** Go 1.26, chi v5, huma v2 (`adapters/humachi`), caarlos0/env v11.4.1, pgx v5, sqlc, air, Bun, Vite, React 19, TanStack Router and Query, `@hey-api/openapi-ts`, Playwright.

**Spec:** `docs/superpowers/specs/2026-09-10-architecture-refactor-design.md`. Read it before you start.

## Global Constraints

- The HTTP contract MUST NOT change: paths, methods, JSON field names, status codes, headers, the error envelope `{"error":{"code","message","details"}}`, and the `sluice_session` cookie.
- The assertions in `tests/e2e/*.go` MUST NOT change. Only `harness_test.go` can change, and only in Task 4.
- After each task, `scripts/e2e-compare.sh` MUST report no regression against `docs/build/e2e-baseline.txt` (created in Task 2).
- C-04: the binary reads only environment variables. C-06: the pgx pool settings in `internal/platform/db/db.go` stay the same.
- REQ-CORE-003: the embedded migrator with `pg_advisory_xact_lock` stays. Do not add the goose library.
- Test names that verify a scenario contain the scenario ID, for example `TestSCN_AUTH_006_RouteInventory`.
- `just forbid` words (`scripts/forbid-words.txt`) MUST NOT appear in code, comments or YAML.
- Prose in comments, commit messages and docs follows ASD-STE100: active voice, no `-ing` forms except technical names, no `should`, `may`, `could` or `would`, no contractions.
- Every commit message ends with the line `Claude-Session: https://claude.ai/code/session_01HwCQCngd1tXrcHmnNajwk3`.
- Shell chains use `set -eo pipefail`. Do not pipe a command that can fail into `tail` or `grep` without a status check.
- Branch: `refactor/architecture`. Do not push.

## File map

| Path | Task | Responsibility |
|---|---|---|
| `docs/sluice-sdd.md` | 1 | Target architecture and process |
| `docs/build/decisions.md`, `docs/build/handover.md` | 1, 19 | Build records |
| `docs/build/e2e-baseline.txt`, `scripts/e2e-compare.sh` | 2 | Refactor proof |
| `internal/app/config.go`, `config_test.go` | 3 | caarlos0/env config and env doc |
| `.env.example`, `justfile`, `.air.toml`, `deploy/compose/*.yml`, `deploy/docker/Dockerfile`, `.dockerignore` | 4 | Dev setup and images |
| `internal/kernel/kernel.go` | 5 | Role, Principal, context helpers |
| `internal/platform/token/token.go` | 5 | Random secrets and hashes |
| `internal/snapshot/` | 5 | Manifest type and bundle format |
| `internal/runnerproto/protocol.go` | 5 | Runner wire types and env names |
| `internal/instance/` | 5, 10 | Instance registry and route |
| `internal/platform/httpx/api.go`, `api_test.go` | 6 | huma setup, access, errors, `Raw` |
| `internal/app/server.go`, `internal/app/routes.go`, `internal/app/cli_openapi.go` | 7 | chi router, route registration, `sluice openapi` |
| `internal/auth/routes.go` | 9 | Auth, users and tokens operations |
| `internal/audit/routes.go`, `internal/instance/routes.go` | 10 | Audit and instances operations |
| `internal/namespace/routes.go` | 11 | Namespace, file and flow operations |
| `internal/execution/routes.go`, `internal/execution/runner_routes.go` | 12 | Execution and runner operations |
| `internal/app/inventory_integration_test.go` | 13 | SCN-AUTH-006 |
| `sqlc.yaml`, `internal/<feature>/queries.sql`, `internal/<feature>/<feature>db/` | 14 | SQL per feature |
| `ui/openapi-ts.config.ts`, `ui/src/api-client.ts` | 16 | Generated client |
| `ui/src/features/**`, `ui/src/routes/**`, `ui/eslint.config.js` | 17 | Feature folders |
| `tools/buildtool/*` | 18 | Reduced process tools |

---

### Task 1: Update the SDD and the build records

**Files:**
- Modify: `docs/sluice-sdd.md`
- Modify: `docs/build/decisions.md`
- Modify: `docs/build/handover.md`

**Interfaces:**
- Consumes: the approved design doc.
- Produces: the SDD text that all later tasks follow.

- [ ] **Step 1: Change the metadata table.** In `docs/sluice-sdd.md`, set `| Version | SDD 1.1 |`.

- [ ] **Step 2: Replace §0 rule 4.** Replace the line that starts with `4. Scenario test layers:` with:

```markdown
4. Scenario test layers: `[E]` end-to-end against the built binary through HTTP and the CLI, `[U]` Playwright against the built binary, `[I]` Go test against the public API of one package or against real infrastructure, `[K]` kind cluster, `[P]` performance. A test calls a public boundary and checks an outcome. Tests do not mock Sluice code. If a refactor keeps the behaviour and a test breaks, the test is at the wrong boundary.
```

- [ ] **Step 3: Replace D-09 and D-10 in §3.**

```markdown
| D-09 | The API is code-first. Each feature registers huma v2 operations on a chi v5 router. `sluice openapi` prints the OpenAPI 3.1 document. `api/openapi.yaml` is generated and committed. The TS client uses `@hey-api/openapi-ts` with the TanStack Query plugin. | One declaration per route: types, path and role. Contract generated on both sides. | Hand-written spec with oapi-codegen. Hand-written clients. |
| D-10 | `pgx` v5 and `sqlc` with one generated package per feature. Migrations use the goose file format and a small embedded runner with a transaction-scoped advisory lock. | Typed SQL, no ORM. goose lockers are session-scoped or table-based, and a session lock is not safe through a transaction-mode pooler. | ORM. The goose library runner. |
```

- [ ] **Step 4: Append to the D-16 reason cell.** Add: ` A later version can adopt auth-all when it has roles, API keys and admin plugins.`

- [ ] **Step 5: Add decisions after D-20.**

```markdown
| D-21 | Configuration uses `caarlos0/env` v11 on one `Config` struct. The justfile loads `.env` for local work. The binary reads only the process environment. | No config code to maintain. C-04 stays true. | Custom reflection loader. viper. |
| D-22 | Packages are vertical slices. A feature holds `routes.go`, `service.go`, `store.go` and `queries.sql`. A feature does not import another feature. Shared packages: `kernel`, `platform`, `flow`, `storage`, `audit`, `snapshot`, `runnerproto`. `execution` also imports `executor`. Only `internal/app` imports all packages. depguard enforces the rules. | Colocated features with no import loops. | Layered packages. |
| D-23 | Every operation declares its access with `httpx.Op`. The server does not start when an operation has no access. | Default deny in one place (SI-03). | A separate permission table. |
| D-24 | Playwright locators use `getByRole` first, then `getByLabel`, then `getByText` for messages. `data-testid` is only for elements with no accessible name. | Playwright guidance. Role locators also check accessibility. | Test ids everywhere. |
| D-25 | Local development: `just setup`, `just up`, `just dev`. `deploy/compose/dev.yml` holds Postgres. `air` reloads the server. Vite proxies `/api` to Go. | One command per step. | Manual steps. |
```

- [ ] **Step 6: Replace §4.4.** Replace the heading text and the code block with:

````markdown
### 4.4 Repository layout (vertical slices)

```
cmd/sluice/            main
internal/app/          config, wiring, server, CLI commands, `sluice openapi`
internal/kernel/       Role, Principal, context helpers (imports nothing internal)
internal/platform/     db, httpx (huma setup, errors, access), token, clock, logging,
                       masking, lease, page, health, promx
internal/flow/         shared domain: flow model, parse, validate, template, schema
internal/snapshot/     shared domain: snapshot manifest and bundle format
internal/runnerproto/  shared: runner wire types, env names, limits
internal/storage/      shared: blob store interface and drivers
internal/audit/        shared writer, audit route
internal/auth/         routes.go service.go store.go queries.sql authdb/
internal/instance/     instance registry, instances route
internal/namespace/    namespaces, files, snapshots, flows
internal/execution/    engine, dispatcher, state, execution routes, runner routes
internal/executor/     inline, process, docker, kubernetes adapters of execution
internal/runner/       `sluice exec` client
internal/trigger/  internal/secret/  internal/variable/  internal/gitsync/
internal/metrics/  internal/ai/        later slices, same shape
api/openapi.yaml       generated by `sluice openapi`
db/migrations/         one ordered schema
schemas/  ui/  deploy/  examples/elt/
tests/e2e/  tests/ui/  tests/k8s/  tests/perf/  tests/fixtures/
docs/sluice-sdd.md  docs/build/  docs/reference/
justfile  .env.example
```

Dependency rules:

1. `kernel` imports no internal package.
2. `platform` imports only `kernel` and `platform`.
3. A feature imports `kernel`, `platform` and the shared packages. `execution` also imports `executor`.
4. A feature does not import another feature. When it needs one, it declares a small interface in its own package, and `internal/app` connects the two.
5. Only `internal/app` imports all packages.
````

- [ ] **Step 7: Replace REQ-API-001.**

```markdown
| REQ-API-001 | `sluice openapi` MUST print the OpenAPI document of all `/api/v1` and `/api/runner/v1` operations. `api/openapi.yaml` holds its output. Generated Go and TS code MUST match (`just gen-check`). |
```

- [ ] **Step 8: Replace §10.1 and the §10.2 table.** §10.1 text:

```markdown
Go (version in `go.mod`), Bun, uv, Docker, kind, kubectl, Helm, just, golangci-lint. Go tools sqlc, air and gotestsum are pinned with the `tool` directive in `go.mod`. `just setup` checks all tools and prints missing ones. PyPI and container registries must be reachable for image builds and examples.
```

§10.2 table:

```markdown
| Recipe | Content |
|---|---|
| `setup` | Tool check. Copy `.env.example` to `.env` if absent. Install Bun packages. |
| `up`, `down` | Start or stop Postgres from `deploy/compose/dev.yml`. |
| `dev` | `air` for the Go server and the Vite dev server together. |
| `migrate` | Apply migrations. |
| `db-reset` | Drop and create the dev database, then migrate. |
| `gen` | sqlc, `sluice openapi`, hey-api, flow schema, validate-result schema, reference docs. |
| `gen-check` | `gen`, then `git diff --exit-code`. |
| `lint` | golangci-lint with depguard, `bun run lint`, `tsc --noEmit`, `helm lint`, `forbid`. |
| `forbid` | Fails on `TODO`, `FIXME`, `XXX`, `HACK`, `not implemented`, `unimplemented` in `.go`, `.ts`, `.tsx`, `.py`, `.sh`, `.yaml` files outside generated code and `docs/` (word list in `scripts/forbid-words.txt`, the only excluded file); fails on `t.Skip`, `test.skip`, `test.only`, `describe.skip` in any test file. No bypass marker exists. |
| `test` | Go tests with `-race` and the `integration` tag (testcontainers), Vitest. |
| `build` | UI build, size check, Go build with embedded UI, images. |
| `e2e` | API end-to-end and Playwright against the built binary. |
| `e2e-k8s` | kind cluster, image load, Helm install, `[K]` scenarios, cluster delete. |
| `perf` | `[P]` scenarios. |
| `trace` | §10.3. |
| `check` | `gen-check lint test`. |
```

- [ ] **Step 9: Replace §10.3 rules 2 and 3.**

```markdown
2. Every scenario ID has at least one test in the JUnit reports in `build/reports/junit/` whose name contains the ID.
3. Every such test passed in those reports. No test for a scenario was skipped.
```

Delete the phrase `(tests under \`tests/review/\` use finding IDs)` from rule 4.

- [ ] **Step 10: Change §11.** Add this row between S4 and S5:

```markdown
| R | Refactor to vertical slices, chi and huma, generated spec, caarlos0/env, `.env.example`, justfile, dev compose, `sluice` image Dockerfile, hey-api client, UI feature folders, reduced build process. No new IDs. All S0 to S4 scenarios that passed before keep passing. | — |
```

In the S8 row, change `Docker executor, images, compose, single-container mode` to `Docker executor, sluice-uv image, production compose, single-container mode`. Replace `After S12: \`just verify\`, then §13, then §14.` with `After S12: §13.`

- [ ] **Step 11: Replace §12, §13 and §14.** Delete from `## 12. Build records` to the line before `## Appendix A`. Insert:

```markdown
## 12. Build records

`docs/build/` is owned by the implementer. The SDD is not.

### 12.1 Decisions — `docs/build/decisions.md`

Two tables. Implementation decisions: `ID (DI-n) | date | area or IDs | decision | reason`. Human answers: `H-<n> | answers B-<n> | date | answer`. The human writes answers. The implementer reads them. A blocked item gets an entry `B-<n>` with date, item IDs, question and options with the effect of each.

### 12.2 Handover — `docs/build/handover.md`

Where the build stopped, open work per slice, and how to run the checks. The implementer updates it at the end of each session.

`build/` is in `.gitignore`.

---

## 13. Definition of done

v1 is DONE when all are true at one commit with a clean tree:

1. `just check` exits 0.
2. `just build`, `just e2e`, `just e2e-k8s` and `just perf` exit 0.
3. `just trace` exits 0 on the JUnit reports of those runs.
4. `docs/build/handover.md` lists no open work and `docs/build/decisions.md` has no open blocked entry.

---
```

- [ ] **Step 12: Check that no reference to removed sections stays.** Run:

```bash
grep -nE "ledger|evidence|verify\b|§13|§14|tests/review|3\.0\.3|oapi-codegen|openapi-typescript|openapi-fetch" docs/sluice-sdd.md
```

Expected: only the D-09 "Rejected" cell names oapi-codegen. Fix any other hit. For example, change `tests/review/` in §4.4 and `test-int` in any text.

- [ ] **Step 13: Update `docs/build/decisions.md`.** Append these rows to the implementation table:

```markdown
| DI-22 | 2026-09-10 | SDD §0 rule 1 | The SDD edit for slice R is a one-time exception to §0 rule 1. The human approved it in the design review (`docs/superpowers/specs/2026-09-10-architecture-refactor-design.md`). Rule 1 stays in force after this. | The SDD is the only memory that survives a context refresh. It must describe the target architecture. |
| DI-23 | 2026-09-10 | D-09, SI-03 | Streamed operations (`getFile`, `uploadFile`, `streamExecutionLogs`, `downloadExecutionLogs`, `streamExecutionEvents`, `downloadArtifact`, `runnerGetBundle`, `runnerPutArtifact`) use `httpx.Raw`: a spec entry plus a plain chi handler behind the same access check. | These bodies must not be buffered. One access check for all routes. |
```

In the rows DI-6, DI-7, DI-8 and DI-11, append to the reason cell: ` Superseded in slice R (D-09, D-10, D-22, D-23).`

- [ ] **Step 14: Rewrite the top of `docs/build/handover.md`.** Replace the first paragraph and the section `## State at the last commit` with:

```markdown
# Build handover

This file records where the build stopped. `decisions.md` is the authoritative record of decisions.

## State at the last commit

- Slices S0 to S4 are committed. Slice R (architecture refactor) is in progress on branch `refactor/architecture`.
- Plan: `docs/superpowers/plans/2026-09-10-architecture-refactor.md`. The checkboxes show the progress.
- Design: `docs/superpowers/specs/2026-09-10-architecture-refactor-design.md`.
- Refactor proof: `scripts/e2e-compare.sh` against `docs/build/e2e-baseline.txt`.
```

Keep the section `## Open work in S4` and the list of Playwright labels. In `## How to run checks` and `## Next slices`, replace `just verify`, `SDD §13 review and §14` with `SDD §13 definition of done`.

- [ ] **Step 15: Commit.**

```bash
git add docs/sluice-sdd.md docs/build/decisions.md docs/build/handover.md
git commit -m "SDD 1.1: vertical slices, chi and huma, reduced build process

Claude-Session: https://claude.ai/code/session_01HwCQCngd1tXrcHmnNajwk3"
```

---

### Task 2: Record the e2e baseline

**Files:**
- Create: `scripts/e2e-compare.sh`
- Create: `docs/build/e2e-baseline.txt`

**Interfaces:**
- Produces: `scripts/e2e-compare.sh [E2E_RUN=<regexp>]`. Exit 0 when no test that passed in the baseline fails now.

- [ ] **Step 1: Write the script.**

```bash
#!/usr/bin/env bash
# Run tests/e2e and compare the result with docs/build/e2e-baseline.txt.
# The script fails when a test that passed in the baseline does not pass now.
# Set E2E_RUN to a go test -run expression to run a subset.
# Set E2E_WRITE_BASELINE=1 to write the baseline instead of a compare.
set -uo pipefail
cd "$(dirname "$0")/.."
baseline=docs/build/e2e-baseline.txt
out=$(mktemp)
args=(-tags e2e -count=1 -timeout 60m -v)
if [ -n "${E2E_RUN:-}" ]; then args+=(-run "$E2E_RUN"); fi
go test "${args[@]}" ./tests/e2e/... >"$out.log" 2>&1
grep -E '^[[:space:]]*--- (PASS|FAIL|SKIP)' "$out.log" | sed -E 's/^[[:space:]]+//; s/ \([0-9.]+s\)$//' | sort -u >"$out"
if [ "${E2E_WRITE_BASELINE:-}" = "1" ]; then
  cp "$out" "$baseline"
  echo "wrote $baseline: $(grep -c '^--- PASS' "$baseline") pass, $(grep -c '^--- FAIL' "$baseline") fail"
  exit 0
fi
regressed=0
while read -r line; do
  name=${line#--- PASS: }
  if grep -qx -- "--- PASS: $name" "$out"; then continue; fi
  if [ -n "${E2E_RUN:-}" ] && ! grep -q -- ": $name\$" "$out"; then continue; fi
  echo "REGRESSION: $name"
  regressed=1
done < <(grep '^--- PASS' "$baseline")
if [ "$regressed" = "1" ]; then
  echo "log: $out.log"
  exit 1
fi
echo "no regression ($(grep -c '^--- PASS' "$out") pass)"
```

- [ ] **Step 2: Make it executable.** Run: `chmod +x scripts/e2e-compare.sh`

- [ ] **Step 3: Write the baseline.** Docker must run. Run: `E2E_WRITE_BASELINE=1 scripts/e2e-compare.sh`
Expected: `wrote docs/build/e2e-baseline.txt: N pass, M fail`. The handover says that SCN-CORE-007 fails. A FAIL line in the baseline is correct: the script ignores failures that exist in the baseline.

- [ ] **Step 4: Check the script against itself.** Run: `scripts/e2e-compare.sh`
Expected: `no regression (N pass)`. If a test is flaky and fails only on this run, run the script again. Record each flaky test name in `docs/build/handover.md` under `## Open work in S4`.

- [ ] **Step 5: Commit.**

```bash
git add scripts/e2e-compare.sh docs/build/e2e-baseline.txt docs/build/handover.md
git commit -m "Add e2e baseline and compare script for the refactor

Claude-Session: https://claude.ai/code/session_01HwCQCngd1tXrcHmnNajwk3"
```

---

### Task 3: Config with caarlos0/env

**Files:**
- Modify: `internal/app/config.go` (full replacement of the loader, keep `ParseByteSize`, `ParseMasterKeys`, `MasterKey`, `Port`, `SecureCookies`, `SecretEnvPrefix`)
- Modify: `internal/app/config_test.go`
- Modify: `internal/app/cli.go:86-98` (`loadOrExit`)
- Modify: `internal/app/routes_integration_test.go:28-33`
- Modify: `docs/reference/env.md` (regenerated)

**Interfaces:**
- Produces: `LoadConfig(LoadOptions{Server bool, Version string, Env map[string]string}) (*Config, error)`. A nil `Env` reads the process environment. `EnvDoc() (string, error)` keeps its signature.

- [ ] **Step 1: Add the dependency.** Run: `go get github.com/caarlos0/env/v11@v11.4.1`

- [ ] **Step 2: Change the tests first.** In `config_test.go`, replace `envOf` and its callers:

```go
func envOf(m map[string]string) map[string]string { return m }
```

Change each `Getenv: envOf(...)` to `Env: envOf(...)`. Add this test:

```go
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
```

- [ ] **Step 3: Run the tests to see them fail.** Run: `go test ./internal/app/ -run 'TestConfig|TestParseByteSize'`
Expected: compile error `unknown field Env in struct literal`.

- [ ] **Step 4: Replace the loader.** In `config.go`, replace everything from `// ByteSize is a size in bytes` down to the end of `setField` with the code below. Keep `ParseByteSize` unchanged. In the `Config` struct, change each tag as follows:
  - `default:"x"` becomes `envDefault:"x"`.
  - `required:"always"` becomes `,required` inside the `env` tag: `env:"SLUICE_DATABASE_URL,required"`.
  - `required:"server"` is removed, and the field gets `docDefault:"required for \`server\`"`.
  - A default with `<` becomes `docDefault:"\`http://127.0.0.1:<port>\`"` with the same text as before, and no `envDefault`.
  - Remove `enum:"..."` and `min:"..."` tags. `validate` checks them now.
  - Keep `desc` and `secret` tags.

```go
// ByteSize is a size in bytes. It parses values like 10MiB, 512KiB or 1024.
type ByteSize int64

// UnmarshalText parses a byte size for caarlos0/env.
func (b *ByteSize) UnmarshalText(text []byte) error {
	n, err := ParseByteSize(string(text))
	if err != nil {
		return err
	}
	*b = ByteSize(n)
	return nil
}

// LoadOptions selects the checks that apply and the source of the values.
type LoadOptions struct {
	// Server adds the checks that only `sluice server` needs.
	Server  bool
	Version string
	// Env replaces the process environment when it is not nil. Tests use it.
	Env map[string]string
}

// LoadConfig reads the configuration from the environment and validates it.
// It returns a ConfigError that lists all problems (REQ-CORE-002).
func LoadConfig(opts LoadOptions) (*Config, error) {
	cfg := &Config{}
	var problems []string
	if err := env.ParseWithOptions(cfg, env.Options{Environment: opts.Env}); err != nil {
		problems = append(problems, parseProblems(err)...)
	}
	cfg.Pools = trimList(cfg.Pools)
	cfg.Executors = trimList(cfg.Executors)
	problems = append(problems, cfg.validate(opts.Server)...)
	if len(problems) > 0 {
		return nil, &ConfigError{Problems: problems}
	}
	cfg.applyComputedDefaults(opts.Version)
	return cfg, nil
}

// parseProblems turns caarlos0/env errors into "VARIABLE: problem" lines.
func parseProblems(err error) []string {
	var agg env.AggregateError
	if !errors.As(err, &agg) {
		return []string{err.Error()}
	}
	keys := tagsByField("env")
	var out []string
	for _, e := range agg.Errors {
		var notSet env.EnvVarIsNotSetError
		var empty env.EmptyEnvVarError
		var parse env.ParseError
		switch {
		case errors.As(e, &notSet):
			out = append(out, notSet.Key+": required")
		case errors.As(e, &empty):
			out = append(out, empty.Key+": required")
		case errors.As(e, &parse):
			out = append(out, fmt.Sprintf("%s: %v", keys[parse.Name], parse.Err))
		default:
			out = append(out, e.Error())
		}
	}
	return out
}

// tagsByField maps each Config field name to the first part of one struct tag.
func tagsByField(tag string) map[string]string {
	out := map[string]string{}
	t := reflect.TypeOf(Config{})
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		v, _, _ := strings.Cut(f.Tag.Get(tag), ",")
		out[f.Name] = v
	}
	return out
}

func trimList(in []string) []string {
	var out []string
	for _, p := range in {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
```

Change `func (c *Config) validate() []string` to `func (c *Config) validate(server bool) []string` and add these checks at its start:

```go
	var p []string
	if server && c.PublicURL == "" {
		p = append(p, "SLUICE_PUBLIC_URL: required")
	}
	oneOf := func(name, val string, allowed ...string) {
		if !contains(allowed, val) {
			p = append(p, fmt.Sprintf("%s: must be one of %s, got %q", name, strings.Join(allowed, ","), val))
		}
	}
	oneOf("SLUICE_LOG_LEVEL", c.LogLevel, "debug", "info", "warn", "error")
	oneOf("SLUICE_LOG_FORMAT", c.LogFormat, "json", "text")
	oneOf("SLUICE_STORAGE_TYPE", c.StorageType, "postgres", "fs", "s3", "azblob")
	atLeast := func(name string, val, min int) {
		if val < min {
			p = append(p, fmt.Sprintf("%s: must be at least %d, got %d", name, min, val))
		}
	}
	atLeast("SLUICE_WORKER_SLOTS", c.WorkerSlots, 0)
	atLeast("SLUICE_RETENTION_DAYS", c.RetentionDays, 1)
	atLeast("SLUICE_K8S_MAX_JOBS", c.K8sMaxJobs, 1)
	atLeast("SLUICE_AI_MAX_CONTEXT_CHARS", c.AIMaxContextChars, 1000)
	for name, d := range map[string]time.Duration{
		"SLUICE_SESSION_TTL": c.SessionTTL, "SLUICE_QUEUE_POLL_INTERVAL": c.QueuePollInterval,
		"SLUICE_HEARTBEAT_TIMEOUT": c.HeartbeatTimeout, "SLUICE_SHUTDOWN_GRACE": c.ShutdownGrace,
		"SLUICE_K8S_JOB_TTL": c.K8sJobTTL, "SLUICE_K8S_PENDING_TIMEOUT": c.K8sPendingTimeout,
		"SLUICE_SECRET_CACHE_TTL": c.SecretCacheTTL,
	} {
		if d <= 0 {
			p = append(p, name+": must be a positive duration like 30s")
		}
	}
```

Remove the old `var p []string` line in `validate`. Sort `p` before return with `sort.Strings(p)` so that the output order is stable. Delete the constants `requiredAlways` and `requiredServer`.

- [ ] **Step 5: Replace `EnvDoc`.**

```go
// EnvDoc renders docs/reference/env.md from the Config struct (REQ-DOC-001).
func EnvDoc() (string, error) {
	params, err := env.GetFieldParams(&Config{})
	if err != nil {
		return "", err
	}
	descs := tagsByKey("desc")
	docDefaults := tagsByKey("docDefault")
	var b strings.Builder
	b.WriteString("# Environment variables\n\n")
	b.WriteString("Generated from `internal/app/config.go` by `just gen`. Do not edit.\n\n")
	b.WriteString("| Variable | Default | Description |\n|---|---|---|\n")
	var missing []string
	for _, p := range params {
		def := "empty"
		switch {
		case docDefaults[p.Key] != "":
			def = docDefaults[p.Key]
		case p.Required:
			def = "required"
		case p.HasDefaultValue:
			def = "`" + p.DefaultValue + "`"
		}
		if descs[p.Key] == "" {
			missing = append(missing, p.Key)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", p.Key, def, descs[p.Key])
	}
	fmt.Fprintf(&b, "| `%s<KEY>` | empty | Value of secret `<KEY>` for the env secret provider. |\n", SecretEnvPrefix)
	b.WriteString("\nAzure credentials use the standard `AZURE_*` variables, workload identity or managed identity.\n")
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("config fields without description: %s", strings.Join(missing, ", "))
	}
	return b.String(), nil
}

// tagsByKey maps each environment variable name to the value of one struct tag.
func tagsByKey(tag string) map[string]string {
	out := map[string]string{}
	t := reflect.TypeOf(Config{})
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		key, _, _ := strings.Cut(f.Tag.Get("env"), ",")
		out[key] = f.Tag.Get(tag)
	}
	return out
}
```

Remove the unused imports (`strconv`, `net/url` if unused, and so on) with `go build ./internal/app/`.

- [ ] **Step 6: Update the callers.** In `cli.go` `loadOrExit`, keep `LoadConfig(LoadOptions{Server: server, Version: Version})`. It needs no change. In `routes_integration_test.go`, replace the `Getenv` function with:

```go
	cfg, err := LoadConfig(LoadOptions{Server: true, Env: map[string]string{
		"SLUICE_DATABASE_URL": dbURL,
		"SLUICE_PUBLIC_URL":   "http://127.0.0.1:8080",
	}})
```

Run `grep -rn "Getenv:" --include=*.go .`. Expected: no hit.

- [ ] **Step 7: Run the tests.** Run: `go test ./internal/app/ -run 'TestConfig|TestParseByteSize'`
Expected: PASS. The type assertions in `parseProblems` depend on the error types of caarlos0/env v11.4.1. If `TestConfigNamesParseErrors` fails, print the error with `t.Logf("%#v", err)`. Then change the switch to the types that you see. Do not change the test.

- [ ] **Step 8: Regenerate the env doc.** Run: `go run ./tools/buildtool gen && go test ./internal/app/ -run TestSCN_DOC_001`
Expected: PASS. Check `git diff docs/reference/env.md`. Only the order and the formatting of defaults can change. No variable can be missing.

- [ ] **Step 9: Check the CLI scenario and the regression.** Run: `E2E_RUN='SCN_CORE_001|SCN_AUTH_002' scripts/e2e-compare.sh`
Expected: `no regression`.

- [ ] **Step 10: Commit.**

```bash
go mod tidy
git add go.mod go.sum internal/app docs/reference/env.md
git commit -m "Config: use caarlos0/env

Claude-Session: https://claude.ai/code/session_01HwCQCngd1tXrcHmnNajwk3"
```

---

### Task 4: Dev setup, compose and Dockerfile

**Files:**
- Create: `.env.example`, `.air.toml`, `.dockerignore`, `deploy/compose/dev.yml`, `deploy/compose/compose.yml`, `deploy/docker/Dockerfile`
- Modify: `justfile`, `.gitignore`, `tests/e2e/harness_test.go` (function `baseEnv` only), `go.mod` (tool directive)

**Interfaces:**
- Produces: the recipes `setup`, `up`, `down`, `dev`, `migrate`, `db-reset`, `e2e-compare`. `just` loads `.env`.

- [ ] **Step 1: Protect the e2e harness from `.env`.** `just` exports the `.env` values into every recipe. Read `baseEnv` in `tests/e2e/harness_test.go`. It must drop inherited variables that start with `SLUICE_`. If it copies `os.Environ()` without a filter, change it to:

```go
// baseEnv returns the process environment without SLUICE_* variables, plus env.
// A developer .env file must not change the servers under test.
func baseEnv(env map[string]string) []string {
	var out []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "SLUICE_") {
			out = append(out, kv)
		}
	}
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
```

Keep the signature that the file uses now. Apply the same rule in `tests/ui/global-setup.ts` where it spawns the binary: build the child `env` from `process.env` without keys that start with `SLUICE_`, then add the test values.

- [ ] **Step 2: Create `.env.example`.**

```dotenv
# Local development values. `just setup` copies this file to .env.
# The justfile loads .env. The binary reads only environment variables.
SLUICE_DATABASE_URL=postgres://sluice:sluice@localhost:5432/sluice?sslmode=disable
# The browser opens the Vite dev server. Vite sends /api to the Go server on :8080.
SLUICE_PUBLIC_URL=http://localhost:5173
SLUICE_LISTEN_ADDR=:8080
SLUICE_LOG_FORMAT=text
SLUICE_LOG_LEVEL=debug
SLUICE_BOOTSTRAP_ADMIN_EMAIL=admin@local.test
SLUICE_BOOTSTRAP_ADMIN_PASSWORD=change-me-local-1
# 32 bytes in base64. For a real key, run: openssl rand -base64 32
SLUICE_MASTER_KEYS=dev:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
SLUICE_STORAGE_TYPE=postgres
```

Add `.env` to `.gitignore`.

- [ ] **Step 3: Create `deploy/compose/dev.yml`.** Use the same Postgres image tag as `internal/testutil/pgtest`. Find the tag with `grep -rn 'postgres:' internal/testutil/pgtest`. The tag below is only an example.

```yaml
name: sluice-dev
services:
  postgres:
    image: postgres:17-alpine
    environment:
      POSTGRES_USER: sluice
      POSTGRES_PASSWORD: sluice
      POSTGRES_DB: sluice
    ports:
      - "5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U sluice -d sluice"]
      interval: 2s
      timeout: 2s
      retries: 30
volumes:
  pgdata: {}
```

- [ ] **Step 4: Create `deploy/docker/Dockerfile`.** Only the `sluice` target. The `sluice-uv` target comes in S8.

```dockerfile
# syntax=docker/dockerfile:1
FROM oven/bun:1 AS ui
WORKDIR /src/ui
COPY ui/package.json ui/bun.lock ./
RUN bun install --frozen-lockfile
COPY ui/ ./
RUN bun run build

FROM golang:1.26 AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/ui/dist ./ui/dist
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/alternayte/sluice/internal/app.Version=${VERSION} -X github.com/alternayte/sluice/internal/app.Commit=${COMMIT}" \
    -o /out/sluice ./cmd/sluice

FROM gcr.io/distroless/static-debian12:nonroot AS sluice
COPY --from=go /out/sluice /usr/local/bin/sluice
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/sluice"]
CMD ["server"]
```

Create `.dockerignore`:

```
.git
.env
bin
build
node_modules
ui/node_modules
ui/dist
tests/ui/node_modules
```

- [ ] **Step 5: Create `deploy/compose/compose.yml`** for Coolify and local trials.

```yaml
name: sluice
services:
  postgres:
    image: postgres:17-alpine
    environment:
      POSTGRES_USER: sluice
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:-sluice}
      POSTGRES_DB: sluice
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U sluice -d sluice"]
      interval: 2s
      timeout: 2s
      retries: 30
  sluice:
    build:
      context: ../..
      dockerfile: deploy/docker/Dockerfile
      target: sluice
    image: sluice:dev
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      SLUICE_DATABASE_URL: postgres://sluice:${POSTGRES_PASSWORD:-sluice}@postgres:5432/sluice?sslmode=disable
      SLUICE_PUBLIC_URL: ${SLUICE_PUBLIC_URL:-http://localhost:8080}
      SLUICE_BOOTSTRAP_ADMIN_EMAIL: ${SLUICE_BOOTSTRAP_ADMIN_EMAIL:-admin@local.test}
      SLUICE_BOOTSTRAP_ADMIN_PASSWORD: ${SLUICE_BOOTSTRAP_ADMIN_PASSWORD:?set SLUICE_BOOTSTRAP_ADMIN_PASSWORD}
      SLUICE_MASTER_KEYS: ${SLUICE_MASTER_KEYS:?set SLUICE_MASTER_KEYS}
    ports:
      - "8080:8080"
```

The image tag must be the same as in `dev.yml`.

- [ ] **Step 6: Add air as a Go tool and create `.air.toml`.** Run: `go get -tool github.com/air-verse/air@latest`

```toml
root = "."
tmp_dir = "build/air"

[build]
  cmd = "go build -o build/air/sluice ./cmd/sluice"
  full_bin = "build/air/sluice server"
  include_ext = ["go", "sql"]
  exclude_dir = ["ui", "tests", "build", "bin", "docs", "node_modules", "deploy"]
  delay = 300

[log]
  time = false
```

- [ ] **Step 7: Change the justfile.** Add `set dotenv-load` after the `set shell` line. Replace the `setup` recipe and add the new recipes after `default`:

```just
# Check the tools, create .env and install the UI packages.
setup:
    go run ./tools/buildtool setup
    test -f .env || cp .env.example .env
    cd ui && bun install --frozen-lockfile
    cd tests/ui && bun install --frozen-lockfile

# Start Postgres for development.
up:
    docker compose -f deploy/compose/dev.yml up -d --wait

# Stop the development services.
down:
    docker compose -f deploy/compose/dev.yml down

# Run the Go server with live reload and the Vite dev server.
dev:
    #!/usr/bin/env bash
    set -euo pipefail
    trap 'kill 0' EXIT
    go tool air &
    (cd ui && bun run dev) &
    wait

# Apply the database migrations.
migrate:
    go run ./cmd/sluice migrate

# Drop the development database, create it again and apply the migrations.
db-reset:
    docker compose -f deploy/compose/dev.yml exec -T postgres psql -U sluice -d postgres -c 'DROP DATABASE IF EXISTS sluice WITH (FORCE)' -c 'CREATE DATABASE sluice'
    go run ./cmd/sluice migrate

# Compare tests/e2e with the refactor baseline.
e2e-compare:
    ./scripts/e2e-compare.sh
```

In `build-images`, remove the `sluice-uv` line. S8 adds it again.

- [ ] **Step 8: Check the laptop flow.** Run these commands in order:

```bash
cp -n .env.example .env
just up
just migrate
just build-ui build-go
set -a; . ./.env; set +a; ./bin/sluice server &
sleep 3; curl -fsS http://127.0.0.1:8080/readyz; kill %1
```

Expected: `/readyz` returns 200 JSON.

Then run `just dev` in a second terminal and open `http://localhost:5173`. Expected: the login page. Sign in as `admin@local.test` with the password from `.env`. Stop with Ctrl+C.

- [ ] **Step 9: Check the image and the compose file.**

```bash
docker build -f deploy/docker/Dockerfile --target sluice -t sluice:dev .
docker run --rm sluice:dev version
SLUICE_BOOTSTRAP_ADMIN_PASSWORD=change-me-local-1 SLUICE_MASTER_KEYS=dev:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA= \
  docker compose -f deploy/compose/compose.yml up -d --wait postgres
SLUICE_BOOTSTRAP_ADMIN_PASSWORD=change-me-local-1 SLUICE_MASTER_KEYS=dev:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA= \
  docker compose -f deploy/compose/compose.yml up -d sluice
sleep 10; curl -fsS http://127.0.0.1:8080/readyz
docker compose -f deploy/compose/compose.yml down -v
```

Expected: `version` prints three fields. `/readyz` returns 200. If port 8080 is in use by `just dev`, stop it first.

- [ ] **Step 10: Check the regression.** Run: `scripts/e2e-compare.sh`
Expected: `no regression`.

- [ ] **Step 11: Commit.**

```bash
git add .env.example .gitignore .air.toml .dockerignore deploy justfile go.mod go.sum tests/e2e/harness_test.go tests/ui/global-setup.ts
git commit -m "Dev setup: .env.example, justfile recipes, dev compose, air and the sluice image

Claude-Session: https://claude.ai/code/session_01HwCQCngd1tXrcHmnNajwk3"
```

---

### Task 5: Shared packages and the dependency rules

**Files:**
- Create: `internal/kernel/kernel.go` (content of `internal/auth/rbac.go`, package `kernel`)
- Create: `internal/platform/token/token.go`
- Create: `internal/runnerproto/protocol.go` (content of `internal/runnerapi/protocol.go`, package `runnerproto`)
- Create: `internal/snapshot/` (the manifest type and the bundle format from `internal/namespace`)
- Move: `internal/platform/instance/` to `internal/instance/`
- Modify: every importer of the moved code, `internal/execution` (consumer interface), `.golangci.yml`
- Delete: `internal/auth/rbac.go`, `internal/runnerapi/protocol.go`

**Interfaces:**
- Produces:
  - `kernel.Role`, `kernel.RoleNone`, `kernel.Viewer`, `kernel.Operator`, `kernel.Editor`, `kernel.Admin`, `kernel.AllRoles`, `kernel.ParseRole(string) (Role, bool)`, `kernel.MinRole(a, b Role) Role`, `kernel.Principal` (same fields as `auth.Principal`), `(*Principal).Can(Role) bool`, `kernel.WithPrincipal(ctx, *Principal) context.Context`, `kernel.FromContext(ctx) *Principal`.
  - `token.RandomBytes(n int) []byte`, `token.HashSecret(s string) []byte`, `token.NewSecret() (secret string, hash []byte)`.
  - `runnerproto.*`: every exported name of the old `runnerapi/protocol.go`, with the same names.
  - `snapshot.Manifest` and `snapshot.ExtractBundle(r io.Reader, dir string, maxBytes int64) error`.
  - `execution.Namespaces`: an interface in package `execution` with the five methods that the engine calls.

- [ ] **Step 1: Move the roles.** Run:

```bash
set -eo pipefail
mkdir -p internal/kernel
git mv internal/auth/rbac.go internal/kernel/kernel.go
sed -i '' 's/^package auth$/package kernel/' internal/kernel/kernel.go
sed -i '' '1i\
// Package kernel holds the types that many features share: roles and the caller.\
' internal/kernel/kernel.go
for id in Role RoleNone Viewer Operator Editor Admin AllRoles ParseRole MinRole Principal WithPrincipal FromContext; do
  for d in internal/auth internal/namespace internal/execution internal/runnerapi internal/api internal/app; do
    gofmt -w -r "$id -> kernel.$id" $d/*.go
  done
done
go run golang.org/x/tools/cmd/goimports@latest -w internal/auth internal/namespace internal/execution internal/runnerapi internal/api internal/app
go build ./...
```

`gofmt -r` also rewrites names that are fields or local variables with the same name. Read `git diff --stat`, then check each change in `internal/auth/service.go`. For example, `p.Role` must stay `p.Role`. `gofmt -r` rewrites only whole identifiers in expression positions, so selector fields are safe, but check each file. Remove a duplicate package comment in `kernel.go` if the file has one.

- [ ] **Step 2: Move the token helpers.** Create `internal/platform/token/token.go` with the bodies of `RandomBytes`, `HashSecret` and `NewSecret` from `internal/auth/tokens.go`:

```go
// Package token creates random secrets and their SHA-256 hashes.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// RandomBytes returns n random bytes. It panics when the system random source fails.
func RandomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// HashSecret returns the SHA-256 hash of a secret.
func HashSecret(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// NewSecret returns a random URL-safe secret of 256 bits and its hash.
func NewSecret() (secret string, hash []byte) {
	secret = base64.RawURLEncoding.EncodeToString(RandomBytes(32))
	return secret, HashSecret(secret)
}
```

Compare this with the current `RandomBytes` in `tokens.go`. If the current body is different, for example it returns an error, copy the current body. Delete the three functions from `tokens.go`. Replace `auth.HashSecret`, `auth.NewSecret` and `auth.RandomBytes` everywhere with `token.X`. Inside package `auth`, the calls become `token.X`. Run `go build ./...`.

- [ ] **Step 3: Move the runner protocol.**

```bash
set -eo pipefail
mkdir -p internal/runnerproto
git mv internal/runnerapi/protocol.go internal/runnerproto/protocol.go
sed -i '' 's/^package runnerapi$/package runnerproto/' internal/runnerproto/protocol.go
grep -rl 'runnerapi\.' internal --include=*.go | xargs sed -i '' -E 's/runnerapi\.(BasePath|BatchBytes|BatchInterval|Complete|Env[A-Za-z]+|Event|EventBatch|HeartbeatInterval|HeartbeatResponse|KillAfter|Limits|LogBatch|LogLine|Max[A-Za-z]+|PendingBufferBytes|RetryWindow|Spec|TruncatedMarker)\b/runnerproto.\1/g'
go run golang.org/x/tools/cmd/goimports@latest -w internal
go build ./...
```

Inside `internal/runnerapi/server.go`, the names had no prefix. Add the `runnerproto.` prefix where the compiler reports undefined names.

- [ ] **Step 4: Create `internal/snapshot`.** Move `ExtractBundle` and the helpers that only it uses from `internal/namespace/bundle.go` to `internal/snapshot/bundle.go` (package `snapshot`). Move the `Manifest` type and its methods to `internal/snapshot/manifest.go`. Find the type with `go doc ./internal/namespace Manifest`. `namespace` and `runner` then import `snapshot`. Keep `(*namespace.Service).EnsureBundle` and `writeBundle` in `namespace`. If `writeBundle` shares constants with `ExtractBundle`, for example a file mode or a size limit, export them from `snapshot` and use them in `namespace`. Run `go build ./...`.

- [ ] **Step 5: Break the `execution` to `namespace` import.** Print the five method signatures:

```bash
for m in EnsureBundle Get GetFlow Manifest ReadBlob; do go doc ./internal/namespace Service.$m; done
```

In `internal/execution`, add `namespaces.go`:

```go
package execution

// Namespaces is what the engine needs from the namespace feature.
// internal/app passes *namespace.Service.
type Namespaces interface {
	// Copy the five signatures from the go doc output here, unchanged.
}
```

Replace the five commented lines with the real signatures. When a signature uses a type from package `namespace`, handle it in one of three ways:
- If the type is snapshot data, move it to `snapshot`.
- If it is a `dbq` row type, leave it: `dbq` is shared until Task 14.
- In any other case, move the type to `snapshot` only if `runner` or `execution` also reads its fields. Otherwise, declare the smallest struct that `execution` needs in `execution`. Then change the method in `namespace` to return that struct, or add a small adapter type in `internal/app`.

Change `Engine.Namespaces` to type `Namespaces`. Check that no file in `internal/execution` imports `internal/namespace`:

```bash
go list -f '{{join .Imports "\n"}}' ./internal/execution | grep -c 'internal/namespace$' || true
```

Expected: `0`.

- [ ] **Step 6: Move the instance registry.** Run:

```bash
set -eo pipefail
git mv internal/platform/instance internal/instance
grep -rl 'internal/platform/instance' --include=*.go . | xargs sed -i '' 's#internal/platform/instance#internal/instance#g'
go build ./...
```

- [ ] **Step 7: Add the depguard rules.** In `.golangci.yml`, add `depguard` to `linters.enable`. Add under `linters.settings`:

```yaml
    depguard:
      rules:
        kernel:
          files: ["**/internal/kernel/**"]
          deny:
            - pkg: github.com/alternayte/sluice/internal
              desc: kernel imports no internal package
        platform:
          files: ["**/internal/platform/**"]
          deny:
            - pkg: github.com/alternayte/sluice/internal/auth
              desc: platform imports only kernel and platform
            - pkg: github.com/alternayte/sluice/internal/namespace
              desc: platform imports only kernel and platform
            - pkg: github.com/alternayte/sluice/internal/execution
              desc: platform imports only kernel and platform
            - pkg: github.com/alternayte/sluice/internal/instance
              desc: platform imports only kernel and platform
            - pkg: github.com/alternayte/sluice/internal/app
              desc: platform imports only kernel and platform
        only-app-imports-auth:
          files: ["**/internal/**", "!**/internal/auth/**", "!**/internal/app/**", "!**/internal/api/**"]
          deny:
            - pkg: github.com/alternayte/sluice/internal/auth
              desc: a feature does not import another feature (SDD §4.4)
        only-app-imports-namespace:
          files: ["**/internal/**", "!**/internal/namespace/**", "!**/internal/app/**", "!**/internal/api/**"]
          deny:
            - pkg: github.com/alternayte/sluice/internal/namespace
              desc: a feature does not import another feature (SDD §4.4)
        only-app-imports-execution:
          files: ["**/internal/**", "!**/internal/execution/**", "!**/internal/app/**", "!**/internal/api/**"]
          deny:
            - pkg: github.com/alternayte/sluice/internal/execution
              desc: a feature does not import another feature (SDD §4.4)
        only-app-imports-instance:
          files: ["**/internal/**", "!**/internal/instance/**", "!**/internal/app/**", "!**/internal/api/**"]
          deny:
            - pkg: github.com/alternayte/sluice/internal/instance
              desc: a feature does not import another feature (SDD §4.4)
        only-execution-imports-executor:
          files: ["**/internal/**", "!**/internal/executor/**", "!**/internal/execution/**", "!**/internal/app/**"]
          deny:
            - pkg: github.com/alternayte/sluice/internal/executor
              desc: executor is the adapter set of execution
        only-app-imports-runner:
          files: ["**/internal/**", "!**/internal/runner/**", "!**/internal/app/**"]
          deny:
            - pkg: github.com/alternayte/sluice/internal/runner
              desc: the runner client is wired only by app
```

`internal/api` is excluded until Task 13 deletes it. A `pkg` entry is a prefix match. Therefore the `internal/runner` rule also matches `internal/runnerapi` and `internal/runnerproto`. Change that rule's `pkg` to `github.com/alternayte/sluice/internal/runner$`. depguard v2 accepts a `$` suffix for an exact match. If your golangci-lint version does not accept it, list the importers of `internal/runner` with `go list` and write the rule for those files only.

- [ ] **Step 8: Run lint and fix the violations.** Run: `golangci-lint run ./...`
Expected: no depguard error. A depguard error names a feature-to-feature import that Steps 1 to 5 missed. Fix it with the same pattern as Step 5.

- [ ] **Step 9: Run the tests.** Run: `go test -race ./... && go test -tags integration ./internal/... && scripts/e2e-compare.sh`
Expected: PASS, and `no regression`.

- [ ] **Step 10: Commit.**

```bash
git add -A internal .golangci.yml
git commit -m "Add kernel, token, snapshot and runnerproto; remove feature-to-feature imports; add depguard

Claude-Session: https://claude.ai/code/session_01HwCQCngd1tXrcHmnNajwk3"
```

---

### Task 6: huma foundation in `platform/httpx`

**Files:**
- Create: `internal/platform/httpx/api.go`
- Create: `internal/platform/httpx/api_test.go`
- Modify: `internal/platform/httpx/httpx.go` (`Error` methods, `WriteError`)
- Modify: `internal/auth/service.go:41` (use `httpx.ErrPasswordChange`)

**Interfaces:**
- Consumes: `kernel.Role`, `kernel.FromContext`, `kernel.WithPrincipal`, `kernel.Principal`.
- Produces:
  - `type Access struct{ Public bool; Min kernel.Role; Self bool; Other string }`
  - `httpx.Public`, `httpx.Authenticated`, `httpx.RunToken`, `httpx.MinRole(kernel.Role) Access`
  - `httpx.Op(id, method, path string, acc Access) huma.Operation`
  - `httpx.AccessOf(*huma.Operation) (Access, bool)`
  - `httpx.Check(ctx, Access) error`
  - `httpx.ErrPasswordChange`
  - `httpx.NewAPI(r *chi.Mux) huma.API`
  - `httpx.CheckAccess(huma.API) error`
  - `httpx.Raw(api huma.API, r chi.Router, op huma.Operation, h http.HandlerFunc)`
  - `httpx.PathParams(names ...string) []*huma.Param`
  - `httpx.RawResponse(status int, contentType, description string) map[string]*huma.Response`
  - `httpx.NotFoundJSON() http.Handler`
  - `*httpx.Error` implements `huma.StatusError`, `GetHeaders() http.Header` and `ContentType(string) string`, and its JSON form is the envelope.

- [ ] **Step 1: Add the dependencies.** Run: `go get github.com/go-chi/chi/v5@latest github.com/danielgtaylor/huma/v2@latest`

- [ ] **Step 2: Write the failing test.**

```go
package httpx_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

type createIn struct {
	Body struct {
		Email    string `json:"email" format:"email"`
		Role     string `json:"role" enum:"viewer,operator,editor,admin"`
		Password string `json:"password" minLength:"10"`
	}
}

type okOut struct {
	Body struct {
		OK bool `json:"ok"`
	}
}

func testHandler(t *testing.T) http.Handler {
	t.Helper()
	r := chi.NewMux()
	api := httpx.NewAPI(r)
	huma.Register(api, httpx.Op("create", http.MethodPost, "/api/v1/things", httpx.MinRole(kernel.Admin)),
		func(context.Context, *createIn) (*okOut, error) {
			out := &okOut{}
			out.Body.OK = true
			return out, nil
		})
	huma.Register(api, httpx.Op("limited", http.MethodGet, "/api/v1/limited", httpx.Public),
		func(context.Context, *struct{}) (*okOut, error) {
			return nil, &httpx.Error{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "slow down", RetryAfter: 7}
		})
	huma.Register(api, httpx.Op("boom", http.MethodGet, "/api/v1/boom", httpx.Public),
		func(context.Context, *struct{}) (*okOut, error) { return nil, errors.New("secret database detail") })
	httpx.Raw(api, r, httpx.Op("stream", http.MethodGet, "/api/v1/stream/{id}", httpx.MinRole(kernel.Viewer)),
		func(w http.ResponseWriter, req *http.Request) { _, _ = io.WriteString(w, "raw:"+chi.URLParam(req, "id")) })
	r.Handle("/api/*", httpx.NotFoundJSON())
	if err := httpx.CheckAccess(api); err != nil {
		t.Fatal(err)
	}
	return r
}

type result struct {
	status int
	header http.Header
	body   string
	env    struct {
		Error struct {
			Code    string `json:"code"`
			Details []struct {
				Field string `json:"field"`
			} `json:"details"`
		} `json:"error"`
	}
}

func do(t *testing.T, h http.Handler, method, path, body string, role kernel.Role) result {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if role != kernel.RoleNone {
		req = req.WithContext(kernel.WithPrincipal(req.Context(), &kernel.Principal{Role: role}))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := result{status: rec.Code, header: rec.Header(), body: rec.Body.String()}
	_ = json.Unmarshal(rec.Body.Bytes(), &res.env)
	return res
}

func TestAPIContract(t *testing.T) {
	h := testHandler(t)
	bad := `{"email":"x","role":"king","password":"short"}`

	t.Run("validation lists each field", func(t *testing.T) {
		r := do(t, h, http.MethodPost, "/api/v1/things", bad, kernel.Admin)
		fields := map[string]bool{}
		for _, d := range r.env.Error.Details {
			fields[d.Field] = true
		}
		if r.status != http.StatusUnprocessableEntity || r.env.Error.Code != "validation_failed" ||
			!fields["email"] || !fields["role"] || !fields["password"] {
			t.Fatalf("%d %s", r.status, r.body)
		}
	})
	t.Run("no caller gets 401 before validation", func(t *testing.T) {
		r := do(t, h, http.MethodPost, "/api/v1/things", bad, kernel.RoleNone)
		if r.status != http.StatusUnauthorized || r.env.Error.Code != "unauthorized" {
			t.Fatalf("%d %s", r.status, r.body)
		}
	})
	t.Run("low role gets 403", func(t *testing.T) {
		r := do(t, h, http.MethodPost, "/api/v1/things", bad, kernel.Viewer)
		if r.status != http.StatusForbidden || r.env.Error.Code != "forbidden" {
			t.Fatalf("%d %s", r.status, r.body)
		}
	})
	t.Run("success body has no extra fields", func(t *testing.T) {
		r := do(t, h, http.MethodPost, "/api/v1/things", `{"email":"a@b.co","role":"admin","password":"long-enough-1"}`, kernel.Admin)
		if r.status != http.StatusOK || strings.TrimSpace(r.body) != `{"ok":true}` {
			t.Fatalf("%d %s", r.status, r.body)
		}
	})
	t.Run("error keeps code and Retry-After", func(t *testing.T) {
		r := do(t, h, http.MethodGet, "/api/v1/limited", "", kernel.RoleNone)
		if r.status != http.StatusTooManyRequests || r.env.Error.Code != "rate_limited" || r.header.Get("Retry-After") != "7" ||
			r.header.Get("Content-Type") != "application/json" {
			t.Fatalf("%d %v %s", r.status, r.header, r.body)
		}
	})
	t.Run("unknown error hides detail", func(t *testing.T) {
		r := do(t, h, http.MethodGet, "/api/v1/boom", "", kernel.RoleNone)
		if r.status != http.StatusInternalServerError || r.env.Error.Code != "internal" || strings.Contains(r.body, "secret") {
			t.Fatalf("%d %s", r.status, r.body)
		}
	})
	t.Run("raw route checks access", func(t *testing.T) {
		if r := do(t, h, http.MethodGet, "/api/v1/stream/42", "", kernel.RoleNone); r.status != http.StatusUnauthorized {
			t.Fatalf("anonymous: %d", r.status)
		}
		if r := do(t, h, http.MethodGet, "/api/v1/stream/42", "", kernel.Viewer); r.status != http.StatusOK || r.body != "raw:42" {
			t.Fatalf("viewer: %d %s", r.status, r.body)
		}
	})
	t.Run("unknown API route is JSON 404", func(t *testing.T) {
		r := do(t, h, http.MethodGet, "/api/v1/nothing", "", kernel.Admin)
		if r.status != http.StatusNotFound || r.env.Error.Code != "not_found" {
			t.Fatalf("%d %s", r.status, r.body)
		}
	})
}

func TestCheckAccessFindsOperationWithoutAccess(t *testing.T) {
	api := httpx.NewAPI(chi.NewMux())
	huma.Register(api, huma.Operation{OperationID: "open", Method: http.MethodGet, Path: "/api/v1/open"},
		func(context.Context, *struct{}) (*okOut, error) { return &okOut{}, nil })
	if err := httpx.CheckAccess(api); err == nil || !strings.Contains(err.Error(), "/api/v1/open") {
		t.Fatalf("want error that names /api/v1/open, got %v", err)
	}
}
```

- [ ] **Step 3: Run the test to see it fail.** Run: `go test ./internal/platform/httpx/`
Expected: compile errors, for example `undefined: httpx.NewAPI`.

- [ ] **Step 4: Change `Error` in `httpx.go`.** Add these methods, and replace the `envelope` type and `WriteError`:

```go
// MarshalJSON writes the envelope {"error":{"code","message","details"}} (REQ-API-002).
func (e *Error) MarshalJSON() ([]byte, error) {
	type body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details any    `json:"details,omitempty"`
	}
	return json.Marshal(struct {
		Error body `json:"error"`
	}{body{Code: e.Code, Message: e.Message, Details: e.Details}})
}

// GetStatus returns the HTTP status. huma uses it.
func (e *Error) GetStatus() int { return e.Status }

// GetHeaders returns Retry-After when it is set. huma uses it.
func (e *Error) GetHeaders() http.Header {
	h := http.Header{}
	if e.RetryAfter > 0 {
		h.Set("Retry-After", strconv.Itoa(e.RetryAfter))
	}
	return h
}

// ContentType keeps application/json for errors. huma uses it.
func (e *Error) ContentType(string) string { return "application/json" }

// WriteError writes err as the error envelope. Unknown errors become 500 internal.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var e *Error
	if !errors.As(err, &e) {
		logging.From(r.Context()).Error("request failed", "err", err)
		e = errInternal
	}
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(e.RetryAfter))
	}
	WriteJSON(w, e.Status, e)
}

var errInternal = &Error{Status: http.StatusInternalServerError, Code: "internal", Message: "internal error"}
```

Delete the `envelope` type. Add `strconv` to the imports. Search for other users with `grep -rn "envelope{" internal`. Expected: no hit.

- [ ] **Step 5: Write `api.go`.**

```go
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/alternayte/sluice/internal/kernel"
)

// Access is the permission of one operation (Appendix B, SI-03).
type Access struct {
	// Public operations need no authentication.
	Public bool
	// Min is the lowest role that can call the operation.
	Min kernel.Role
	// Self operations are allowed while the user must change the password.
	Self bool
	// Other names the credential that the operation checks itself, for example a run token.
	Other string
}

// Access values that many operations use.
var (
	Public        = Access{Public: true}
	Authenticated = Access{Min: kernel.Viewer, Self: true}
	RunToken      = Access{Other: "run token of the task run (SI-04)"}
)

// MinRole returns the access for role r and higher roles.
func MinRole(r kernel.Role) Access { return Access{Min: r} }

const accessKey = "sluice:access"

// Op returns an operation with its access. Every operation uses Op (D-23).
func Op(id, method, path string, acc Access) huma.Operation {
	return huma.Operation{OperationID: id, Method: method, Path: path, Metadata: map[string]any{accessKey: acc}}
}

// AccessOf returns the access of an operation.
func AccessOf(op *huma.Operation) (Access, bool) {
	if op == nil || op.Metadata == nil {
		return Access{}, false
	}
	a, ok := op.Metadata[accessKey].(Access)
	return a, ok
}

// ErrPasswordChange blocks all operations except self operations until the user sets a new password.
var ErrPasswordChange = Errorf(http.StatusForbidden, "password_change_required", "change your password first")

// Check returns nil when the caller in ctx can use an operation with access acc.
func Check(ctx context.Context, acc Access) error {
	if acc.Public || acc.Other != "" {
		return nil
	}
	p := kernel.FromContext(ctx)
	if p == nil {
		return ErrUnauthorized
	}
	if p.MustChangePassword && !acc.Self {
		return ErrPasswordChange
	}
	if !p.Can(acc.Min) {
		return ErrForbidden
	}
	return nil
}

func init() { huma.NewError = newError }

// NewAPI creates the huma API on r. It serves no spec and no docs: `sluice openapi` prints the spec.
// The access check runs before huma reads the request, so a caller without permission
// never sees validation details (SI-03).
func NewAPI(r *chi.Mux) huma.API {
	cfg := huma.DefaultConfig("Sluice API", "1")
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.SchemasPath = ""
	// No $schema field in bodies and no Link header: the JSON contract stays as it is.
	cfg.CreateHooks = nil
	api := humachi.New(r, cfg)
	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		acc, ok := AccessOf(ctx.Operation())
		if !ok {
			writeHuma(ctx, ErrForbidden)
			return
		}
		if err := Check(ctx.Context(), acc); err != nil {
			writeHuma(ctx, err)
			return
		}
		next(ctx)
	})
	return api
}

func writeHuma(ctx huma.Context, err error) {
	var e *Error
	if !errors.As(err, &e) {
		slog.ErrorContext(ctx.Context(), "request failed", "err", err)
		e = errInternal
	}
	if e.RetryAfter > 0 {
		ctx.SetHeader("Retry-After", strconv.Itoa(e.RetryAfter))
	}
	ctx.SetHeader("Content-Type", "application/json")
	ctx.SetStatus(e.Status)
	_ = json.NewEncoder(ctx.BodyWriter()).Encode(e)
}

// newError replaces huma.NewError. It keeps the Sluice envelope and codes (REQ-API-002).
func newError(status int, msg string, errs ...error) huma.StatusError {
	switch {
	case status >= http.StatusInternalServerError:
		slog.Error("request failed", "status", status, "err", msg)
		return errInternal
	case status == http.StatusUnprocessableEntity || (status == http.StatusBadRequest && len(errs) > 0):
		fields := make([]FieldError, 0, len(errs))
		for _, err := range errs {
			var d *huma.ErrorDetail
			if errors.As(err, &d) {
				fields = append(fields, FieldError{Field: fieldName(d.Location), Message: d.Message})
				continue
			}
			fields = append(fields, FieldError{Message: err.Error()})
		}
		return Validation(fields...)
	}
	return &Error{Status: status, Code: codeFor(status), Message: msg}
}

// fieldName turns a huma location such as body.email, query.limit or path.userId into the field name.
func fieldName(loc string) string {
	if _, rest, ok := strings.Cut(loc, "."); ok {
		return rest
	}
	return loc
}

func codeFor(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusRequestEntityTooLarge:
		return "too_large"
	case http.StatusUnsupportedMediaType:
		return "unsupported_media_type"
	}
	return strings.ReplaceAll(strings.ToLower(http.StatusText(status)), " ", "_")
}

// CheckAccess returns an error that names each operation without access (default deny, D-23).
func CheckAccess(api huma.API) error {
	var missing []string
	for path, item := range api.OpenAPI().Paths {
		for _, op := range []*huma.Operation{item.Get, item.Put, item.Post, item.Delete, item.Patch, item.Head, item.Options} {
			if op == nil {
				continue
			}
			if _, ok := AccessOf(op); !ok {
				missing = append(missing, op.Method+" "+path)
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("operations without access: %s", strings.Join(missing, ", "))
	}
	return nil
}

// Raw adds an operation to the spec and serves it with a plain handler behind the access check.
// Use it only for bodies that must not be buffered: file and artifact transfer, bundles and SSE (DI-23).
func Raw(api huma.API, r chi.Router, op huma.Operation, h http.HandlerFunc) {
	acc, ok := AccessOf(&op)
	if !ok {
		panic("httpx.Raw: operation " + op.OperationID + " has no access")
	}
	if op.Responses == nil {
		op.Responses = RawResponse(http.StatusOK, "application/octet-stream", "Content.")
	}
	api.OpenAPI().AddOperation(&op)
	r.Method(op.Method, op.Path, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if err := Check(req.Context(), acc); err != nil {
			WriteError(w, req, err)
			return
		}
		h(w, req)
	}))
}

// PathParams describes string path parameters for a Raw operation.
func PathParams(names ...string) []*huma.Param {
	out := make([]*huma.Param, 0, len(names))
	for _, n := range names {
		out = append(out, &huma.Param{Name: n, In: "path", Required: true, Schema: &huma.Schema{Type: huma.TypeString}})
	}
	return out
}

// RawResponse describes the success response of a Raw operation.
func RawResponse(status int, contentType, description string) map[string]*huma.Response {
	resp := &huma.Response{Description: description}
	if contentType != "" {
		resp.Content = map[string]*huma.MediaType{contentType: {}}
	}
	return map[string]*huma.Response{strconv.Itoa(status): resp}
}

// NotFoundJSON answers unknown API routes with a JSON 404 (REQ-API-002).
func NotFoundJSON() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, Errorf(http.StatusNotFound, "not_found", "unknown API route %s %s", r.Method, r.URL.Path))
	})
}
```

- [ ] **Step 6: Use the moved error in auth.** In `internal/auth/service.go`, change the `ErrPasswordChange` line to `ErrPasswordChange = httpx.ErrPasswordChange`.

- [ ] **Step 7: Run the test.** Run: `go test ./internal/platform/httpx/`
Expected: PASS. Two cases can fail if the pinned huma version differs from the assumptions of this plan:
- The `$schema` field is present. Then look for the schema link hook in `huma.DefaultConfig` (`go doc github.com/danielgtaylor/huma/v2 DefaultConfig`) and remove only that hook.
- `Retry-After` or `Content-Type` is missing on the `limited` case. Then huma does not call `GetHeaders` or `ContentType`. Check with `go doc github.com/danielgtaylor/huma/v2 HeadersError` and `go doc github.com/danielgtaylor/huma/v2 ContentTypeFilter`, and use the method names that the docs give.

Do not change the test assertions. They are the HTTP contract.

- [ ] **Step 8: Commit.**

```bash
go mod tidy
git add go.mod go.sum internal/platform/httpx internal/auth/service.go
git commit -m "httpx: huma API with access check, Sluice error envelope and Raw routes

Claude-Session: https://claude.ai/code/session_01HwCQCngd1tXrcHmnNajwk3"
```

---

### Task 7: chi router, route registration and `sluice openapi`

**Files:**
- Create: `internal/app/routes.go`
- Create: `internal/app/cli_openapi.go`
- Modify: `internal/app/server.go:170-229` (replace `RecordingMux`, `Routes` and `Handler`)
- Modify: `internal/app/cli.go:31-42` (add the command)
- Delete: `internal/app/routes_integration_test.go` (Task 13 writes the new SCN-AUTH-006 test)

**Interfaces:**
- Consumes: `httpx.NewAPI`, `httpx.CheckAccess`, `httpx.NotFoundJSON`.
- Produces:
  - `type services struct{...}` in `internal/app`
  - `registerRoutes(api huma.API, r chi.Router, s services)`
  - `(*Server).services() services`
  - `sluice openapi`, which prints OpenAPI 3.1 YAML to stdout.

- [ ] **Step 1: Create `routes.go`.**

```go
package app

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/auth"
	"github.com/alternayte/sluice/internal/execution"
	"github.com/alternayte/sluice/internal/instance"
	"github.com/alternayte/sluice/internal/namespace"
	"github.com/alternayte/sluice/internal/platform/clock"
)

// services holds what the routes call. `sluice openapi` passes the zero value:
// route registration must not use a service.
type services struct {
	Auth             *auth.Service
	Audit            *audit.Writer
	Instances        *instance.Registry
	Clock            clock.Clock
	Namespaces       *namespace.Service
	Engine           *execution.Engine
	MaxArtifactBytes int64
}

// registerRoutes registers every API operation on api, and the streamed routes on r.
func registerRoutes(api huma.API, r chi.Router, s services) {
	_, _ = r, s
}

func (s *Server) services() services {
	return services{Auth: s.Auth, Audit: s.Audit, Instances: s.Registry, Clock: s.Clock, Namespaces: s.Namespaces,
		Engine: s.Engine, MaxArtifactBytes: int64(s.Cfg.MaxArtifactBytes)}
}
```

Tasks 9 to 12 replace the `_, _ = r, s` line with the calls to the feature `Routes` functions. Remove unused imports until then. The compiler reports each one.

- [ ] **Step 2: Replace the router in `server.go`.** Delete `RecordingMux`, its methods and `Routes()`. Replace `Handler()` with:

```go
// Handler builds the root HTTP handler.
func (s *Server) Handler() (http.Handler, error) {
	r := chi.NewMux()
	r.Use(httpx.SecurityHeaders, httpx.WithRequestID, s.withLogger, s.metrics, s.Auth.Middleware,
		runnerapi.ContentTypeMiddleware, runnerapi.TokenMiddleware(s.Engine))
	r.Get("/healthz", health.Healthz)
	r.Get("/readyz", s.Health.Readyz)
	r.Method(http.MethodGet, "/metrics", s.Metrics.Handler())
	api := httpx.NewAPI(r)
	registerRoutes(api, r, s.services())
	if err := httpx.CheckAccess(api); err != nil {
		return nil, err
	}
	legacy, err := s.legacyAPI()
	if err != nil {
		return nil, err
	}
	r.Handle("/api/*", legacy)
	r.Handle("/*", spaHandler())
	return r, nil
}

// legacyAPI serves the operations of the old generated server until each feature moves to huma.
// chi matches the huma routes first, because a static path segment wins over the /api/* wildcard.
func (s *Server) legacyAPI() (http.Handler, error) {
	mux := http.NewServeMux()
	apiServer := &api.Server{
		System:    api.System{Instances: s.Registry, Clock: s.Clock},
		Handlers:  auth.Handlers{Svc: s.Auth},
		API:       namespace.API{Svc: s.Namespaces},
		ExecAPI:   execution.ExecAPI{E: s.Engine},
		RunnerAPI: runnerapi.RunnerAPI{B: s.Engine, MaxArtifactBytes: int64(s.Cfg.MaxArtifactBytes)},
	}
	if err := api.Mount(mux, apiServer, nil); err != nil {
		return nil, err
	}
	authorize, err := api.AuthorizeMiddleware(auth.Authorize)
	if err != nil {
		return nil, err
	}
	return authorize(mux), nil
}

func (s *Server) withLogger(next http.Handler) http.Handler { return withLogger(s.Log, next) }

// metrics records the HTTP duration with the chi route pattern as the route label.
func (s *Server) metrics(next http.Handler) http.Handler {
	return s.Metrics.Middleware(func(r *http.Request) string {
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			return rc.RoutePattern()
		}
		return "unmatched"
	}, next)
}
```

Check the types of `httpx.SecurityHeaders` and `httpx.WithRequestID`. Each must be `func(http.Handler) http.Handler`. The JSON 404 for unknown `/api` routes comes from the legacy mux now, and from `httpx.NotFoundJSON` after Task 13.

- [ ] **Step 3: Add the `openapi` command.** Create `cli_openapi.go`:

```go
package app

import (
	"context"
	"fmt"
	"io"

	"github.com/go-chi/chi/v5"

	"github.com/alternayte/sluice/internal/platform/httpx"
)

// runOpenAPI prints the OpenAPI document. It needs no database and no server (REQ-API-001).
func runOpenAPI(_ context.Context, _ []string, stdout, stderr io.Writer) int {
	r := chi.NewMux()
	api := httpx.NewAPI(r)
	registerRoutes(api, r, services{})
	if err := httpx.CheckAccess(api); err != nil {
		fmt.Fprintln(stderr, err)
		return exitFail
	}
	b, err := api.OpenAPI().YAML()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFail
	}
	_, _ = stdout.Write(b)
	return exitOK
}
```

In `commands()`, add before `version`: `{"openapi", "Print the OpenAPI document of the API.", runOpenAPI},`.

- [ ] **Step 4: Delete the old inventory test.** Run: `git rm internal/app/routes_integration_test.go`. SCN-AUTH-006 has no test until Task 13. `just trace` does not run until Task 19.

- [ ] **Step 5: Build and check the command.** Run: `go build ./... && go run ./cmd/sluice openapi | head -3`
Expected: the first line is `openapi: 3.1.0`, or `3.1.x`.

- [ ] **Step 6: Check the regression.** Run: `golangci-lint run ./... && scripts/e2e-compare.sh`
Expected: no lint error, and `no regression`. If unknown `/api` routes fail SCN-API-002, look at the old `api.Mount`. It registers `/api/` on the legacy mux, so the legacy handler returns the JSON 404.

- [ ] **Step 7: Commit.**

```bash
go mod tidy
git add -A internal/app go.mod go.sum
git commit -m "Server: chi router with huma API and the old handler as fallback; add sluice openapi

Claude-Session: https://claude.ai/code/session_01HwCQCngd1tXrcHmnNajwk3"
```

---

### Rules for Tasks 9 to 12 (read before each of them)

1. **Contract copy.** For each operation, open its entry and schemas in the current `api/openapi.yaml`. Make Go types with the same JSON names. Name each Go type like its schema (`Me`, `User`, `Token`, `Namespace`, `Execution`). Then the generated TS names stay close to the old names.
2. **Tags.**

   | Spec keyword | Go struct tag |
   |---|---|
   | field not in `required` | add `,omitempty` to the `json` tag |
   | `nullable: true` | pointer type plus `,omitempty` when the field is not required |
   | `minLength`, `maxLength`, `minimum`, `maximum`, `pattern`, `format` | the huma tag of the same name, for example `minLength:"10"` |
   | `enum: [a, b]` | `enum:"a,b"` |
   | `default` | `default:"x"` |
   | path parameter `{userId}` | `UserID uuid.UUID \`path:"userId"\`` |
   | query parameter | `Limit int \`query:"limit" minimum:"1" maximum:"200"\`` |
   | header `Last-Event-ID` | `LastEventID string \`header:"Last-Event-ID"\`` |

3. **Status.** For a success status other than 200, set `op.DefaultStatus`. For 204, the output type is `*struct{}` and the handler returns `nil, nil`.
4. **Access.** Take the access of each operation from `internal/auth/permissions.go`:
   - `public` becomes `httpx.Public`.
   - `authenticated` becomes `httpx.Authenticated`.
   - `viewer`, `operator`, `editor` and `admin` become `httpx.MinRole(kernel.X)`.
   - `runToken` becomes `httpx.RunToken`.
5. **Handlers.** Keep each service call exactly as in the old handler. Only the input and output types change. If a field type in this plan differs from the old handler, use the old type. The compiler reports each difference.
6. **Streamed operations** use `httpx.Raw`. Move the body of the old `Visit…Response` or streamed handler into the `http.HandlerFunc`. Read path values with `chi.URLParam(r, "name")`. Parse UUIDs with `uuid.Parse`, and on error return `httpx.WriteError(w, r, httpx.ErrNotFound)`.
7. **Pagination.** Cursor and limit are plain values: `Cursor string \`query:"cursor"\`` and `Limit int \`query:"limit" minimum:"1" maximum:"200"\``. Use `page.Limit(in.Limit)` and `page.DecodeOpt(in.Cursor)` (added in Task 8).
8. **Proof.** After each task, run `scripts/e2e-compare.sh`. Keep the old handler file until Task 13. It is not reachable after its operations move, because chi matches the huma route first.

---

### Task 8: Pagination helper

**Files:**
- Modify: `internal/platform/page/page.go` (the file that holds `DecodePtr`)

**Interfaces:**
- Produces: `page.DecodeOpt(c string) (*time.Time, *uuid.UUID, error)`. An empty cursor gives `nil, nil, nil`.

- [ ] **Step 1: Add the function.**

```go
// DecodeOpt decodes an optional cursor. An empty cursor gives nil values.
func DecodeOpt(c string) (*time.Time, *uuid.UUID, error) {
	if c == "" {
		return nil, nil, nil
	}
	t, id, err := Decode(c)
	if err != nil {
		return nil, nil, err
	}
	return &t, &id, nil
}
```

- [ ] **Step 2: Build and commit.** A test is not necessary: `DecodeOpt` only calls `Decode`, and `tests/e2e` SCN-API-003 covers the pages.

```bash
go build ./... && git add internal/platform/page && git commit -m "page: add DecodeOpt

Claude-Session: https://claude.ai/code/session_01HwCQCngd1tXrcHmnNajwk3"
```

---

### Task 9: Auth, users and tokens on huma

**Files:**
- Create: `internal/auth/routes.go`
- Modify: `internal/app/routes.go` (call `auth.Routes`)

**Interfaces:**
- Consumes: `httpx.Op`, `httpx.Public`, `httpx.Authenticated`, `httpx.MinRole`, `page.DecodeOpt`, `page.Limit`, `page.Encode`, the `auth.Service` methods that `handlers.go` calls.
- Produces: `auth.Routes(api huma.API, s *Service)`. The operations are `login`, `logout`, `getMe`, `updateMe`, `changePassword`, `revokeOtherSessions`, `listUsers`, `createUser`, `updateUser`, `resetUserPassword`, `listTokens`, `createToken` and `revokeToken`.

- [ ] **Step 1: Write `routes.go`.**

```go
package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/dbq"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/platform/page"
)

// Me is the current user.
type Me struct {
	ID                 uuid.UUID `json:"id"`
	Email              string    `json:"email"`
	Name               string    `json:"name"`
	Role               string    `json:"role" enum:"viewer,operator,editor,admin"`
	MustChangePassword bool      `json:"must_change_password"`
	AuthType           string    `json:"auth_type" enum:"session,token"`
}

// User is a user account.
type User struct {
	ID                 uuid.UUID  `json:"id"`
	Email              string     `json:"email"`
	Name               string     `json:"name"`
	Role               string     `json:"role" enum:"viewer,operator,editor,admin"`
	MustChangePassword bool       `json:"must_change_password"`
	Disabled           bool       `json:"disabled"`
	CreatedAt          time.Time  `json:"created_at"`
	LastLoginAt        *time.Time `json:"last_login_at,omitempty"`
}

// Token is an API token without its secret.
type Token struct {
	ID         uuid.UUID  `json:"id"`
	UserID     uuid.UUID  `json:"user_id"`
	UserEmail  string     `json:"user_email"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Role       string     `json:"role" enum:"viewer,operator,editor,admin"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type meOut struct{ Body Me }

type meCookieOut struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      Me
}

type cookieOut struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

type userOut struct{ Body User }

type listIn struct {
	Cursor string `query:"cursor"`
	Limit  int    `query:"limit" minimum:"1" maximum:"200"`
}

type userIDIn struct {
	UserID uuid.UUID `path:"userId"`
}

func toMe(p *kernel.Principal) Me {
	kind := "session"
	if p.Kind == "token" {
		kind = "token"
	}
	return Me{ID: p.UserID, Email: p.Email, Name: p.Name, Role: p.Role.String(), MustChangePassword: p.MustChangePassword, AuthType: kind}
}

func toUser(u dbq.User) User {
	return User{ID: u.ID, Email: u.Email, Name: u.Name, Role: u.Role, MustChangePassword: u.MustChangePassword,
		Disabled: u.DisabledAt != nil, CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt}
}

func toToken(t dbq.ListTokensRow) Token {
	return Token{ID: t.ID, UserID: t.UserID, UserEmail: t.UserEmail, Name: t.Name, Prefix: t.Prefix, Role: t.Role,
		CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt, RevokedAt: t.RevokedAt}
}

func mustPrincipal(ctx context.Context) (*kernel.Principal, error) {
	p := kernel.FromContext(ctx)
	if p == nil {
		return nil, httpx.ErrUnauthorized
	}
	return p, nil
}

func role(s string) kernel.Role {
	r, _ := kernel.ParseRole(s)
	return r
}

func withStatus(op huma.Operation, status int) huma.Operation {
	op.DefaultStatus = status
	return op
}

// Routes registers the session, profile, user and token operations (REQ-AUTH-001 to REQ-AUTH-005).
func Routes(api huma.API, s *Service) {
	registerSession(api, s)
	registerUsers(api, s)
	registerTokens(api, s)
}

func registerSession(api huma.API, s *Service) {
	huma.Register(api, httpx.Op("login", http.MethodPost, "/api/v1/auth/login", httpx.Public),
		func(ctx context.Context, in *struct {
			Body struct {
				Email    string `json:"email" minLength:"3" maxLength:"320"`
				Password string `json:"password" minLength:"1" maxLength:"1024"`
			}
		}) (*meCookieOut, error) {
			m := MetaFrom(ctx)
			res, err := s.Login(ctx, in.Body.Email, in.Body.Password, m.IP, m.UserAgent)
			if err != nil {
				return nil, err
			}
			return &meCookieOut{SetCookie: *s.SessionCookie(res.SessionID), Body: toMe(res.Principal)}, nil
		})

	huma.Register(api, withStatus(httpx.Op("logout", http.MethodPost, "/api/v1/auth/logout", httpx.Authenticated), http.StatusNoContent),
		func(ctx context.Context, _ *struct{}) (*cookieOut, error) {
			p, err := mustPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			if err := s.Logout(ctx, p); err != nil {
				return nil, err
			}
			return &cookieOut{SetCookie: *s.ClearCookie()}, nil
		})

	huma.Register(api, httpx.Op("getMe", http.MethodGet, "/api/v1/auth/me", httpx.Authenticated),
		func(ctx context.Context, _ *struct{}) (*meOut, error) {
			p, err := mustPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			return &meOut{Body: toMe(p)}, nil
		})

	huma.Register(api, httpx.Op("updateMe", http.MethodPatch, "/api/v1/auth/me", httpx.Authenticated),
		func(ctx context.Context, in *struct {
			Body struct {
				Name string `json:"name" maxLength:"200"`
			}
		}) (*meOut, error) {
			p, err := mustPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			u, err := s.UpdateMe(ctx, p, in.Body.Name)
			if err != nil {
				return nil, err
			}
			p2 := *p
			p2.Name = u.Name
			return &meOut{Body: toMe(&p2)}, nil
		})

	huma.Register(api, withStatus(httpx.Op("changePassword", http.MethodPost, "/api/v1/auth/password", httpx.Authenticated), http.StatusNoContent),
		func(ctx context.Context, in *struct {
			Body struct {
				CurrentPassword string `json:"current_password" minLength:"1" maxLength:"1024"`
				NewPassword     string `json:"new_password" minLength:"10" maxLength:"1024"`
			}
		}) (*struct{}, error) {
			p, err := mustPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			return nil, s.ChangePassword(ctx, p, in.Body.CurrentPassword, in.Body.NewPassword)
		})

	huma.Register(api, httpx.Op("revokeOtherSessions", http.MethodPost, "/api/v1/auth/sessions/revoke-others", httpx.Authenticated),
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Count int `json:"count"`
			}
		}, error) {
			p, err := mustPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			n, err := s.RevokeOtherSessions(ctx, p)
			if err != nil {
				return nil, err
			}
			out := &struct {
				Body struct {
					Count int `json:"count"`
				}
			}{}
			out.Body.Count = int(n)
			return out, nil
		})
}

// UserList is one page of users.
type UserList struct {
	Items      []User  `json:"items"`
	NextCursor *string `json:"next_cursor,omitempty"`
}

func registerUsers(api huma.API, s *Service) {
	admin := httpx.MinRole(kernel.Admin)

	huma.Register(api, httpx.Op("listUsers", http.MethodGet, "/api/v1/users", admin),
		func(ctx context.Context, in *listIn) (*struct{ Body UserList }, error) {
			after, afterID, err := page.DecodeOpt(in.Cursor)
			if err != nil {
				return nil, err
			}
			limit := page.Limit(in.Limit)
			rows, err := s.q(nil).ListUsers(ctx, dbq.ListUsersParams{AfterCreated: after, AfterID: afterID, Lim: int32(limit + 1)})
			if err != nil {
				return nil, err
			}
			out := &struct{ Body UserList }{Body: UserList{Items: []User{}}}
			if len(rows) > limit {
				rows = rows[:limit]
				last := rows[len(rows)-1]
				c := page.Encode(last.CreatedAt, last.ID)
				out.Body.NextCursor = &c
			}
			for _, u := range rows {
				out.Body.Items = append(out.Body.Items, toUser(u))
			}
			return out, nil
		})

	huma.Register(api, withStatus(httpx.Op("createUser", http.MethodPost, "/api/v1/users", admin), http.StatusCreated),
		func(ctx context.Context, in *struct {
			Body struct {
				Email    string `json:"email" format:"email" maxLength:"320"`
				Name     string `json:"name,omitempty" maxLength:"200"`
				Role     string `json:"role" enum:"viewer,operator,editor,admin"`
				Password string `json:"password" minLength:"10" maxLength:"1024" doc:"Temporary password. The user must change it at first login."`
			}
		}) (*userOut, error) {
			u, err := s.CreateUser(ctx, in.Body.Email, in.Body.Name, role(in.Body.Role), in.Body.Password, true)
			if err != nil {
				return nil, err
			}
			return &userOut{Body: toUser(u)}, nil
		})

	huma.Register(api, httpx.Op("updateUser", http.MethodPatch, "/api/v1/users/{userId}", admin),
		func(ctx context.Context, in *struct {
			userIDIn
			Body struct {
				Name     *string `json:"name,omitempty" maxLength:"200"`
				Role     *string `json:"role,omitempty" enum:"viewer,operator,editor,admin"`
				Disabled *bool   `json:"disabled,omitempty"`
			}
		}) (*userOut, error) {
			ch := UserChange{Name: in.Body.Name, Disabled: in.Body.Disabled}
			if in.Body.Role != nil {
				r := role(*in.Body.Role)
				ch.Role = &r
			}
			u, err := s.UpdateUser(ctx, in.UserID, ch)
			if err != nil {
				return nil, err
			}
			return &userOut{Body: toUser(u)}, nil
		})

	huma.Register(api, withStatus(httpx.Op("resetUserPassword", http.MethodPost, "/api/v1/users/{userId}/reset-password", admin), http.StatusNoContent),
		func(ctx context.Context, in *struct {
			userIDIn
			Body struct {
				Password string `json:"password" minLength:"10" maxLength:"1024"`
			}
		}) (*struct{}, error) {
			return nil, s.ResetPassword(ctx, in.UserID, in.Body.Password, true)
		})
}

// TokenList is one page of tokens.
type TokenList struct {
	Items      []Token `json:"items"`
	NextCursor *string `json:"next_cursor,omitempty"`
}

// CreatedToken is a new token with its secret. The secret is shown once (REQ-AUTH-005).
type CreatedToken struct {
	Token  Token  `json:"token"`
	Secret string `json:"secret"`
}

func registerTokens(api huma.API, s *Service) {
	viewer := httpx.MinRole(kernel.Viewer)

	huma.Register(api, httpx.Op("listTokens", http.MethodGet, "/api/v1/tokens", viewer),
		func(ctx context.Context, in *struct {
			listIn
			All bool `query:"all" doc:"All users. Needs admin."`
		}) (*struct{ Body TokenList }, error) {
			p, err := mustPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			after, afterID, err := page.DecodeOpt(in.Cursor)
			if err != nil {
				return nil, err
			}
			limit := page.Limit(in.Limit)
			rows, err := s.ListTokens(ctx, p, in.All, after, afterID, limit+1)
			if err != nil {
				return nil, err
			}
			out := &struct{ Body TokenList }{Body: TokenList{Items: []Token{}}}
			if len(rows) > limit {
				rows = rows[:limit]
				last := rows[len(rows)-1]
				c := page.Encode(last.CreatedAt, last.ID)
				out.Body.NextCursor = &c
			}
			for _, t := range rows {
				out.Body.Items = append(out.Body.Items, toToken(t))
			}
			return out, nil
		})

	huma.Register(api, withStatus(httpx.Op("createToken", http.MethodPost, "/api/v1/tokens", viewer), http.StatusCreated),
		func(ctx context.Context, in *struct {
			Body struct {
				Name          string `json:"name" minLength:"1" maxLength:"100"`
				Role          string `json:"role" enum:"viewer,operator,editor,admin"`
				ExpiresInDays *int   `json:"expires_in_days,omitempty" minimum:"1" maximum:"365"`
			}
		}) (*struct{ Body CreatedToken }, error) {
			p, err := mustPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			secret, row, err := s.CreateToken(ctx, p, in.Body.Name, role(in.Body.Role), in.Body.ExpiresInDays)
			if err != nil {
				return nil, err
			}
			return &struct{ Body CreatedToken }{Body: CreatedToken{Token: toToken(row), Secret: secret}}, nil
		})

	huma.Register(api, withStatus(httpx.Op("revokeToken", http.MethodDelete, "/api/v1/tokens/{tokenId}", viewer), http.StatusNoContent),
		func(ctx context.Context, in *struct {
			TokenID uuid.UUID `path:"tokenId"`
		}) (*struct{}, error) {
			p, err := mustPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			return nil, s.RevokeToken(ctx, p, in.TokenID)
		})
}
```

Check four points against the old code and `api/openapi.yaml`:
- **Request bodies.** The body schemas of `UpdateMeRequest`, `ChangePasswordRequest` and `ResetPasswordRequest`. Their required fields and limits must match the spec.
- **Return types.** The `dbq.User.Role` type and the `ListTokensRow.Role` type. If one is not a `string`, convert it with `string(...)`.
- **The pagination parameters.** The `Cursor` and `Limit` component parameters, which give the name and the maximum.
- **The token list parameter.** The `all` query parameter.

The old `CreateUser` handler passes `string(req.Body.Email)`. The new one passes `in.Body.Email`.

If the `huma.Register` type inference rejects the anonymous output struct of `revokeOtherSessions`, declare a named type `countOut`.

- [ ] **Step 2: Register the routes.** In `internal/app/routes.go`, replace the body of `registerRoutes` with:

```go
	auth.Routes(api, s.Auth)
	_ = r
```

- [ ] **Step 3: Build and generate.** Run: `go build ./... && go run ./cmd/sluice openapi > /tmp/sluice-openapi.yaml && grep -c operationId /tmp/sluice-openapi.yaml`
Expected: `13`.

- [ ] **Step 4: Run the auth scenarios.** Run: `E2E_RUN='SCN_AUTH|SCN_API' scripts/e2e-compare.sh`
Expected: `no regression`. If a status or a field name differs, compare the huma type with `api/openapi.yaml`. The e2e assertions do not change.

- [ ] **Step 5: Run the full compare.** Run: `scripts/e2e-compare.sh`
Expected: `no regression`.

- [ ] **Step 6: Commit.**

```bash
git add internal/auth/routes.go internal/app/routes.go
git commit -m "Auth: serve session, user and token operations with huma

Claude-Session: https://claude.ai/code/session_01HwCQCngd1tXrcHmnNajwk3"
```

---

### Task 10: Audit and instances on huma

**Files:**
- Create: `internal/audit/routes.go`, `internal/instance/routes.go`
- Modify: `internal/app/routes.go`

**Interfaces:**
- Produces:
  - `audit.Routes(api huma.API, w *Writer)`, with operation `listAuditEvents`: `GET /api/v1/audit`, admin. Query parameters: `actor`, `action`, `target`, `from`, `to`, `cursor`, `limit`.
  - `instance.Routes(api huma.API, reg *Registry, clk clock.Clock)`, with operation `listInstances`: `GET /api/v1/instances`, admin.

- [ ] **Step 1: Write `internal/audit/routes.go`.** Move the body of `ListAuditEvents` from `internal/auth/handlers.go`. Copy the `AuditEvent` schema from the spec into a Go type `Event`. Map the query parameters as follows:
- `Actor string \`query:"actor"\``, `Action string \`query:"action"\`` and `Target string \`query:"target"\``.
- `From time.Time \`query:"from"\`` and `To time.Time \`query:"to"\``. Pass a pointer only when the value is not zero, because `audit.Filter` takes `*time.Time` in the old code.
- `Cursor` and `Limit` as in rule 7.

```go
// Routes registers the audit log operation (REQ-AUTH-007).
func Routes(api huma.API, w *Writer) {
	huma.Register(api, httpx.Op("listAuditEvents", http.MethodGet, "/api/v1/audit", httpx.MinRole(kernel.Admin)),
		func(ctx context.Context, in *listIn) (*struct{ Body EventList }, error) {
			f := Filter{Actor: in.Actor, Action: in.Action, Target: in.Target, Cursor: in.Cursor, Limit: page.Limit(in.Limit)}
			if !in.From.IsZero() {
				f.From = &in.From
			}
			if !in.To.IsZero() {
				f.To = &in.To
			}
			rows, next, err := w.List(ctx, f)
			if err != nil {
				return nil, err
			}
			out := &struct{ Body EventList }{Body: EventList{Items: []Event{}}}
			if next != "" {
				out.Body.NextCursor = &next
			}
			for _, r := range rows {
				d := r.Details
				if d == nil {
					d = map[string]any{}
				}
				out.Body.Items = append(out.Body.Items, Event{ID: r.ID, Ts: r.Ts, ActorType: string(r.ActorType), ActorID: r.ActorID,
					ActorLabel: r.ActorLabel, Action: r.Action, TargetType: r.TargetType, TargetID: r.TargetID, Details: d, IP: r.IP})
			}
			return out, nil
		})
}
```

Define `listIn`, `EventList` and `Event` in the same file, with JSON names from the spec. Check the field types of `Filter` with `go doc ./internal/audit Filter`, and adapt if they differ.

- [ ] **Step 2: Write `internal/instance/routes.go`.** Move the body of `System.ListInstances` from `internal/api/system.go`. The output is `struct{ Body struct{ Items []Instance \`json:"items"\` } }`. Copy the fields of the `Instance` schema from the spec.

- [ ] **Step 3: Register both.** In `registerRoutes`, add `audit.Routes(api, s.Audit)` and `instance.Routes(api, s.Instances, s.Clock)`. Add `instance` to the platform rule in depguard only if a platform package imports it. No platform package imports it.

- [ ] **Step 4: Run the scenarios.** Run: `E2E_RUN='SCN_AUTH|SCN_CORE_006' scripts/e2e-compare.sh && scripts/e2e-compare.sh`
Expected: `no regression` for both.

- [ ] **Step 5: Commit.**

```bash
git add internal/audit/routes.go internal/instance/routes.go internal/app/routes.go
git commit -m "Audit and instances: serve with huma

Claude-Session: https://claude.ai/code/session_01HwCQCngd1tXrcHmnNajwk3"
```

---

### Task 11: Namespaces, files and flows on huma

**Files:**
- Create: `internal/namespace/routes.go` (split into `routes_namespaces.go`, `routes_files.go` and `routes_flows.go` if one file is longer than about 400 lines)
- Modify: `internal/app/routes.go`

**Interfaces:**
- Produces: `namespace.Routes(api huma.API, r chi.Router, s *Service)` with these operations. The source of each handler body is `internal/namespace/handlers.go`.

| Operation | Method and path | Access | Status | Kind |
|---|---|---|---|---|
| listNamespaces | GET /api/v1/namespaces | viewer | 200 | huma |
| createNamespace | POST /api/v1/namespaces | editor | 201 | huma |
| getNamespace | GET /api/v1/namespaces/{namespace} | viewer | 200 | huma |
| deleteNamespace | DELETE /api/v1/namespaces/{namespace} | admin | 204 | huma |
| listFiles | GET /api/v1/namespaces/{namespace}/files | viewer | 200 | huma |
| getFile | GET /api/v1/namespaces/{namespace}/file | viewer | 200 | Raw, `application/octet-stream` |
| uploadFile | PUT /api/v1/namespaces/{namespace}/file | editor | 201 | Raw |
| saveChanges | POST /api/v1/namespaces/{namespace}/changes | editor | 201 | huma |
| listVersions | GET /api/v1/namespaces/{namespace}/versions | viewer | 200 | huma |
| diffVersions | GET /api/v1/namespaces/{namespace}/diff | viewer | 200 | huma |
| revertVersion | POST /api/v1/namespaces/{namespace}/revert | editor | 201 | huma |
| validateFile | POST /api/v1/namespaces/{namespace}/validate | viewer | 200 | huma |
| listFlows | GET /api/v1/flows | viewer | 200 | huma |
| getFlow | GET /api/v1/flows/{namespace}/{flowId} | viewer | 200 | huma |
| updateFlow | PATCH /api/v1/flows/{namespace}/{flowId} | editor | 200 | huma |
| listFlowRevisions | GET /api/v1/flows/{namespace}/{flowId}/revisions | viewer | 200 | huma |
| getFlowRevision | GET /api/v1/flows/{namespace}/{flowId}/revisions/{revisionId} | viewer | 200 | huma |
| diffFlowRevisions | GET /api/v1/flows/{namespace}/{flowId}/diff | viewer | 200 | huma |
| getFlowSchema | GET /api/v1/schemas/flow.json | public | 200 | huma, `Body json.RawMessage` |

- [ ] **Step 1: Write the huma operations.** Follow the Rules section for each operation in the table. The namespace name validation (SCN-NS-001 in `tests/e2e/namespace_test.go`) must return 422 `validation_failed`. If the old spec has a `pattern` on the name, use the same `pattern` tag. If the service returns the validation error, keep the service call.

- [ ] **Step 2: Write the two Raw operations.** This is the pattern for `getFile`:

```go
	getFile := httpx.Op("getFile", http.MethodGet, "/api/v1/namespaces/{namespace}/file", httpx.MinRole(kernel.Viewer))
	getFile.Parameters = append(httpx.PathParams("namespace"),
		&huma.Param{Name: "path", In: "query", Required: true, Schema: &huma.Schema{Type: huma.TypeString}},
		&huma.Param{Name: "version", In: "query", Schema: &huma.Schema{Type: huma.TypeInteger}})
	getFile.Responses = httpx.RawResponse(http.StatusOK, "application/octet-stream", "File content.")
	httpx.Raw(api, r, getFile, func(w http.ResponseWriter, req *http.Request) {
		// Move the body of the old GetFile handler and its Visit function here.
		// Read the namespace with chi.URLParam(req, "namespace"), and the query with req.URL.Query().
	})
```

The two comment lines in the handler describe the work. Replace them with the moved code. The query parameter names come from `FilePathQuery` and `VersionQuery` in `api/openapi.yaml#/components/parameters`. `uploadFile` uses the same pattern with the query parameters `path`, `message` and `executable`, and `httpx.RawResponse(http.StatusCreated, "application/json", "Saved.")`. Its handler writes the JSON response with `httpx.WriteJSON(w, http.StatusCreated, body)`. Keep the `SLUICE_MAX_FILE_BYTES` limit: use `http.MaxBytesReader` as the old code does.

- [ ] **Step 3: Register.** In `registerRoutes`, add `namespace.Routes(api, r, s.Namespaces)` and remove the `_ = r` line.

- [ ] **Step 4: Run the scenarios.** Run: `E2E_RUN='SCN_NS|SCN_FLOW|SCN_STO' scripts/e2e-compare.sh && scripts/e2e-compare.sh`
Expected: `no regression` for both.

- [ ] **Step 5: Commit.**

```bash
git add internal/namespace internal/app/routes.go
git commit -m "Namespaces, files and flows: serve with huma

Claude-Session: https://claude.ai/code/session_01HwCQCngd1tXrcHmnNajwk3"
```

---

### Task 12: Executions and the runner protocol on huma

**Files:**
- Create: `internal/execution/routes.go`, `internal/execution/runner_routes.go`
- Move: the middlewares `TokenMiddleware`, `ContentTypeMiddleware`, `ErrWrongTaskRun` and `ErrArtifactTooLarge` from `internal/runnerapi/server.go` to `internal/execution/runner_routes.go`
- Modify: `internal/app/routes.go`, `internal/app/server.go` (middlewares from `execution`)

**Interfaces:**
- Produces:
  - `execution.Routes(api huma.API, r chi.Router, e *Engine)`
  - `execution.RunnerRoutes(api huma.API, r chi.Router, e *Engine, maxArtifactBytes int64)`
  - `execution.RunTokenMiddleware(e *Engine) func(http.Handler) http.Handler`
  - `execution.RunnerContentType(next http.Handler) http.Handler`

  The `runnerapi.Backend` interface goes away, because the routes call `*Engine` directly.

| Operation | Method and path | Access | Status | Kind |
|---|---|---|---|---|
| triggerFlow | POST /api/v1/flows/{namespace}/{flowId}/executions | operator | 201 | huma |
| runFile | POST /api/v1/namespaces/{namespace}/run | operator | 201 | huma |
| listExecutions | GET /api/v1/executions | viewer | 200 | huma. Query: state, namespace, flow, trigger_type, label, from, to, sort, cursor, limit |
| getExecution | GET /api/v1/executions/{executionId} | viewer | 200 | huma |
| cancelExecution | POST /api/v1/executions/{executionId}/cancel | operator | 202 | huma |
| rerunExecution | POST /api/v1/executions/{executionId}/rerun | operator | 201 | huma |
| restartExecution | POST /api/v1/executions/{executionId}/restart | operator | 201 | huma |
| getExecutionLogs | GET /api/v1/executions/{executionId}/logs | viewer | 200 | huma. Query: task, search, limit, cursor |
| streamExecutionLogs | GET /api/v1/executions/{executionId}/logs/stream | viewer | 200 | Raw, `text/event-stream` |
| downloadExecutionLogs | GET /api/v1/executions/{executionId}/logs/download | viewer | 200 | Raw, `text/plain` |
| streamExecutionEvents | GET /api/v1/executions/{executionId}/events | viewer | 200 | Raw, `text/event-stream` |
| listExecutionMetrics | GET /api/v1/executions/{executionId}/metrics | viewer | 200 | huma |
| listExecutionArtifacts | GET /api/v1/executions/{executionId}/artifacts | viewer | 200 | huma |
| downloadArtifact | GET /api/v1/executions/{executionId}/artifacts/{artifactId} | viewer | 200 | Raw, `application/octet-stream` |
| runnerGetSpec | GET /api/runner/v1/task-runs/{taskRunId}/spec | run token | 200 | huma, `Body runnerproto.Spec` |
| runnerGetBundle | GET /api/runner/v1/task-runs/{taskRunId}/bundle | run token | 200 | Raw, `application/gzip` |
| runnerPostLogs | POST /api/runner/v1/task-runs/{taskRunId}/logs | run token | 204 | huma, `Body runnerproto.LogBatch` |
| runnerPostEvents | POST /api/runner/v1/task-runs/{taskRunId}/events | run token | 204 | huma, `Body runnerproto.EventBatch` |
| runnerPutArtifact | PUT /api/runner/v1/task-runs/{taskRunId}/artifacts/{name} | run token | 204 | Raw |
| runnerHeartbeat | POST /api/runner/v1/task-runs/{taskRunId}/heartbeat | run token | 200 | huma, `Body runnerproto.HeartbeatResponse` |
| runnerComplete | POST /api/runner/v1/task-runs/{taskRunId}/complete | run token | 204 | huma, `Body runnerproto.Complete` |

- [ ] **Step 1: Check the runner body limits.** huma limits the request body size with `Operation.MaxBodyBytes`, and the default is 1 MiB. Log batches can be larger. For `runnerPostLogs` and `runnerPostEvents`, set `op.MaxBodyBytes` to the value that the runner uses. It is `runnerproto.BatchBytes` plus room for the JSON envelope. Check the limit in the old server code and use the same value. `triggerFlow` inputs follow the old spec.

- [ ] **Step 2: Write `routes.go` and `runner_routes.go`.** Follow the Rules section. Move the SSE bodies from `internal/execution/api.go` (lines 410 to 575) into the `httpx.Raw` handlers without a change, and read `Last-Event-ID` with `req.Header.Get("Last-Event-ID")`. The SSE handlers use `http.NewResponseController(w).Flush()`. That still works: chi and the Sluice middlewares pass the writer through, and `StatusRecorder` implements `Unwrap`.

- [ ] **Step 3: Move the runner middlewares.** Move them into `runner_routes.go`, and change `Backend` to `*Engine`. In `app/server.go`, change the `r.Use` line to use `execution.RunnerContentType` and `execution.RunTokenMiddleware(s.Engine)`. Keep `runnerapi` compiled for the legacy mux until Task 13. If a name is duplicated, give the new copy its new name.

- [ ] **Step 4: Register.** Add `execution.Routes(api, r, s.Engine)` and `execution.RunnerRoutes(api, r, s.Engine, s.MaxArtifactBytes)` to `registerRoutes`.

- [ ] **Step 5: Check the operation count.** Run: `go run ./cmd/sluice openapi | grep -c operationId`
Expected: `55`.

- [ ] **Step 6: Run the scenarios.** Run: `E2E_RUN='SCN_EXE|SCN_RUN|SCN_EXR|SCN_TRG|SCN_API|SCN_CORE' scripts/e2e-compare.sh && scripts/e2e-compare.sh`
Expected: `no regression` for both.

- [ ] **Step 7: Commit.**

```bash
git add -A internal/execution internal/app
git commit -m "Executions and runner protocol: serve with huma and Raw routes

Claude-Session: https://claude.ai/code/session_01HwCQCngd1tXrcHmnNajwk3"
```

---

### Task 13: Remove the old HTTP stack and add the inventory test

**Files:**
- Delete:
  - `internal/api/` (all files, including `apigen/`)
  - `api/oapi-codegen.yaml`
  - `internal/auth/handlers.go` (keep `RequestMeta`, `withMeta` and `MetaFrom`: move them to `internal/auth/middleware.go`)
  - `internal/auth/permissions.go`
  - `internal/namespace/handlers.go`
  - the strict handler code in `internal/execution/api.go`
  - `internal/runnerapi/`
- Modify:
  - `internal/auth/middleware.go`: delete `Authorize`, `AuthorizeRoute`, `RequireRole` and `check`
  - `internal/app/server.go`: delete `legacyAPI`, and add `r.Handle("/api/*", httpx.NotFoundJSON())`
  - `justfile`: the `gen` and `gen-check` recipes
  - `ui/package.json`: the `gen:api` script
  - `.golangci.yml`: remove the `!**/internal/api/**` exclusions
  - `go.mod`: remove kin-openapi, oapi-codegen runtime and the oapi-codegen tool
  - `api/openapi.yaml`, which is now generated
- Create: `internal/app/inventory_integration_test.go`

**Interfaces:**
- Produces: `TestSCN_AUTH_006_RouteInventory` (tag `integration`).

- [ ] **Step 1: Write the new inventory test.** It fails to compile until Step 3 removes the old names that it replaces.

```go
//go:build integration

package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/platform/logging"
	"github.com/alternayte/sluice/internal/testutil/pgtest"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	dbURL := pgtest.Shared(t).NewDatabase(t)
	cfg, err := LoadConfig(LoadOptions{Server: true, Env: map[string]string{
		"SLUICE_DATABASE_URL": dbURL,
		"SLUICE_PUBLIC_URL":   "http://127.0.0.1:8080",
	}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(context.Background(), cfg, logging.New(io.Discard, "error", "text"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Pool.Close)
	return s
}

type invOp struct {
	ID, Method, Path string
	Access           httpx.Access
}

func inventory(t *testing.T) []invOp {
	t.Helper()
	r := chi.NewMux()
	api := httpx.NewAPI(r)
	registerRoutes(api, r, services{})
	var out []invOp
	for path, item := range api.OpenAPI().Paths {
		for _, op := range []*huma.Operation{item.Get, item.Put, item.Post, item.Delete, item.Patch} {
			if op == nil {
				continue
			}
			acc, ok := httpx.AccessOf(op)
			if !ok {
				t.Errorf("%s %s has no access", op.Method, path)
			}
			out = append(out, invOp{ID: op.OperationID, Method: op.Method, Path: path, Access: acc})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

var paramRe = regexp.MustCompile(`\{[^}]+\}`)

// TestSCN_AUTH_006_RouteInventory calls every operation without authentication and with
// each role. The result must match the access of the operation (REQ-AUTH-006, SI-03).
func TestSCN_AUTH_006_RouteInventory(t *testing.T) {
	ops := inventory(t)
	if len(ops) < 55 {
		t.Fatalf("inventory has %d operations, want at least 55", len(ops))
	}
	s := testServer(t)
	h, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx := audit.WithActor(context.Background(), audit.Actor{Type: audit.ActorSystem})
	tokens := map[kernel.Role]string{}
	for _, role := range kernel.AllRoles {
		u, err := s.Auth.CreateUser(ctx, "inv-"+role.String()+"@example.com", "", role, "inventory-pass-1", false)
		if err != nil {
			t.Fatal(err)
		}
		secret, _, err := s.Auth.CreateToken(ctx, &kernel.Principal{UserID: u.ID, Email: u.Email, Role: role}, "inv", role, nil)
		if err != nil {
			t.Fatal(err)
		}
		tokens[role] = secret
	}

	call := func(op invOp, token string) int {
		path := paramRe.ReplaceAllString(op.Path, uuid.NewString())
		var body io.Reader
		if op.Method == http.MethodPost || op.Method == http.MethodPut || op.Method == http.MethodPatch {
			body = bytes.NewReader([]byte("{}"))
		}
		req, _ := http.NewRequest(op.Method, srv.URL+path, body)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	for _, op := range ops {
		acc := op.Access
		if op.ID == "logout" {
			continue // logout with a token only records an event
		}
		own := acc.Public || acc.Other != ""
		anon := call(op, "")
		if own && (anon == http.StatusUnauthorized || anon == http.StatusForbidden) {
			t.Errorf("%s: public route returned %d without auth", op.ID, anon)
		}
		if !own && anon != http.StatusUnauthorized {
			t.Errorf("%s: no auth returned %d, want 401", op.ID, anon)
		}
		for _, role := range kernel.AllRoles {
			got := call(op, tokens[role])
			allowed := own || role >= acc.Min
			if allowed && (got == http.StatusForbidden || got == http.StatusUnauthorized) {
				t.Errorf("%s as %s: %d, want allowed", op.ID, role, got)
			}
			if !allowed && got != http.StatusForbidden {
				t.Errorf("%s as %s: %d, want 403", op.ID, role, got)
			}
		}
	}
}
```

The old test treated runner routes as "own" access and expected no 401 or 403. This test keeps that rule. If a runner route now returns 401 for a user token, compare the result with the old test. Find the commit that deleted it with `git log --diff-filter=D --format=%h -- internal/app/routes_integration_test.go`, then read it with `git show <hash>~1:internal/app/routes_integration_test.go`. The expected behaviour is the old behaviour. Fix the code, not the test.

- [ ] **Step 2: Run it to see it fail.** Run: `go test -tags integration ./internal/app/ -run TestSCN_AUTH_006`
Expected: FAIL or a compile error, because the old files still define conflicting names such as `testServer`. Delete the conflicts in Step 3.

- [ ] **Step 3: Delete the old stack.**

```bash
set -eo pipefail
git rm -r internal/api internal/runnerapi api/oapi-codegen.yaml internal/auth/permissions.go internal/namespace/handlers.go
# Move RequestMeta, withMeta and MetaFrom from auth/handlers.go to auth/middleware.go, then:
git rm internal/auth/handlers.go
```

In `internal/execution/api.go`, delete the `ExecAPI` type and its methods. Keep helpers that `routes.go` uses, and move them to `routes.go`. Delete `api.go` if it is then empty. In `internal/auth/middleware.go`, delete `Authorize`, `AuthorizeRoute`, `RequireRole` and `check`. In `server.go`, delete `legacyAPI` and replace `r.Handle("/api/*", legacy)` with `r.Handle("/api/*", httpx.NotFoundJSON())`.

- [ ] **Step 4: Remove the old dependencies.** Remove the `tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen` line from `go.mod`. Then run: `go mod tidy && go build ./... && ! grep -E 'kin-openapi|oapi-codegen' go.mod`
Expected: the build passes, and the grep finds nothing.

- [ ] **Step 5: Switch the generator.** In the justfile `gen` recipe, replace the oapi-codegen line with:

```just
    go run ./cmd/sluice openapi > api/openapi.yaml
```

In `gen-check`, replace `internal/api/apigen` with `api/openapi.yaml`. In `ui/package.json`, keep `gen:api` on `openapi-typescript ../api/openapi.yaml -o src/api/schema.d.ts` for now. Task 16 replaces it. Remove the `!**/internal/api/**` entries from `.golangci.yml`.

- [ ] **Step 6: Generate and check the UI types.** Run: `just gen && cd ui && bunx tsc --noEmit`
Expected: `api/openapi.yaml` is regenerated. If `tsc` reports errors, they come from renamed schema types in `src/api/schema.d.ts`. Fix the UI imports to the new names with the smallest change. Task 16 replaces the client, so do not refactor the UI here.

- [ ] **Step 7: Run all checks.** Run:

```bash
go test -tags integration ./internal/app/ -run TestSCN_AUTH_006
golangci-lint run ./...
go test -race ./...
scripts/e2e-compare.sh
```

Expected: all pass, and `no regression`.

- [ ] **Step 8: Commit.**

```bash
go mod tidy
git add -A
git commit -m "Remove oapi-codegen, kin-openapi and the permission table; generate api/openapi.yaml; new route inventory test

Claude-Session: https://claude.ai/code/session_01HwCQCngd1tXrcHmnNajwk3"
```

---

### Task 14: sqlc output per feature

**Files:**
- Modify: `sqlc.yaml`
- Move: `db/queries/auth.sql` to `internal/auth/queries.sql` (the audit queries go to `internal/audit/queries.sql`), `db/queries/instances.sql` to `internal/instance/queries.sql`, `db/queries/namespaces.sql` and `db/queries/flows.sql` to `internal/namespace/queries.sql`, and `db/queries/executions.sql` to `internal/execution/queries.sql`
- Delete: `internal/platform/dbq/`, `db/queries/`
- Modify: every `dbq.` user, and the justfile `gen-check` path list

**Interfaces:**
- Produces: the packages `authdb`, `auditdb`, `instancedb`, `namespacedb` and `executiondb` under their features. Each has `New(db DBTX) *Queries`, as sqlc generates.

- [ ] **Step 1: Write the new `sqlc.yaml`.**

```yaml
version: "2"
overrides:
  go:
    overrides:
      - db_type: uuid
        go_type: github.com/google/uuid.UUID
      - db_type: uuid
        nullable: true
        go_type:
          import: github.com/google/uuid
          type: UUID
          pointer: true
      - db_type: timestamptz
        go_type: time.Time
      - db_type: timestamptz
        nullable: true
        go_type:
          import: time
          type: Time
          pointer: true
      - db_type: jsonb
        go_type: encoding/json.RawMessage
      - db_type: jsonb
        nullable: true
        go_type: encoding/json.RawMessage
      - db_type: citext
        go_type: string
x-go: &go
  sql_package: pgx/v5
  emit_json_tags: false
  emit_empty_slices: true
  emit_pointers_for_null_types: true
sql:
  - engine: postgresql
    schema: db/migrations
    queries: internal/auth/queries.sql
    gen: { go: { <<: *go, package: authdb, out: internal/auth/authdb } }
  - engine: postgresql
    schema: db/migrations
    queries: internal/audit/queries.sql
    gen: { go: { <<: *go, package: auditdb, out: internal/audit/auditdb } }
  - engine: postgresql
    schema: db/migrations
    queries: internal/instance/queries.sql
    gen: { go: { <<: *go, package: instancedb, out: internal/instance/instancedb } }
  - engine: postgresql
    schema: db/migrations
    queries: internal/namespace/queries.sql
    gen: { go: { <<: *go, package: namespacedb, out: internal/namespace/namespacedb } }
  - engine: postgresql
    schema: db/migrations
    queries: internal/execution/queries.sql
    gen: { go: { <<: *go, package: executiondb, out: internal/execution/executiondb } }
```

If sqlc rejects the `x-go` key or the merge keys, write the five `go` blocks in full.

- [ ] **Step 2: Move the query files.** Move `InsertAuditEvent` and `DeleteOldAuditEvents` from `auth.sql` to `internal/audit/queries.sql`. Concatenate `namespaces.sql` and `flows.sql` into `internal/namespace/queries.sql`. Delete `db/queries/`.

- [ ] **Step 3: Generate and fix the imports.** Run: `go tool sqlc generate && git rm -r internal/platform/dbq`. In each feature, replace `dbq.` with `<feature>db.` and change the import. For example, run `sed -i '' 's/\bdbq\./authdb./g' internal/auth/*.go`, then goimports.

- [ ] **Step 4: Resolve the cross-feature queries.** `execution` uses `FlowRevision`, `GetSnapshotFile` and possibly other namespace queries. Run `go build ./...`. For each undefined query in `execution`, copy the `-- name:` block from `internal/namespace/queries.sql` into `internal/execution/queries.sql`. A feature owns the reads it needs. Model types such as `executiondb.FlowRevision` come from the shared schema, so each package generates its own. Repeat `go tool sqlc generate && go build ./...` until the build passes.

- [ ] **Step 5: Update `gen-check`.** Replace `internal/platform/dbq` with `internal/*/*db`.

- [ ] **Step 6: Run the checks.** Run: `just gen-check && golangci-lint run ./... && go test -race -tags integration ./... && scripts/e2e-compare.sh`
Expected: all pass, and `no regression`.

- [ ] **Step 7: Commit.**

```bash
git add -A
git commit -m "sqlc: one generated package per feature

Claude-Session: https://claude.ai/code/session_01HwCQCngd1tXrcHmnNajwk3"
```

---

### Task 15: Test cleanup

**Files:**
- Modify or delete: `internal/auth/auth_integration_test.go`, `internal/namespace/namespace_integration_test.go`, `internal/execution/state_integration_test.go`
- Maybe create: new tests in `tests/e2e/` for scenarios that only those files cover

**Interfaces:**
- Produces: no test that calls a service method, except `[I]` scenarios that the SDD tags `[I]`.

- [ ] **Step 1: List the scenario IDs in each file.**

```bash
for f in internal/auth/auth_integration_test.go internal/namespace/namespace_integration_test.go internal/execution/state_integration_test.go; do
  echo "== $f"; grep -oE 'func Test[A-Za-z0-9_]+' "$f"
done
```

- [ ] **Step 2: Decide for each test.** For a test named `TestSCN_X_NNN_...`, find the tag of `SCN-X-NNN` in the SDD with `grep -n 'SCN-X-NNN' docs/sluice-sdd.md`.
  - If the SDD tags it `[I]` and the test calls the package's public API against real Postgres, keep it. For example, SCN-AUTH-012 inspects the database.
  - If the SDD tags it `[E]` and `grep -rn 'SCN_X_NNN' tests/e2e` finds a test, delete the integration test.
  - If the SDD tags it `[E]` and no e2e test exists, move it to `tests/e2e` as an HTTP test with the helpers in `tests/e2e/client_test.go`, then delete the integration test.
  - If a test has no scenario ID, delete it when an e2e test covers the same behaviour. Otherwise, keep it only if it tests pure domain logic.

  Write each decision into the commit message.

- [ ] **Step 3: Run the checks.** Run: `go test -race -tags integration ./... && scripts/e2e-compare.sh`
Expected: PASS, and `no regression`.

- [ ] **Step 4: Commit.**

```bash
git add -A internal tests
git commit -m "Tests: remove service-level tests that e2e covers

<one line per test: kept, moved or deleted, and why>

Claude-Session: https://claude.ai/code/session_01HwCQCngd1tXrcHmnNajwk3"
```

---

### Task 16: hey-api client

**Files:**
- Create: `ui/openapi-ts.config.ts`, `ui/src/api-client.ts`
- Modify: `ui/package.json`, `ui/src/main.tsx` (import `api-client` once), `ui/eslint.config.js` (ignore `src/api/**`)
- Replace: `ui/src/api/` (generated), and the callers of `api` and `unwrap` from the old `src/api/client.ts`
- Delete: `openapi-fetch`, `openapi-typescript`, `src/api/client.ts`, `src/api/schema.d.ts`

**Interfaces:**
- Produces:
  - `ui/src/api/*.gen.ts`: the SDK functions, the `…Options()` query options, the `…Mutation()` mutation options and the query keys.
  - `ApiError` and `onUnauthorized(fn)` from `@/api-client`.

- [ ] **Step 1: Install.** Run: `cd ui && bun add -d @hey-api/openapi-ts && bun remove openapi-fetch openapi-typescript`

- [ ] **Step 2: Write `openapi-ts.config.ts`.**

```ts
import { defineConfig } from "@hey-api/openapi-ts";

export default defineConfig({
  input: "../api/openapi.yaml",
  output: { path: "src/api", format: "prettier" },
  plugins: [
    "@hey-api/client-fetch",
    "@hey-api/typescript",
    "@hey-api/sdk",
    {
      name: "@tanstack/react-query",
      queryOptions: true,
      infiniteQueryOptions: true,
      mutationOptions: true,
      queryKeys: true,
    },
  ],
});
```

Set `"gen:api": "openapi-ts"` in `package.json`. Run `bun run gen:api`. Expected: `src/api/client.gen.ts`, `sdk.gen.ts`, `types.gen.ts` and `@tanstack/react-query.gen.ts`. If the installed version rejects an option, run `bunx openapi-ts --help` and read the plugin page at https://heyapi.dev/openapi-ts/plugins/tanstack-query. Use the names that they give.

- [ ] **Step 3: Write `api-client.ts`.** Copy the `ApiError` class and `onUnauthorized` from the old `client.ts`.

```ts
import { client } from "@/api/client.gen";

/** ApiError carries the error envelope {"error":{"code","message","details"}}. */
export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public details?: unknown,
  ) {
    super(message);
  }
}

const unauthorized = new Set<() => void>();

/** onUnauthorized registers a callback for 401 responses. */
export function onUnauthorized(fn: () => void) {
  unauthorized.add(fn);
  return () => unauthorized.delete(fn);
}

type Envelope = { error?: { code?: string; message?: string; details?: unknown } };

client.setConfig({ baseUrl: "", credentials: "same-origin", throwOnError: true });

client.interceptors.response.use((response) => {
  if (response.status === 401) unauthorized.forEach((fn) => fn());
  return response;
});

client.interceptors.error.use((error, response) => {
  const env = (error ?? {}) as Envelope;
  return new ApiError(
    response?.status ?? 0,
    env.error?.code ?? "http_error",
    env.error?.message ?? response?.statusText ?? "request failed",
    env.error?.details,
  );
});
```

Import `@/api-client` once in `src/main.tsx`, before the router is created.

- [ ] **Step 4: Replace the callers.** Find them with `grep -rln "from \"@/api/client\"\|unwrap(" ui/src`. The pattern:

```ts
// before
const q = useQuery({ queryKey: ["executions", filters], queryFn: async () => unwrap(await api.GET("/api/v1/executions", { params: { query: filters } })) });
// after
import { listExecutionsOptions } from "@/api/@tanstack/react-query.gen";
const q = useQuery(listExecutionsOptions({ query: filters }));
```

For mutations, use `useMutation({ ...createUserMutation(), onSuccess: () => qc.invalidateQueries({ queryKey: listUsersQueryKey() }) })`. The Raw operations have spec entries, but their bodies are streams. Keep `fetch` or `EventSource` for them in the current code, and move that code into `features/<name>/api/` in Task 17. Delete a hand-written helper in `src/lib/*.ts` when no caller remains. Delete its `*.test.ts` file with it only if it tests the removed API wrapper. Keep tests of pure logic.

- [ ] **Step 5: Check the UI.** Run: `cd ui && bun run lint && bunx tsc --noEmit && bun run test && bun run build`
Expected: all pass.

- [ ] **Step 6: Check it end to end.** Run: `just build-ui build-go && cd tests/ui && SLUICE_E2E_BINARY=$PWD/../../bin/sluice bunx playwright test`
Expected: PASS for the specs that passed before. Check the Playwright lines in `docs/build/e2e-baseline.txt`, or run the specs on `HEAD~1` if the baseline has no Playwright lines.

- [ ] **Step 7: Commit.**

```bash
git add -A ui justfile
git commit -m "UI: generated hey-api client with TanStack Query options

Claude-Session: https://claude.ai/code/session_01HwCQCngd1tXrcHmnNajwk3"
```

---

### Task 17: UI feature folders and boundaries

**Files:**
- Create: `ui/src/app/` (providers, router setup, app shell), `ui/src/features/{auth,audit,namespaces,flows,executions,instances}/`
- Modify: every file in `ui/src/routes/` (thin), `ui/eslint.config.js`
- Move: the components and helpers listed in Step 1

**Interfaces:**
- Produces:
  - Each feature exports its page components from `features/<name>/index.ts`.
  - Route files import only from `@/features/*`, `@/components/*` and `@/lib/*`.

- [ ] **Step 1: Move the files with `git mv`.**

| From | To |
|---|---|
| `components/app-shell.tsx`, `components/theme-provider.tsx` | `app/` |
| `components/change-password-form.tsx`, `components/admin-only.tsx`, `lib/auth.ts` | `features/auth/` |
| `components/namespace-source.tsx`, `components/diff-view.tsx`, `components/editor/*`, `lib/namespaces.ts`, `lib/diff.ts` (+ tests) | `features/namespaces/` |
| `lib/flows.ts` (+ test), `components/run-dialogs.tsx` | `features/flows/` |
| `components/gantt.tsx`, `components/log-viewer.tsx`, `components/execution-bits.tsx`, `lib/executions.ts` (+ test) | `features/executions/` |
| `components/data-state.tsx`, `confirm-dialog.tsx`, `state-badges.tsx`, `load-more.tsx`, `components/ui/*` | stay in `components/` |
| `lib/utils.ts`, `lib/roles.ts`, `lib/errors.ts` (+ tests) | stay in `lib/` |

If a moved file is used by two features, keep it in `components/` or `lib/`. For example, `run-dialogs.tsx` is used by flows and namespaces. Fix the imports until `bunx tsc --noEmit` passes.

- [ ] **Step 2: Make the route files thin.** For each file in `routes/` longer than 40 lines, move the page body into `features/<name>/<Name>Page.tsx`. The route file keeps `createFileRoute`, the search parameter validation and the loader, and renders the page:

```tsx
import { createFileRoute } from "@tanstack/react-router";
import { NamespacePage } from "@/features/namespaces";

export const Route = createFileRoute("/namespaces/$namespace")({
  component: function NamespaceRoute() {
    const { namespace } = Route.useParams();
    return <NamespacePage namespace={namespace} />;
  },
});
```

Split `NamespacePage`, which is about 700 lines, into `NamespaceTree.tsx`, `FileEditorPanel.tsx`, `VersionsPanel.tsx` and `SourcePanel.tsx`, by the existing sections of the file. Keep all labels, roles and `data-testid` values. `docs/build/handover.md` lists the ones that tests use.

- [ ] **Step 3: Add the boundary rule.** Run: `cd ui && bun add -d eslint-plugin-import eslint-import-resolver-typescript`. In `eslint.config.js`, add to the config object:

```js
import importPlugin from "eslint-plugin-import";

// in the config object:
    plugins: { "react-hooks": reactHooks, import: importPlugin },
    settings: { "import/resolver": { typescript: true } },
    rules: {
      "react-hooks/rules-of-hooks": "error",
      "react-hooks/exhaustive-deps": "warn",
      "import/no-restricted-paths": [
        "error",
        {
          zones: [
            ...["auth", "audit", "namespaces", "flows", "executions", "instances"].map((f) => ({
              target: `./src/features/${f}`,
              from: "./src/features",
              except: [`./${f}`],
            })),
            { target: "./src/features", from: ["./src/app", "./src/routes"] },
            { target: ["./src/components", "./src/lib"], from: ["./src/features", "./src/app", "./src/routes"] },
          ],
        },
      ],
    },
```

Add `"src/api/**"` to `ignores`.

- [ ] **Step 4: Check the UI.** Run: `cd ui && bun run lint && bunx tsc --noEmit && bun run test && bun run build && cd .. && go run ./tools/buildtool size-check`
Expected: all pass. The size check covers NFR-003.

- [ ] **Step 5: Check the Playwright specs.** Run: `just build-ui build-go && cd tests/ui && SLUICE_E2E_BINARY=$PWD/../../bin/sluice bunx playwright test`
Expected: the same result as in Task 16.

- [ ] **Step 6: Commit.**

```bash
git add -A ui
git commit -m "UI: feature folders, thin routes and import boundaries

Claude-Session: https://claude.ai/code/session_01HwCQCngd1tXrcHmnNajwk3"
```

---

### Task 18: Reduce the build process

**Files:**
- Delete: `tools/buildtool/evidence.go`, `ledger.go`, `ledgerset.go`, `verify.go`, `docs/build/ledger.md`
- Modify: `tools/buildtool/main.go`, `tools/buildtool/buildtool_test.go`, `tools/buildtool/trace.go` (if it reads the ledger or `verify.json`), `justfile`

**Interfaces:**
- Produces: `buildtool <gen|forbid|trace|size-check|setup>`.

- [ ] **Step 1: Delete the files.** Run: `git rm tools/buildtool/evidence.go tools/buildtool/ledger.go tools/buildtool/ledgerset.go tools/buildtool/verify.go docs/build/ledger.md`

- [ ] **Step 2: Trim `main.go`.** Keep the cases `gen`, `forbid`, `trace`, `size-check` and `setup`. Change the usage line to `usage: buildtool <gen|forbid|trace|size-check|setup>`. Change the package comment to `// Command buildtool implements the checks of SDD §10: gen, forbid, trace, size-check and setup.`

- [ ] **Step 3: Fix the build.** Run: `go build ./tools/buildtool && go vet ./tools/buildtool`. Remove the helpers and tests that only the deleted commands used. `buildtool_test.go` can test ledger rules: delete those test functions, and keep the tests for trace and forbid. `trace.go` must read only `build/reports/junit/*.xml`. If it reads `verify.json`, remove that code.

- [ ] **Step 4: Change the justfile.** Delete the recipes `test-int`, `ledger-check`, `verify`, `evidence` and `evidence-check`. Change `test`:

```just
test:
    mkdir -p {{junit}}
    {{gotestsum}} --junitfile {{junit}}/go.xml -- -race -count=1 -tags integration ./...
    if [ -f ui/package.json ]; then cd ui && bun run test --reporter=default --reporter=junit --outputFile.junit=../{{junit}}/vitest.xml; fi
```

- [ ] **Step 5: Run the checks.** Run: `go test ./tools/buildtool && just forbid && just --list`
Expected: PASS. The list shows no removed recipe.

- [ ] **Step 6: Commit.**

```bash
git add -A tools justfile docs/build
git commit -m "Build: remove ledger, verify and evidence; keep trace, forbid and size-check

Claude-Session: https://claude.ai/code/session_01HwCQCngd1tXrcHmnNajwk3"
```

---

### Task 19: Final verification and handover

**Files:**
- Modify: `docs/build/handover.md`, `docs/build/e2e-baseline.txt` (only if a test changed from FAIL to PASS)

- [ ] **Step 1: Run the full definition of done for slice R.** On a clean tree, run:

```bash
set -eo pipefail
just check
just build
just e2e
just trace || true
scripts/e2e-compare.sh
```

Expected:
- `check`, `build`, `e2e` and `e2e-compare` pass.
- `just trace` reports only the scenarios that were OPEN before slice R. Compare its list with the open list in `handover.md`. A scenario that passed before and has no passing test now is a regression: fix it.

- [ ] **Step 2: Check the laptop flow once more.** From a fresh clone of the branch in a temporary directory, run:

```bash
just setup && just up && just dev
```

Expected: `http://localhost:5173` shows the login page, and the `.env` admin can sign in.

- [ ] **Step 3: Check the dependency rules.** Run: `golangci-lint run ./...`. Also run `go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./internal/...`, and check by eye that no feature imports another feature.

- [ ] **Step 4: Update `handover.md`.** Record these facts:
  - Slice R is complete, with the commit SHA.
  - The layout summary, with a pointer to SDD §4.4.
  - The laptop commands.
  - The scenarios that stay OPEN.
  - The next slice: S5.

  Change a baseline FAIL line to PASS only if a test now passes. Write that with `E2E_WRITE_BASELINE=1 scripts/e2e-compare.sh`.

- [ ] **Step 5: Commit.**

```bash
git add docs/build
git commit -m "Slice R complete: handover

Claude-Session: https://claude.ai/code/session_01HwCQCngd1tXrcHmnNajwk3"
```
