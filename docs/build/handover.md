# Build handover

This file records where the build stopped. `decisions.md` is the authoritative record of decisions. The build follows SDD 1.1 (DI-25): this file holds the state per slice, `just trace` gives the evidence, and SDD §13 defines done.

## State at the last commit

- Status: in progress, slice S6. B-1 is resolved: H-1 chose option A, `go clean -cache` and `docker builder prune -f` ran, and Docker Desktop restarted on the human's request. The disk had 61 GiB free afterwards. Keep an eye on free disk space: the Go build cache grows fast with `-race` builds.
- Slice S7 is complete: `internal/gitsync` (sources, sync leader on `git-sync`, webhooks, Sync now, runs, push to `sluice/<user>/<time>`), `internal/testutil/gitserver` (git fixture: smart HTTP with token, SSH with key; Go tests call `gitserver.Start(t)`) and `tests/fixtures/gitserver` (the same fixture as a program with a control API, for Playwright). UI: `/settings/git`, the git source panel and "Push to branch" on git namespaces. Passing: SCN-GIT-001 to SCN-GIT-008, SCN-NS-004, SCN-NS-005.
- Next: S8 (docker executor, sluice-uv image, production compose, single-container mode).
- Slice S6 is complete except SCN-SEC-010 (it also needs the S8 docker executor and the S11 AI triage) and SCN-SEC-012 (kind, S9). Passing: SCN-SEC-001 to SCN-SEC-009, SCN-SEC-011, SCN-RUN-005, SCN-EXE-012, SCN-UI-010, SCN-AUTH-010, SCN-CORE-001. UI: `/secrets`, `/variables`, `/settings/secret-providers`, and the Variables and Secrets tabs of a namespace (the route passes the panels to `NamespacePage`, because a feature must not import another feature).
- Two defects fixed in S6: huma drops path parameters of unexported embedded input structs, so put and provider operations now declare their path fields directly; the CSP of SI-12 blocked the CodeMirror `<style>` elements and `data:` images and fonts (DI-32: constructed style sheet, `assetsInlineLimit: 0`, CSS lint markers).
- Slices S0 to S5 and slice R are complete, except the open items below.
- Session of 2026-09-11: SCN-CORE-007 fixed (the runner forwards SIGTERM and flushes for at most 5 s). New tests: SCN-CORE-004, SCN-CORE-008, SCN-EXE-010, SCN-EXE-014, SCN-RUN-006, SCN-NFR-002. Slice S5 added `internal/trigger` (scheduler, webhooks, flow triggers, upcoming schedules) with SCN-TRG-002 to SCN-TRG-007, SCN-FLOW-003, SCN-FLOW-005 and SCN-DEP-004.

## Open work per slice

- S3: SCN-NS-002 (UI gap: new files must be staged in the editor and saved together with a message, REQ-UI-007). SCN-NS-004 and the git part of SCN-NS-005 need S7. SCN-FLOW-006 needs `examples/elt` (S12).
- S4: SCN-RUN-007 (images, S8), SCN-EXR-001 (Docker and kind detection, S8 and S9).
- S6: SCN-SEC-010 (after S8 and S11), SCN-SEC-012 (S9).
- Later slices: S7 to S12 in SDD §11 order, then SDD §13.
- Playwright scenarios still open: SCN-UI-001, SCN-UI-004, SCN-UI-006, SCN-UI-007, SCN-UI-009 (S10).

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
