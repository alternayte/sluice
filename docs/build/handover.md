# Build handover

This file records where the build stopped. `decisions.md` is the authoritative record of decisions. The build follows SDD 1.1 (DI-25): this file holds the state per slice, `just trace` gives the evidence, and SDD §13 defines done.

## State at the last commit

- Status: in progress, slice S11 (test runs pending), then S12. Slice S9 is complete: `just e2e-k8s` passes all 15 tests (SCN-EXR-001, SCN-EXR-005, SCN-EXR-006, SCN-EXR-007, SCN-DEP-002, SCN-SEC-012). The first kind run had one 500 answer in SCN-EXR-007 (bad image) without a log; the rerun passed. `tests/k8s/run.sh` now saves the server logs to `build/reports/k8s-server.log`.
- Slice S11 backend: `internal/ai` (provider settings, adapters, tool registry, MCP at `/mcp`, assistant, triage worker), DI-38 to DI-40. UI: `/settings/ai`, the assistant drawer on all routes, and the failure triage panel on the execution page. Passing: SCN-AI-001 (API and MCP part), SCN-AI-002, SCN-AI-003, SCN-AI-008, SCN-AI-009. Written but not yet run: SCN-AI-007 (needs the DI-41 fix), SCN-SEC-010 (`tests/e2e/sec010_test.go`, needs `just build-images`), and `tests/ui/specs/assistant.spec.ts` (SCN-AI-001 UI part, SCN-AI-004, SCN-AI-005, SCN-AI-006, AI part of SCN-UI-006). The scripted LLM substitute is `internal/testutil/llmserver`, and `tests/fixtures/llmserver` is the same substitute as a program with a control API for Playwright.
- DI-41 fixed a defect: flow and subflow executions pinned the revision snapshot, so a change of a script alone did not reach new executions. They now pin the namespace head.
- B-1 is resolved: H-1 chose option A, `go clean -cache` and `docker builder prune -f` ran, and Docker Desktop restarted on the human's request. The disk had 61 GiB free afterwards. Keep an eye on free disk space: the Go build cache grows fast with `-race` builds.
- Slice S10 is implemented: `internal/metrics` (dashboard, flow stats and flow metric endpoints), `GET /api/v1/storage`, the dashboard page, the flow charts on the flow overview and `/settings/storage`. Recharts loads lazily; each chart has a data table for screen readers. Passing: SCN-UI-001, SCN-UI-004, SCN-UI-007 (axe), SCN-UI-009. The full Playwright suite passes (24 specs).
- DI-36 fixed a pool deadlock: concurrent API triggers held all pool connections in open transactions and waited for a second connection to load the flow.
- Slice S7 is complete: `internal/gitsync` (sources, sync leader on `git-sync`, webhooks, Sync now, runs, push to `sluice/<user>/<time>`), `internal/testutil/gitserver` (git fixture: smart HTTP with token, SSH with key; Go tests call `gitserver.Start(t)`) and `tests/fixtures/gitserver` (the same fixture as a program with a control API, for Playwright). UI: `/settings/git`, the git source panel and "Push to branch" on git namespaces. Passing: SCN-GIT-001 to SCN-GIT-008, SCN-NS-004, SCN-NS-005.
- Slice S8 (docker executor, `sluice-uv` image, compose, single-container mode) and slice S9 (kubernetes executor, reconciler, Helm chart, kind harness) are implemented. Tests: `tests/e2e/docker_test.go` (SCN-EXR-003, SCN-EXR-004, SCN-RUN-007, SCN-DEP-001, SCN-DEP-003, SCN-DEP-005; they need `just build-images`), `tests/k8s` (SCN-EXR-001, SCN-EXR-005, SCN-EXR-006, SCN-EXR-007, SCN-DEP-002, SCN-SEC-012; run with `just e2e-k8s`, which creates the kind cluster `sluice-e2e`, loads the images and installs Postgres, Vault and the chart; `SLUICE_KIND_KEEP=1` keeps the cluster for debugging).
- Slice S6 is complete except SCN-SEC-010 (it also needs the S8 docker executor and the S11 AI triage) and SCN-SEC-012 (kind, S9). Passing: SCN-SEC-001 to SCN-SEC-009, SCN-SEC-011, SCN-RUN-005, SCN-EXE-012, SCN-UI-010, SCN-AUTH-010, SCN-CORE-001. UI: `/secrets`, `/variables`, `/settings/secret-providers`, and the Variables and Secrets tabs of a namespace (the route passes the panels to `NamespacePage`, because a feature must not import another feature).
- Two defects fixed in S6: huma drops path parameters of unexported embedded input structs, so put and provider operations now declare their path fields directly; the CSP of SI-12 blocked the CodeMirror `<style>` elements and `data:` images and fonts (DI-32: constructed style sheet, `assetsInlineLimit: 0`, CSS lint markers).
- Slices S0 to S5 and slice R are complete, except the open items below.
- Session of 2026-09-11: SCN-CORE-007 fixed (the runner forwards SIGTERM and flushes for at most 5 s). New tests: SCN-CORE-004, SCN-CORE-008, SCN-EXE-010, SCN-EXE-014, SCN-RUN-006, SCN-NFR-002. Slice S5 added `internal/trigger` (scheduler, webhooks, flow triggers, upcoming schedules) with SCN-TRG-002 to SCN-TRG-007, SCN-FLOW-003, SCN-FLOW-005 and SCN-DEP-004.

## Open work per slice

- S3: complete. SCN-NS-002 passes with staged files (DI-42). SCN-FLOW-006 passes with `examples/elt/namespace`.
- Playwright global setup: it waits up to 30 s for the mapped Postgres port. A busy Docker engine (for example during `just build-images`) reported the port late, and the server then used port 5432. Do not edit `ui/` while `just build-images` runs: the image build copies the source, and a half-done edit breaks the UI stage.
- S4: SCN-EXR-001 (kind detection, S9). SCN-RUN-007 passes.
- S6: SCN-SEC-010 (after S11), SCN-SEC-012 (S9).
- S8: complete. The docker e2e tests pass after `just build-images`.
- S9: the `tests/k8s` tests have not run yet. Run `just e2e-k8s`.
- S10: SCN-UI-006 (git source and provider forms; the AI provider part needs S11), SCN-NFR-001 (perf test with 100 000 executions), SCN-NFR-003 (buildtool test with synthetic manifests), and the success rate definition.
- Later slices: S11 and S12 in SDD §11 order, then SDD §13.

## Laptop flow

- `just setup` (installs tools and packages), `just up` (starts Postgres), `just dev` (Go server with live reload and the Vite dev server). Vite listens on :5173 and sends `/api`, `/hooks` and `/mcp` to the Go server on :8080. Sign in with the `.env` bootstrap admin.
- Coolify deploys `deploy/docker/Dockerfile` or `deploy/compose/compose.yml`.
- Kubernetes: the Helm chart comes in S9.

## Test layout

- Go e2e tests: `tests/e2e` (build tag `e2e`). `startServer` and `startServerOnPort` start the binary. Tests that wait for real minute boundaries (`schedule_test.go`) use `t.Parallel`.
- Perf tests: `tests/perf` (build tag `perf`) with their own small harness. They need `SLUICE_E2E_BINARY` (`just build`, then `just perf`). Results go to `build/reports/perf/`.
- Fake-clock scheduler tests: `internal/app/scheduler_integration_test.go` builds servers with `newServer(..., clock)` and calls `Triggers.Tick` directly.
- Playwright: `tests/ui/specs`, shared setup in `tests/ui/helpers/app.ts`.

## How to run checks

- Unit: `go test ./...`, UI: `cd ui && bun run lint && bun run test && bun run build`.
- Integration: `go test -tags integration ./...` (Docker needed).
- E2E: `go test -tags e2e ./tests/e2e/` (builds its own binary without UI), and Playwright `cd tests/ui && SLUICE_E2E_BINARY=$PWD/../../bin/sluice bunx playwright test` after `just build-ui build-go`.
- Fast loop: `just check` (generated-file check, lint, forbid scan, unit and integration tests, UI tests). Full definition of done: SDD §13.
- CI: `.github/workflows/ci.yml` runs `check`, `e2e` and `image` jobs.
- Commit chains must use `set -eo pipefail` and must not pipe a failing command into `tail` or `grep -v` without checking the status.
