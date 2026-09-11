# Architecture refactor — design

| Field | Value |
|---|---|
| Date | 2026-09-10 |
| Status | Approved in chat, pending written review |
| Scope | Backend layout, HTTP stack, config, developer setup, frontend layout, test boundary, build process, SDD update |
| Slice | R (new), before S5 |

## 1. Goal

Make the code lean and easy to change before slices S5 to S12 start. The refactor keeps all behaviour. The HTTP contract stays the same: paths, JSON field names, status codes and the error envelope `{"error":{"code","message","details"}}`. The current `tests/e2e` suite is the proof. It must pass before the refactor, after each step, and at the end with no change to its assertions.

## 2. Findings that drive the design

1. Four artifacts describe each route: the hand-written `api/openapi.yaml` (2,320 lines), the kin-openapi validator middleware, the operationId-to-role map in `internal/auth/permissions.go`, and `RecordingMux`.
2. All SQL generates into one shared `dbq` package. All API types generate into one shared `apigen` package. Every feature depends on both.
3. The execution feature is in seven packages.
4. A custom 396-line reflection config loader and a custom migrator exist.
5. `tools/buildtool` has about 1.5k lines of process code: trace, ledger, verify, evidence.
6. `tests/e2e` (2.2k lines) is black-box. It starts the built binary and uses only HTTP. It imports no app package except test helpers.
7. About 1.2k lines of integration tests call service methods directly. These are at the wrong boundary.

## 3. Decisions

| ID | Decision | Reason | Rejected |
|---|---|---|---|
| R-01 | Router: `go-chi/chi` v5. API layer: `danielgtaylor/huma` v2 on the chi adapter, code-first. | Each operation declares types, path and role in one place, in its feature package. huma validates requests and generates the spec. This removes the YAML, kin-openapi, the permission map and `RecordingMux`. | oapi-codegen spec-first on chi (keeps the YAML and the separate permission map). chi only (no TS types). |
| R-02 | `sluice openapi` prints the spec with no server and no database. `just gen` writes it to `api/openapi.yaml`, which stays committed. | The UI codegen needs a spec file with no server process. `gen-check` still finds drift. | Fetch `/openapi.yaml` from a server that runs. |
| R-03 | Errors use the SDD envelope through a custom `huma.NewError`. | Keeps REQ-API-002 and the e2e assertions. | RFC 9457 default of huma. |
| R-04 | TS client: `@hey-api/openapi-ts` with the `@tanstack/react-query` plugin. | Generates query options, mutation options, query keys and infinite queries. Removes hand-written hooks. | openapi-typescript with openapi-fetch (all hooks by hand). orval (larger, more opinionated output). |
| R-05 | Config: `caarlos0/env` v11. One `Config` struct for `server`, `migrate` and `user`. The runner keeps its three run variables. | No dependencies. `required` and defaults. `GetFieldParams` feeds the env doc generator. | Custom loader, viper, koanf, envconfig. |
| R-06 | `.env` loads through `set dotenv-load` in the justfile. The binary reads only the process environment. | C-04 stays true. No config file in production. | godotenv in the binary. |
| R-07 | Migrations: keep the small embedded migrator (DI-2). Files keep the goose format. | goose lockers are session-scoped or table-based. REQ-CORE-003 needs a transaction-scoped advisory lock. A session lock is not safe through a transaction-mode pooler. | The goose library runner. |
| R-08 | Vertical slices with the dependency rules in §4.2, enforced by depguard. | Colocated features. Import loops cannot occur. | Layered packages. Shared `dbq` and `apigen`. |
| R-09 | Auth stays in `internal/auth` for v1. auth-all is the future path after it gains roles, API keys and admin plugins. A separate auth-all design doc covers that work. | auth-all covers about 3 of 9 auth requirements today. Adoption now adds a second user table and a second migration system. | Adopt auth-all now, in full or in part. |
| R-10 | Playwright stays for UI tests. agent-browser is only for manual exploration. | agent-browser has no assertions, workers, retries, reporters, traces or sharding. | agent-browser in CI. |
| R-11 | Locators: `getByRole` first, then `getByLabel`, then `getByText` for messages. `data-testid` only for elements with no accessible name. | Playwright and Testing Library guidance. Role locators also check accessibility (REQ-UI-011). | Test ids everywhere. |
| R-12 | Tests call a public boundary and check an outcome. No mocks of our own code. | Tests survive refactors that keep behaviour. | Tests per handler, service or store. |
| R-13 | Reduce the build process. Keep scenario IDs in test names, `just trace`, `just forbid`, `decisions.md` and `handover.md`. Remove the ledger, verify, evidence and the review protocol. | Keeps the requirement-to-test check. Removes most process code. | Keep all. Remove all. |
| R-14 | The SDD update for this refactor is a one-time exception to SDD §0.1. `decisions.md` records it. §0.1 stays in force after this. | The SDD is the memory that survives a context refresh. It must describe the target architecture. | Leave the SDD unchanged. |
| R-15 | The Dockerfile and `deploy/compose/dev.yml` move from S8 into slice R. Helm stays in S9. | `just up` and a Coolify deploy need them now. | Wait for S8. |
| R-16 | `air` gives live reload in `just dev`. | Popular and simple. | Manual restart. |

## 4. Backend

### 4.1 Layout

```
cmd/sluice/            main
internal/app/          config, wiring, server, CLI commands, `sluice openapi`
internal/kernel/       IDs, Role, Principal, error codes
internal/platform/     db (pgx pool, goose), httpx (huma setup, errors, Op helper),
                       clock, logx, masking, lease, page
internal/flow/         pure domain: parse, validate, template, schema
internal/storage/      blob drivers
internal/snapshot/     shared domain: snapshot manifest and bundle format
internal/runnerproto/  shared: runner wire types, env names, limits
internal/instance/     instance registry and the instances route
internal/auth/         routes.go service.go store.go queries.sql authdb/
internal/audit/        same shape
internal/namespace/    same shape
internal/execution/    engine, dispatcher, state, routes.go, runner_routes.go, store
internal/executor/     inline, process, docker, kubernetes adapters
internal/runner/       `sluice exec` client
db/migrations/         one ordered schema for goose
```

Later slices (`trigger`, `secret`, `variable`, `gitsync`, `metrics`, `ai`) use the same shape.

### 4.2 Dependency rules

1. `kernel` imports no internal package.
2. `platform` imports only `kernel`.
3. A feature imports `kernel`, `platform` and the shared packages `flow`, `storage`, `audit`, `snapshot` and `runnerproto`. `execution` also imports `executor`, its adapter set.
4. A feature does not import another feature. When it needs one, it declares a small interface in its own package. `app` connects the two.
5. Only `app` imports all packages.
6. depguard rules in `.golangci.yml` enforce rules 1 to 5.

### 4.3 File convention in a feature

- `routes.go` registers the huma operations. Each operation declares its role, for example `httpx.Op(http.MethodPost, "/executions", kernel.Operator)`.
- `service.go` holds behaviour and domain rules.
- `store.go` wraps the sqlc queries of the feature. sqlc generates them into `<feature>db/` from `queries.sql`.
- No interface has only one implementation.

### 4.4 Route registration

- Each feature exports `Routes(api huma.API, svc *Service)`.
- Route registration must not need live dependencies. `sluice openapi` passes empty services and prints the spec.
- One huma middleware reads the role from the operation metadata. If an operation has no role, the server does not start. This is default deny.
- The server mounts the SPA, `/healthz`, `/readyz` and `/metrics` directly on chi.

### 4.5 Streamed routes

Eight operations stream a body: `getFile`, `uploadFile`, `streamExecutionLogs`, `downloadExecutionLogs`, `streamExecutionEvents`, `downloadArtifact`, `runnerGetBundle` and `runnerPutArtifact`. They use `httpx.Raw`. It adds the operation to the huma spec and serves a plain `http.HandlerFunc` on chi behind the same access check. No huma streaming spike is necessary.

## 5. Config and developer setup

### 5.1 Config

- Fields declare `env`, `envDefault`, `required` or `notEmpty`, and a `desc` tag.
- A generator of about 30 lines reads `GetFieldParams` and the `desc` tags. It writes `docs/reference/env.md`.
- No `file` option. In caarlos0/env, `file` changes the meaning of the variable to a file path. Kubernetes injects secrets as env vars through `secretKeyRef`.

### 5.2 Files

- `.env.example` is committed with working local values: the compose Postgres URL, a dev master key, bootstrap admin `admin@local.test`, `SLUICE_PUBLIC_URL=http://localhost:8080`.
- `.env` is in `.gitignore`.
- `deploy/compose/dev.yml` holds Postgres for development. MinIO is not in it: the default storage driver is postgres, and storage tests use testcontainers.
- `deploy/compose/compose.yml` holds Postgres and Sluice for Coolify and local trials.
- `deploy/docker/Dockerfile` builds the `sluice` image.

### 5.3 Justfile recipes

| Recipe | Content |
|---|---|
| `setup` | Tool check. Copy `.env.example` to `.env` if absent. Install Bun packages. |
| `up`, `down` | Start or stop Postgres from `deploy/compose/dev.yml`. |
| `dev` | `air` for the Go server and the Vite dev server together. Vite sends `/api` to Go. |
| `db-reset` | Drop and create the dev database, then migrate. |
| `migrate` | Apply migrations. |
| `gen` | sqlc, `sluice openapi`, hey-api, schemas, env docs. |
| `gen-check` | `gen`, then `git diff --exit-code`. |
| `lint` | golangci-lint with depguard, ESLint, `tsc --noEmit`, `forbid`, `helm lint` when the chart exists. |
| `forbid` | No change. |
| `test` | Go tests (including testcontainers), Vitest. |
| `build` | UI build, size check, Go build, images. |
| `e2e` | Build the binary. Run `tests/e2e` and Playwright. |
| `e2e-k8s`, `perf` | No change. |
| `trace` | Rules in §7.3. |
| `check` | `gen-check lint test`. |

### 5.4 Deployment targets

- Laptop: `just setup`, `just up`, `just dev`.
- Coolify: `deploy/docker/Dockerfile` or `deploy/compose/compose.yml`. Env vars come from the Coolify UI.
- Kubernetes: `deploy/helm/sluice` (S9). Env vars come from a ConfigMap and a Secret.

## 6. Frontend

### 6.1 Layout

```
ui/src/
  app/            providers, router setup, app shell
  routes/         file routes only: read params, render a feature component
  features/
    auth/         login, profile, change password, users, tokens
    audit/
    namespaces/   tree, file browser, editor, diff, source info
    flows/        list, detail, run form
    executions/   list, detail, gantt, log viewer, run dialogs
    instances/
  components/     ui/ (shadcn), data-state, confirm-dialog, state-badges, load-more
  lib/            utils, roles, error mapping
  api/            generated by hey-api, not edited
  api-client.ts   base URL, credentials "include", error envelope mapping
```

### 6.2 Rules

- ESLint `import/no-restricted-paths` enforces the direction: shared, then `features`, then `app` and `routes`. A feature does not import another feature.
- A route file has about 40 lines or fewer.
- Components call generated options directly, for example `useQuery(listExecutionsOptions({ query }))`.
- SSE for logs and execution events stays hand-written in `features/executions/api/`.
- Components use real roles and accessible names. Only Gantt bars, chart series, KPI values and the log panel get `data-testid`.

### 6.3 Frontend tests

- Vitest covers pure logic only: editor diagnostics, diff, error mapping, role checks.
- Tests of hand-written API wrappers go away with the wrappers.
- No component render tests. Playwright covers UI behaviour.

## 7. Tests and process

### 7.1 Layers

| Tag | Boundary | Use |
|---|---|---|
| `[E]` | Built binary through HTTP and CLI, real Postgres | Most scenarios |
| `[U]` | Playwright on the built binary | UI scenarios |
| `[I]` | Public API of one Go package, or real infrastructure | Pure domain logic and infrastructure contracts |
| `[K]`, `[P]` | No change | kind, performance |

### 7.2 Current tests

- `tests/e2e`: keep unchanged. It is the refactor proof.
- `tests/ui/specs/auth.spec.ts`: keep. Change selectors only where markup changes.
- Keep and move with their packages: flow fixtures, templates, storage conformance, leases, migrations.
- Service-level integration tests in `auth`, `namespace`, `execution/state` and `app/routes`: delete each one that `tests/e2e` already covers. Move the rest to `tests/e2e` as HTTP tests.
- SCN-AUTH-006 becomes one `[I]` test. It reads all operations from `api.OpenAPI()` and calls each one with each role.

### 7.3 Trace rules

`just trace` fails unless all are true:

1. Every REQ, NFR and SI ID in the SDD appears in at least one scenario.
2. Every scenario ID has at least one test in the latest JUnit reports whose name contains the ID.
3. Every such test passed. No such test was skipped.
4. No test name references an ID that is not in the SDD.

### 7.4 Removed

`docs/build/ledger.md`, `just ledger-check`, `just verify`, `build/reports/verify.json`, `just evidence`, `just evidence-check`, SDD §13. The buildtool keeps `forbid`, `trace`, `size-check`, `setup` and the schema and doc generators.

### 7.5 Definition of done

At one clean commit, `just check`, `just e2e` and `just trace` pass.

## 8. Order of work

`tests/e2e` passes after each step.

1. Update the SDD (§9). Record R-14 in `decisions.md`. Update `handover.md`.
2. Record a baseline `just e2e` run on the current code.
3. Config and developer setup: caarlos0/env, `.env.example`, justfile, dev compose, Dockerfile, `air`.
4. Create `kernel`, `platform/token`, `snapshot`, `runnerproto` and the `instance` feature. Remove feature-to-feature imports. Add depguard.
5. chi with huma. Mount the old generated handler on chi as a fallback. Move features one at a time: auth, audit and instances, namespace and flows, execution with the runner protocol. Then delete `apigen`, kin-openapi and `RecordingMux`. Switch `just gen` to `sluice openapi`.
6. sqlc output per feature. Delete `dbq`.
7. Test cleanup (§7.2) and the new SCN-AUTH-006 test.
8. Frontend: hey-api, feature folders, thin routes, ESLint boundaries.
9. Reduce the buildtool (§7.4).
10. Run `just check`, `just e2e` and `just trace` at one commit.

## 9. SDD changes

| Section | Change |
|---|---|
| §0 | Test layers as in §7.1 of this doc. |
| §3 | D-09: chi with huma, code-first, generated and committed spec, `@hey-api/openapi-ts`. D-10: sqlc per feature, embedded migrator kept. Add decisions for caarlos0/env, slice dependency rules, test boundary, locator rule. D-16: note auth-all as the future path. |
| §4.4 | Layout and dependency rules from §4 of this doc. |
| REQ-API-001 | The spec is generated from code, committed, and checked by `gen-check`. |
| §10.1, §10.2 | Add `air`. Recipe table from §5.3 of this doc. |
| §10.3 | Trace rules from §7.3 of this doc. |
| §11 | Add slice R before S5. Move the Dockerfile and dev compose from S8 into R. |
| §12 | Keep `decisions.md` and `handover.md` only. |
| §13 | Delete. |
| §14 | Definition of done from §7.5 of this doc. |

## 10. Out of scope

- New features from S5 to S12.
- Changes to the HTTP contract.
- Adoption of auth-all.
- The Helm chart (S9).
