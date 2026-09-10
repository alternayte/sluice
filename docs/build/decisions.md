# Sluice v1 decisions

## Implementation decisions

| ID | Date | Area or IDs | Decision | Reason |
|---|---|---|---|---|
| DI-1 | 2026-09-10 | Ledger | A named slice ID without prefix (for example `EXR-004`) maps to `REQ-<id>` when that requirement exists, otherwise to `SCN-<id>`. `SEC-012` maps to `SCN-SEC-012`. | No `REQ-SEC-012` exists in the SDD. |
| DI-2 | 2026-09-10 | REQ-CORE-003, D-10 | Migrations use goose file format (`-- +goose Up`) but a small embedded runner applies them. All pending migrations run in one transaction after `pg_advisory_xact_lock`. Versions are in `schema_migrations`. | goose v3.28 lockers are session-scoped or table-based. REQ-CORE-003 requires a transaction-scoped advisory lock. sqlc still reads the goose files. |
| DI-3 | 2026-09-10 | REQ-CORE-006 | Lease expiry and leader guards use the application clock value as a statement parameter, not `now()`. | Fake-clock scenarios (SCN-TRG-004, SCN-STO-005) need control of time. Instances use NTP-synced clocks. |
| DI-4 | 2026-09-10 | §5 | Migration `00001_init.sql` holds the full §5 data model plus helper columns (for example `task_runs.cancel_requested`, `not_before`, `executions.deadline_at`). It is edited in place until v1 release. | No deployment exists before v1. One schema file is simpler to review. |
| DI-5 | 2026-09-10 | C-06 | The pgx pool uses `QueryExecModeExec` with statement and description caches off. Migrations run in simple protocol. | Unnamed statements work through PgBouncer transaction mode. |
| DI-6 | 2026-09-10 | D-09, REQ-API-002 | Request validation uses kin-openapi `openapi3filter` against the embedded spec. Failures return 422 `validation_failed` with `{field, message}` details. | One contract source. Field details come from the schema errors. |
| DI-7 | 2026-09-10 | D-10 | sqlc generates static queries into `internal/platform/dbq`. Queries with dynamic filters use hand-written pgx SQL. | sqlc cannot express optional filters without complex SQL. |
| DI-8 | 2026-09-10 | §10.2 | `buildtool` (Go, `tools/buildtool`) implements forbid, trace, ledger-check, verify, evidence, evidence-check, size-check and setup. Ledger evidence format is `path::TestName`. Go tools sqlc, oapi-codegen and gotestsum are pinned with the `tool` directive in `go.mod`. | One language for the tooling. Pinned generators keep `gen-check` stable. |
| DI-9 | 2026-09-10 | Test layers | Go tests use build tags: none for unit, `integration` for testcontainers tests ([I] and `tests/review`), `e2e` for `tests/e2e`, `k8s` for `tests/k8s`, `perf` for `tests/perf`. Playwright tests live in `tests/ui`. | `just test` and `just test-int` must select different sets. |
| DI-10 | 2026-09-10 | REQ-UI-010 | Fonts are self-hosted with `@fontsource` packages. | The CSP `default-src 'self'` (SI-12) blocks font CDNs. |

## Human answers

| ID | Answers | Date | Answer |
|---|---|---|---|
