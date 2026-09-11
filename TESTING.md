# Sluice tests

This document gives the test layers, the local substitutes and the definition of done. All test recipes write JUnit XML to `build/reports/junit/`.

## Test layers

| Layer | SDD tag | Build tag | Location | Command |
|---|---|---|---|---|
| Unit | — | none | Go packages, `ui/src` | `go test ./...`, `cd ui && bun run test` |
| Integration | `[I]` | `integration` | `*_integration_test.go` in `internal/` | `just test` |
| End-to-end | `[E]` | `e2e` | `tests/e2e` | `just e2e` |
| Playwright | `[U]` | — | `tests/ui/specs` | `just e2e` |
| Kubernetes | `[K]` | `k8s` | `tests/k8s` | `just e2e-k8s` |
| Performance | `[P]` | `perf` | `tests/perf` | `just perf` |

A test calls a public boundary and checks an outcome. Tests do not mock Sluice code (SDD §0 rule 4).

### Unit

```sh
go test ./...
cd ui && bun run lint && bun run test && bun run build
```

### Integration

Integration tests use testcontainers, so Docker must run.

```sh
just test
```

`just test` runs all Go tests with `-race` and the `integration` tag. Then it runs Vitest in `ui/`. To run the Go part only:

```sh
go test -tags integration ./...
```

### End-to-end and Playwright

The e2e tests start the built binary and call it through HTTP and the CLI.

1. Build the UI and the binary:

   ```sh
   just build-ui build-go
   ```

2. Run the Go e2e suite and the Playwright suite:

   ```sh
   just e2e
   ```

To run the Playwright suite alone:

```sh
cd tests/ui && SLUICE_E2E_BINARY=$PWD/../../bin/sluice bunx playwright test
```

The Docker tests in `tests/e2e` need the images. Run `just build-images` first. `just build` builds the UI, the binary and both images.

`just e2e-compare` runs `tests/e2e` and compares the result with `docs/build/e2e-baseline.txt`. It fails when a test that passed in the baseline does not pass now.

### Kubernetes

The Kubernetes tests need kind, kubectl, Helm and the images.

1. Build the images:

   ```sh
   just build-images
   ```

2. Run the tests:

   ```sh
   just e2e-k8s
   ```

`just e2e-k8s` runs `tests/k8s/run.sh`. The script does these steps:

1. It creates the kind cluster `sluice-e2e` and loads the `sluice:dev` and `sluice-uv:dev` images.
2. It deploys Postgres and a Vault dev server with Kubernetes auth.
3. It installs the Helm chart `deploy/helm/sluice` with two replicas.
4. It runs `go test -tags k8s ./tests/k8s/...`.
5. It saves the server logs to `build/reports/k8s-server.log`.
6. It deletes the cluster.

Set `SLUICE_KIND_KEEP=1` to keep the cluster.

### Performance

```sh
just build
just perf
```

The perf tests need `SLUICE_E2E_BINARY`, which `just perf` sets to `bin/sluice`. Results go to `build/reports/perf/`.

## Local substitutes

Tests do not use cloud accounts (D-18). Each substitute replaces only the external system. The Sluice code under test is the production code (SDD §10.4).

| External system | Substitute | Location |
|---|---|---|
| Pooled Postgres | `postgres:17-alpine` behind `edoburu/pgbouncer` in transaction mode | `internal/testutil/pgtest` |
| AWS S3, Cloudflare R2 | MinIO | `internal/testutil/storetest` |
| Azure Blob | Azurite | `internal/testutil/storetest` |
| Azure Key Vault | lowkey-vault | `internal/secret/providers_integration_test.go` |
| HashiCorp Vault | Vault dev server | `internal/secret/providers_integration_test.go`, `tests/e2e`, `tests/k8s/vault.yaml` |
| Kubernetes | kind | `tests/k8s` |
| Git host | Local git server with smart HTTP and SSH | `internal/testutil/gitserver`, `tests/fixtures/gitserver` |
| LLM providers | Scripted servers for the Anthropic Messages API and the OpenAI Chat Completions API | `internal/testutil/llmserver`, `tests/fixtures/llmserver` |
| Network outage | Toxiproxy | `tests/e2e/outage_test.go` |

The programs in `tests/fixtures` have a control API for the Playwright suite.

## Scenario IDs and `just trace`

A test verifies a scenario only when the test name contains the scenario ID (SDD §0 rule 5). Use `SCN-EXE-003` or `SCN_EXE_003`:

```go
func TestSCN_SEC_003_NearestScope(t *testing.T) {
```

```ts
test("SCN-AUTH-001 login errors, reload, logout and old cookie", async ({ page, context }) => {
```

`just trace` reads the JUnit reports in `build/reports/junit/` and writes `build/reports/trace.json`. It fails unless all these rules are true (SDD §10.3):

1. A scenario lists each `REQ-*`, `NFR-*` and `SI-*` ID of the SDD.
2. Each scenario ID has at least one test whose name contains the ID.
3. Each such test passed. No test for a scenario was skipped.
4. No test name refers to an ID that is not in the SDD.

## Definition of done

The SDD §13 run gives the evidence for v1. Run the steps one after another at one commit with a clean tree:

```sh
just check
just build
just e2e
just e2e-k8s
just perf
just trace
```

Each command must exit 0. `just trace` reads the JUnit reports of the runs before it. Also, `docs/build/handover.md` must list no open work, and `docs/build/decisions.md` must have no open blocked entry.

## CI

`.github/workflows/ci.yml` runs on each pull request and on each push to `main`.

| Job | Steps |
|---|---|
| `check` | `just check`, then upload of `build/reports/junit`. |
| `e2e` | `just build-ui build-go`, `scripts/e2e-compare.sh`, the Playwright type check and the Playwright suite. |
| `image` | `docker build` of the `sluice` target of `deploy/docker/Dockerfile`. |

CI does not run `just e2e-k8s` or `just perf`.

## Known test behaviour

- Heavy runs at the same time can make tests fail because of time limits. Run the SDD §13 steps one after another.
- Do not edit `ui/` while `just build-images` runs. The image build copies the source, and a half-done edit breaks the UI stage.
- The Playwright global setup waits up to 30 s for the mapped Postgres port.
- The first run of the ELT example downloads dlt and SQLMesh with `uv`. SCN-EX-001 and SCN-EX-002 need network access to PyPI.
- `-race` builds make the Go build cache grow fast. Monitor the free disk space.

## Rules for commands

Use `set -eo pipefail` in command chains. Do not pipe a command that can fail into `tail` or `grep -v` without a check of its status.
