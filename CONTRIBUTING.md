# Contribute to Sluice

This document tells you how to set up Sluice, change it and submit the change. For the test layers, read [TESTING.md](TESTING.md).

## Prerequisites

| Tool | Version or note |
|---|---|
| Go | `go 1.26.2`, from `go.mod` |
| Bun | CI uses `1.2.13` |
| Docker | The daemon must run. Integration tests, e2e tests and image builds need it. |
| just | CI uses `1.58.0` |
| golangci-lint | CI uses `v2.12.2` |
| uv | CI uses `0.7.8`. The e2e tests of the example project need it. |
| git | The git sync tests need it. |
| kind, kubectl, Helm | Only for the Kubernetes tests (`just e2e-k8s`). |

`go.mod` pins the Go tools sqlc, air and gotestsum with the `tool` directive. You do not install them.

## Set up

1. Check the tools, create `.env` and install the UI packages:

   ```sh
   just setup
   ```

   `just setup` prints `ok` or `missing` for each tool. It fails when a tool is missing or the Docker daemon does not answer.

2. Start Postgres for development:

   ```sh
   just up
   ```

3. Start the Go server with live reload and the Vite dev server:

   ```sh
   just dev
   ```

4. Open <http://localhost:5173>. Vite sends `/api`, `/hooks` and `/mcp` to the Go server on port 8080.
5. Sign in with the bootstrap admin of `.env`.

Other recipes:

| Recipe | Purpose |
|---|---|
| `just down` | Stop the development services. |
| `just migrate` | Apply the database migrations. |
| `just db-reset` | Drop the development database, create it again and apply the migrations. |
| `just --list` | Show all recipes. |

## Repository layout

Sluice uses vertical feature packages (SDD §4.4, D-22).

| Path | Content |
|---|---|
| `cmd/sluice/` | The `main` package. |
| `internal/app/` | Configuration, package connections, server, CLI commands and `sluice openapi`. Only this package imports all packages. |
| `internal/kernel/` | Role, principal and context helpers. It imports no internal package. |
| `internal/platform/` | Shared infrastructure: `db`, `httpx`, `token`, `clock`, `logging`, `masking`, `lease`, `page`, `health`, `promx`. |
| `internal/flow/`, `internal/snapshot/`, `internal/runnerproto/`, `internal/storage/`, `internal/audit/` | Shared packages. |
| `internal/auth/`, `internal/instance/`, `internal/namespace/`, `internal/execution/`, `internal/trigger/`, `internal/secret/`, `internal/variable/`, `internal/gitsync/`, `internal/metrics/`, `internal/ai/` | Feature packages. |
| `internal/executor/` | The inline, process, docker and kubernetes adapters of `execution`. |
| `internal/runner/` | The `sluice exec` client. |
| `internal/testutil/` | Test helpers and local substitutes. |
| `db/migrations/` | The SQL schema. |
| `api/openapi.yaml` | The generated OpenAPI document. |
| `ui/` | The React UI. |
| `tests/` | The e2e, Playwright, Kubernetes and perf tests, and the fixtures. |
| `deploy/` | The Dockerfile, the compose files and the Helm chart. |
| `tools/buildtool/` | The `setup`, `gen`, `forbid`, `trace` and `size-check` commands. |

### Import rules

depguard in `.golangci.yml` enforces these rules:

1. `kernel` imports no internal package.
2. `platform` imports only the standard library, `kernel`, `platform`, `db`, `testutil` and a short list of modules.
3. A feature does not import another feature. When a feature needs another feature, declare a small interface in the feature package. Then connect the two in `internal/app`.
4. Only `execution` imports `executor`.
5. Only `internal/app` imports `internal/runner`.

### SQL and sqlc

Each feature keeps its SQL in `queries.sql`. sqlc generates one package per feature, for example `internal/secret/secretdb`. `sqlc.yaml` lists each feature. Do not edit the generated packages.

### API and generated code

The API is code first (D-09). Each feature registers huma v2 operations on a chi v5 router. `sluice openapi` prints the OpenAPI 3.1 document. The UI client in `ui/src/api` comes from `@hey-api/openapi-ts` with the TanStack Query plugin.

Run `just gen` after a change to SQL, to an API operation or to a schema. `just gen` runs these steps:

1. `go tool sqlc generate`
2. `go run ./cmd/sluice openapi > api/openapi.yaml`
3. `bun run gen:api` in `ui/`
4. `go run ./tools/buildtool gen` for the schemas in `schemas/` and the reference docs in `docs/reference/`

Commit the generated files. `just gen-check` fails when they differ from the committed files.

## Add an API operation

1. Add the input and output types to `routes.go` of the feature.
2. Register the operation with `huma.Register` and `httpx.Op`. The last argument of `httpx.Op` is the access level:

   ```go
   huma.Register(api, httpx.Op("listGlobalSecrets", http.MethodGet, "/api/v1/secrets", viewer), handler)
   ```

3. Use `httpx.Raw` for an operation that streams its body. Examples are file downloads and log streams (DI-23).
4. For a new feature, call its `Routes` function in `registerRoutes` in `internal/app/routes.go`.
5. Run `just gen`.
6. Use the generated client of `ui/src/api` in the UI.
7. Add a test whose name contains the scenario ID. Refer to [TESTING.md](TESTING.md).

Every operation must declare its access (D-23). The server does not start when an operation has no access. The route inventory test in `internal/app/inventory_integration_test.go` also checks all routes.

## Add a migration

Migrations are in `db/migrations/` in the goose file format (`-- +goose Up`). A small embedded runner applies them in one transaction (DI-2). sqlc reads the same files as the schema.

Until the v1 release, `00001_init.sql` holds the full data model, and you edit it in place (DI-4). No deployment exists before v1.

1. Change the schema in `db/migrations/00001_init.sql`.
2. Change the queries in `queries.sql` of the feature.
3. Run `just gen`.
4. Run `just db-reset` to apply the schema to the development database.

The migrations must work through a transaction-mode pooler (C-06). Do not use session advisory locks, `LISTEN` or `NOTIFY`, session prepared statements or temporary tables.

## Code style

| Area | Tool | Command |
|---|---|---|
| Go format | gofmt, through golangci-lint | `golangci-lint run ./...` |
| Go lint | golangci-lint with depguard, bodyclose, misspell, nilerr, unconvert | `just lint` |
| UI lint | ESLint, `ui/eslint.config.js` | `cd ui && bun run lint` |
| UI types | TypeScript | `cd ui && bunx tsc --noEmit` |
| Helm chart | helm lint | `just lint` |
| Forbidden words | `buildtool forbid` | `just forbid` |

`just forbid` fails on the words in `scripts/forbid-words.txt` in source files. It also fails on `t.Skip`, `test.skip`, `test.only` and `describe.skip` in test files. No bypass marker exists.

Run the fast loop before you push:

```sh
just check
```

`just check` runs `gen-check`, `lint` and `test`.

## Commits and pull requests

1. Make a branch from `main`.
2. Start the commit subject with the area, then a colon, then a short sentence. Examples from the history:

   ```text
   Storage: bound the s3 upload memory to fix the SCN-STO-001 heap flake
   CI: install uv in the e2e job
   ```

3. Put the scenario IDs and decision IDs of the change in the commit message.
4. Keep generated files in the same commit as their source.
5. Open a pull request against `main`.
6. Make sure that the CI jobs `check`, `e2e` and `image` pass.

## Specification and decisions

`docs/sluice-sdd.md` is the authoritative specification. Do not edit it (SDD §0 rule 1). Items that are not in the SDD are out of v1 scope.

The SDD uses these IDs:

| ID | Description |
|---|---|
| `REQ-<AREA>-nnn` | Requirement |
| `SCN-<AREA>-nnn` | Acceptance scenario |
| `SI-nn` | Security invariant |
| `NFR-nnn` | Non-functional requirement |
| `D-nn` | Design decision |

`docs/build/` holds the build records (SDD §12):

- `docs/build/decisions.md` records each implementation decision as `DI-n` with a date, the IDs, the decision and the reason. When the SDD does not define a behaviour, add a `DI` entry. A blocked item gets a `B-n` entry with the question and the options. A human writes the answer as `H-n`.
- `docs/build/handover.md` records the state of the build and how to run the checks.
