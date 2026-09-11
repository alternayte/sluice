# Build handover

This file records where the build stopped. `decisions.md` is the authoritative record of decisions. The build follows SDD 1.1 (DI-25): this file holds the state per slice, `just trace` gives the evidence, and SDD §13 defines done.

## State at the last commit

- Status: all slices of SDD §11 are complete (S0 to S12 and slice R). Every one of the 139 SDD scenarios has a test. The evidence is the SDD §13 run: `just check`, `just build`, `just e2e`, `just e2e-k8s`, `just perf`, then `just trace` on the JUnit reports in `build/reports/junit`.
- S12: `examples/elt/namespace` holds the flow `elt` (dlt extract, SQLMesh transform, pinned Python packages). SCN-FLOW-006, SCN-EX-001 (single container) and SCN-EX-002 (kind) cover it. See `examples/elt/README.md`.
- S11: `internal/ai` (provider settings, Anthropic and OpenAI-compatible adapters, tool registry, MCP at `/mcp`, assistant, triage worker), DI-38 to DI-40. UI: `/settings/ai`, the assistant drawer on all routes, the failure triage panel. The scripted LLM substitute is `internal/testutil/llmserver`; `tests/fixtures/llmserver` is the same substitute as a program with a control API for Playwright.
- S10: dashboard, flow charts, storage status, UI size check, accessibility and mobile width. DI-37 defines the success rate. SCN-NFR-001 measured p95 4.7 ms with 100 000 executions (limit 300 ms).
- S9: kubernetes executor, reconciler, Helm chart and kind harness. `tests/k8s/run.sh` saves the server logs to `build/reports/k8s-server.log`.
- Defects fixed in the last sessions: DI-36 (concurrent triggers held all pool connections), DI-41 (new executions pinned the revision snapshot, so a script change alone did not reach them), DI-42 (the namespace editor saved each new file at once; files are now staged and saved in one version), SCN-CORE-007 (the runner forwards SIGTERM), the huma path parameters of embedded input structs, and the CSP of the CodeMirror styles (DI-32).
- B-1 is resolved (H-1). Keep an eye on free disk space: the Go build cache grows fast with `-race` builds.

## Open work per slice

None.

## Known test behaviour

- Heavy runs at the same time make tests fail on timing: one kind run had a 500 answer in SCN-EXR-007 without a log, and one `just check` run had an Azurite start timeout during an image build. Both passed when they ran alone. Run the SDD §13 steps one after another.
- Do not edit `ui/` while `just build-images` runs: the image build copies the source, and a half-done edit breaks the UI stage.
- The Playwright global setup waits up to 30 s for the mapped Postgres port.
- The first run of the ELT example downloads dlt and SQLMesh with `uv`, so SCN-EX-001 and SCN-EX-002 need network access to PyPI.

## Laptop flow

- `just setup` (installs tools and packages), `just up` (starts Postgres), `just dev` (Go server with live reload and the Vite dev server). Vite listens on :5173 and sends `/api`, `/hooks` and `/mcp` to the Go server on :8080. Sign in with the `.env` bootstrap admin.
- Coolify deploys `deploy/docker/Dockerfile` or `deploy/compose/compose.yml`.
- Kubernetes deploys the Helm chart in `deploy/helm/sluice`.

## Test layout

- Go e2e tests: `tests/e2e` (build tag `e2e`). `startServer` and `startServerOnPort` start the binary. Tests that wait for real minute boundaries (`schedule_test.go`) use `t.Parallel`. Docker tests need `just build-images`.
- Kind tests: `tests/k8s` (build tag `k8s`), run with `just e2e-k8s`. `SLUICE_KIND_KEEP=1` keeps the cluster `sluice-e2e`.
- Perf tests: `tests/perf` (build tag `perf`). They need `SLUICE_E2E_BINARY` (`just build`, then `just perf`). Results go to `build/reports/perf/`.
- Fake-clock scheduler tests: `internal/app/scheduler_integration_test.go` builds servers with `newServer(..., clock)` and calls `Triggers.Tick` directly.
- Playwright: `tests/ui/specs`, shared setup in `tests/ui/helpers/app.ts`.

## How to run checks

- Unit: `go test ./...`, UI: `cd ui && bun run lint && bun run test && bun run build`.
- Integration: `go test -tags integration ./...` (Docker needed).
- E2E: `go test -tags e2e ./tests/e2e/` (builds its own binary without UI), and Playwright `cd tests/ui && SLUICE_E2E_BINARY=$PWD/../../bin/sluice bunx playwright test` after `just build-ui build-go`.
- Fast loop: `just check` (generated-file check, lint, forbid scan, unit and integration tests, UI tests). Full definition of done: SDD §13.
- CI: `.github/workflows/ci.yml` runs `check`, `e2e` and `image` jobs.
- Commit chains must use `set -eo pipefail` and must not pipe a failing command into `tail` or `grep -v` without checking the status.
