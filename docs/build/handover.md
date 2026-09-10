# Build handover

This file records where the build stopped. The ledger (`ledger.md`) and the
decisions (`decisions.md`) are the authoritative records. This file only helps the
next session start fast.

## State at the last commit

- Slices S0 to S3 have their code committed. Items of these slices that need a
  later slice stay IN_PROGRESS (for example SCN-CORE-001 needs `secrets rekey`,
  SCN-CORE-004 needs a PgBouncer integration test with an execution).
- Slice S4 (engine, dispatcher, process and inline executors, runner, runner API,
  logs, executions API and UI) is committed. Its passed scenarios are PASS in the
  ledger.

## Open work in S4

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
  SCN-FLOW-008 (Playwright or later slices).
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
- Commit chains must use `set -eo pipefail` and must not pipe a failing command into
  `tail` or `grep -v` without checking the status.

## Next slices

After S4: S5 (schedules, webhooks, flow triggers, two-instance cooperation), S6
(secrets, variables, masking), S7 (git), S8 (docker, images, compose), S9
(kubernetes, Helm, kind), S10 (dashboard, charts, accessibility, perf), S11 (AI),
S12 (example ELT project). Then `just verify`, SDD §13 review and §14.
