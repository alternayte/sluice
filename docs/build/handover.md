# Build handover

This file records where the build stopped. `decisions.md` is the authoritative record of decisions.

## State at the last commit

- Slices S0 to S4 and slice R (architecture refactor) are complete on branch
  `refactor/architecture`. The last implementation commit is `4d578ef`
  ("Ledger: set S4 requirements PASS whose scenarios pass"). This handover
  commit follows it and closes slice R.
- SDD 1.1 (`docs/sluice-sdd.md`) describes the target layout: vertical slices
  (§4.4), chi and huma with a generated `api/openapi.yaml` (D-09),
  `caarlos0/env` config (D-21), per-feature sqlc (D-10), depguard rules
  (D-22), and access declared per operation with `httpx.Op` (D-23).
- Plan: `docs/superpowers/plans/2026-09-10-architecture-refactor.md`. The checkboxes show the progress.
- Design: `docs/superpowers/specs/2026-09-10-architecture-refactor-design.md`.
- Refactor proof: `scripts/e2e-compare.sh` against `docs/build/e2e-baseline.txt`
  (39 baseline PASS, 2 baseline FAIL, plus 3 tests added during slice R with
  no scenario ID: `TestServerHeadAndBareAPI`, `TestSaveVersionsDiffRevert`,
  `TestLoginRateLimitSharedAcrossInstances`; all 3 pass). The final
  verification run of task 19 found no regression: 42 PASS, the same 2 FAIL
  as the baseline.

## Laptop flow

- `just setup` (installs tools and packages), `just up` (starts Postgres),
  `just dev` (runs the Go server with live reload and the Vite dev server).
  Vite listens on :5173 and sends `/api`, `/hooks` and `/mcp` to the Go
  server on :8080. Sign in with the `.env` bootstrap admin.
- Coolify deploys `deploy/docker/Dockerfile` or `deploy/compose/compose.yml`.
- Kubernetes: the Helm chart comes in S9.

## Open work in S4

- SCN-CORE-001 fails: the CLI help text does not list the `secrets rekey`
  command. That command comes with S6 secrets.
- SCN-CORE-007 fails: after SIGTERM, instance A does not exit within
  `SLUICE_SHUTDOWN_GRACE` (10 s in the test). Check `app.Server.Run`: the order is
  `Engine.Shutdown`, stop background loops, HTTP shutdown. A likely cause is a
  background loop that does not return (the lease leader waits for `leaderWork`,
  or `Engine.Run` waits on a slow query), or open SSE connections that keep
  `http.Server.Shutdown` waiting. Test: `go test -tags e2e -run SCN_CORE_007 ./tests/e2e/`.
- Not written yet: SCN-EXE-010 (claims across two instances, [I]),
  SCN-EXE-014 (retention with a fake clock, [I]), SCN-EXE-012 (templates; needs the
  S6 variables API for vars precedence), SCN-EXE-008, SCN-EXE-009, SCN-UI-*
  and SCN-RUN-008 (Playwright), SCN-RUN-005 (masking; needs S6 secrets),
  SCN-RUN-006 (Toxiproxy), SCN-RUN-007 (needs S8 images), SCN-EXR-001 (needs
  docker and kind detection), SCN-NFR-002 (perf), SCN-CORE-004, SCN-CORE-008,
  SCN-NS-002, SCN-NS-003, SCN-NS-007, SCN-FLOW-003, SCN-FLOW-004, SCN-FLOW-005,
  SCN-FLOW-008 (Playwright or later slices). `just trace` also lists scenarios
  for slices after S4 that are not in scope yet: SCN-AI-*,
  SCN-AUTH-010, SCN-DEP-*, SCN-EX-001, SCN-EX-002, SCN-EXR-003 to
  SCN-EXR-007, SCN-FLOW-006, SCN-GIT-*, SCN-NFR-001, SCN-NFR-003,
  SCN-NS-004, SCN-NS-005, SCN-SEC-*, SCN-TRG-002 to SCN-TRG-007. None of
  these were open before slice R, so none is a regression.
- Playwright specs for S3 and S4 pages are not written. Labels: namespace page
  buttons "New file" (dialog "New file", submit "Create", field "Path"), "Save"
  (dialog "Save file", field "Commit message", submit "Save"), "Rename", "Delete",
  tabs "Files" and "Versions", "Revert to this version" (confirm "Revert"); flow page
  tabs "Overview", "Triggers", "Source", "Revisions", switch "Enabled"/"Disabled";
  editor container `data-testid="file-editor"`; Gantt `data-testid="gantt"` with bars
  `data-testid="gantt-bar"`; log panel `data-testid="log-panel"`; executions table
  `data-testid="executions-table"`.

## How to run checks

- Unit: `go test ./...`, UI: `cd ui && bun run lint && bun run test && bun run build`.
- Integration: `go test -tags integration ./...` (Docker needed).
- E2E: `go test -tags e2e ./tests/e2e/` (builds its own binary without UI), and
  Playwright `cd tests/ui && SLUICE_E2E_BINARY=$PWD/../../bin/sluice bunx playwright test`
  after `just build-ui build-go`.
- Fast loop: `just check` (generated-file check, lint, forbid scan, unit and
  UI tests). Full definition of done: `just check`, `just build`, `just e2e`,
  `just trace`, `scripts/e2e-compare.sh` (SDD §10, §12, §13).
- Commit chains must use `set -eo pipefail` and must not pipe a failing command into
  `tail` or `grep -v` without checking the status.

## Next slices

Next: S5 (schedules, webhooks, flow triggers, two-instance cooperation). After
S5: S6 (secrets, variables, masking), S7 (git), S8 (docker, images, compose),
S9 (kubernetes, Helm, kind), S10 (dashboard, charts, accessibility, perf),
S11 (AI), S12 (example ELT project). Then SDD §13 definition of done.
