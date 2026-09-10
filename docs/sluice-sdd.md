# Sluice v1 — Solution Design Document

| Field | Value |
|---|---|
| Product | Sluice (working name; rename is a find/replace of `sluice`/`SLUICE_`) |
| Version | SDD 1.0 |
| Status | Approved for build |
| Build scope | Complete v1. No partial build. |
| Path in repo | `docs/sluice-sdd.md` |
| Language | ASD-STE100 style. MUST = required. |

## 0. Rules for this document

1. This SDD is the authoritative specification. The implementer MUST NOT edit it.
2. IDs: `REQ-<AREA>-nnn` requirement, `SCN-<AREA>-nnn` acceptance scenario, `SI-nn` security invariant, `NFR-nnn` non-functional requirement, `D-nn` design decision.
3. Each scenario lists the IDs it verifies in parentheses. `just trace` reads these lists.
4. Scenario test layers: `[I]` Go integration, `[E]` end-to-end against the built binary, `[U]` Playwright UI, `[K]` kind cluster, `[P]` performance.
5. A test verifies a scenario only when the test name contains the scenario ID (`SCN-EXE-003` or `SCN_EXE_003`).
6. Items not in this document are out of v1 scope.

---

## 1. Product and scope

### 1.1 Statement

Sluice is a lean, self-hosted workflow orchestrator. It is one Go binary with an embedded React UI. Postgres holds all state. It runs scripts and commands from namespace files. Files come from git or are edited in the UI. Tasks run as local processes, Docker containers or Kubernetes Jobs. It has user accounts, scoped secrets, run logs, metrics, charts and AI features. dlt and SQLMesh are the first use case. Sluice has no dependency on them.

### 1.2 In scope (v1)

1. Single binary: server, runner, CLI.
2. Postgres state store. Pooler-safe (Neon, PgBouncer transaction mode).
3. User accounts, sessions, API tokens, four fixed roles, audit log.
4. Object storage: postgres, fs, S3-compatible (AWS S3, Cloudflare R2, MinIO), Azure Blob.
5. Namespaces with files. Two source modes: git and managed.
6. Git sync: poll, webhook, push edits to a new branch.
7. Flows in YAML: DAG of tasks, inputs, outputs, retries, timeouts, concurrency.
8. Task types: `script`, `command`, `http`, `subflow`. Direct run of script files.
9. Triggers: manual, schedule, webhook, flow completion.
10. Executors: inline, process, docker, kubernetes. Auto detection. Pools.
11. Runner protocol: logs, outputs, metrics, artifacts, heartbeat, cancel.
12. Secrets and variables: scoped, inherited, masked. Providers: builtin, env, kubernetes, Azure Key Vault, HashiCorp Vault.
13. UI: dashboard, executions, execution detail, flows, files and editor, secrets, variables, settings, charts.
14. AI: provider adapters, MCP server, assistant panel, flow authoring, failure triage.
15. Deployment: container images, Helm chart, single-container mode, two clusters on one database.
16. Example ELT project with dlt and SQLMesh.

### 1.3 Out of scope (v1)

OIDC, SSO, SCIM. Custom roles and namespace-scoped RBAC. Multi-tenancy. AWS Secrets Manager and GCP Secret Manager. GCS storage. Writes into external secret managers. Per-environment secret values. Backfills. Key-value store. Plugin system. Notification integrations (use `http` tasks). Azure Container Apps Jobs executor. SSH executor. SQL task type. DAG graph view. Pull request creation through provider APIs. Conversion between managed and git namespaces. Windows support for `exec`. Email delivery. UI localization.

---

## 2. Hard constraints

| ID | Constraint |
|---|---|
| C-01 | One Go binary. Same binary is server, runner and CLI. |
| C-02 | Postgres is the only state store. No Redis, no broker, no second database. |
| C-03 | No persistent volumes. Containers use only ephemeral storage (`emptyDir` or container filesystem). |
| C-04 | Configuration only from environment variables (CLI flags for CLI commands). No config files. |
| C-05 | Any number of server replicas. Replicas can run in different clusters on one database. |
| C-06 | Database access MUST work through a transaction-mode pooler. No session advisory locks, no LISTEN/NOTIFY, no session-scoped prepared statements, no temp tables. |
| C-07 | Runs as one container with no volumes (Azure Container Apps compatible). |
| C-08 | Kubernetes is the primary target. |
| C-09 | UI is a React SPA embedded with `go:embed`. |
| C-10 | No feature in this SDD is licence-gated. |

---

## 3. Decision log

| ID | Decision | Reason | Rejected |
|---|---|---|---|
| D-01 | Server and runner are one binary (`sluice server`, `sluice exec`). | One artifact. Runner code is identical on all executors. | Separate runner binary. |
| D-02 | Queue = `SELECT … FOR UPDATE SKIP LOCKED`. Leader election = `leases` table with TTL. | Works through poolers (C-06). No extra infrastructure. | River, session advisory locks, LISTEN/NOTIFY. |
| D-03 | Runner pushes logs, events and heartbeats to the server over HTTP. | Same path for process, docker and kubernetes. Survives server restarts. Secrets go to the runner at runtime, not into Job specs. | Reading pod logs and docker logs. |
| D-04 | Runner binary comes from the runner image: Kubernetes init container copies it into `emptyDir`; Docker copies it with the Engine API. | Any task image works. No volumes. Host OS does not matter. | Mandatory custom images, bind mounts. |
| D-05 | Namespace content is stored as content-addressed file objects plus snapshot manifests. Each execution pins one snapshot. | Exact reruns. Tasks need no git credentials. | Clone per task. |
| D-06 | Each namespace has one owner: git or managed. Git namespaces are read-only in the UI. Edits push a new branch. | No two-way sync drift. | Bidirectional sync. |
| D-07 | Secrets are references resolved at dispatch. Builtin values are AES-256-GCM encrypted. External providers are read-only references. | All secret features in the base product. No secret in Postgres plaintext or cluster objects. | Base64 env secrets. |
| D-08 | Four task types plus a JSON Lines emit protocol. | Generic. dlt and SQLMesh use the same protocol as any script. | Plugin system. |
| D-09 | OpenAPI 3.0.3 is the API source. Go server with `oapi-codegen` (strict, `net/http`). TS client with `openapi-typescript` and `openapi-fetch`. | One contract, generated both sides. | Hand-written clients. |
| D-10 | `pgx` v5, `sqlc`, `goose` with embedded migrations. | Typed SQL, no ORM. | ORM. |
| D-11 | Storage uses `gocloud.dev/blob` for s3, azblob and fs, plus a Sluice postgres driver. Default driver is postgres. | One interface. Single container needs no extra infrastructure. | Per-provider SDK code. |
| D-12 | Azure Container Apps is a deployment target only. No ACA executor. | Requirement is that one container runs Sluice. | ACA Jobs executor. |
| D-13 | AI uses two adapters (Anthropic Messages, OpenAI-compatible Chat Completions) and one tool registry shared by MCP and the assistant. MCP uses the official Go SDK. | Provider-agnostic. One permission model for tools. | Per-feature prompts with no tools. |
| D-14 | Schedule firing is exactly-once through a unique key `(trigger_id, scheduled_for)`. | Leader changes cannot duplicate runs. | Leader-only memory state. |
| D-15 | One Sluice deployment is one environment. | Less model complexity. | Environment dimension on values. |
| D-16 | Authentication is in-app (sessions, tokens). | v1 needs user accounts only. | External IdP dependency. |
| D-17 | Execution view uses a Gantt timeline. No graph library. | Lean bundle. | React Flow. |
| D-18 | Tests use deterministic local substitutes: testcontainers (Postgres, PgBouncer, MinIO, Azurite, Vault, lowkey-vault), Docker, kind, local git server, scripted LLM HTTP servers. | Verification without cloud access. | Cloud accounts in CI. |
| D-19 | Dagu (GPL-3.0) may be studied for patterns. Its code MUST NOT be copied. | Licence freedom. | — |
| D-20 | Execution retries, not Kubernetes Job retries (`backoffLimit: 0`). | One retry model on all executors. | Job backoff. |

---

## 4. Architecture

### 4.1 Components

```
                 sluice server (N replicas, one or more clusters)
 browser ─────▶ ┌──────────────────────────────────────────────────────────────┐
 MCP client ──▶ │ HTTP: UI · /api/v1 · /mcp · /hooks · /api/runner/v1          │
 git host  ───▶ │ auth · namespaces · flows · git sync · secrets · AI          │
                │ engine · dispatcher (SKIP LOCKED) · scheduler (lease)        │
                │ reconciler (lease) · maintenance (lease)                     │
                │ executors: inline | process | docker | kubernetes            │
                └──────┬──────────────────┬─────────────────────┬──────────────┘
                       │                  │                     │
                   Postgres        object storage        task process / container
                  (all state)  (postgres|fs|s3|azblob)    running `sluice exec`
                                                           └─▶ /api/runner/v1
```

### 4.2 Commands

`sluice server`, `sluice exec`, `sluice runner-install <dir>` (copies the binary into `<dir>`; used by init containers), `sluice migrate`, `sluice user create`, `sluice user reset-password`, `sluice secrets rekey`, `sluice validate`, `sluice version`.

### 4.3 Execution sequence

1. A trigger creates an `executions` row. It pins `flow_revision_id` and `snapshot_id`. State `QUEUED`.
2. The engine moves the execution to `RUNNING` when concurrency allows. It creates task runs. Tasks with met dependencies become `QUEUED`.
3. A dispatcher on any instance claims a `QUEUED` task run. The claim filters on pool, executor type and free slots. It creates a run token. The task run becomes `RUNNING`.
4. The executor starts `sluice exec` with `SLUICE_API_URL`, `SLUICE_RUN_TOKEN`, `SLUICE_TASK_RUN_ID`.
5. The runner gets the spec (resolved env with secrets) and the bundle. It runs the command. It pushes logs, events and heartbeats.
6. The runner posts `complete`. In one transaction the engine sets the task state, queues ready tasks or finalizes the execution, and queues flow triggers. The run token is revoked. Logs are archived.
7. Inline tasks (`http`, `subflow`) run as goroutines in the claiming instance and use the same state transitions.

### 4.4 Repository layout (package by feature)

```
cmd/sluice/                 main
internal/app/               wiring, config, lifecycle
internal/platform/          db, lease, clock, httpx, logging, masking
internal/auth/              users, sessions, tokens, rbac
internal/audit/
internal/storage/           interface + postgres, fs, s3, azblob
internal/namespace/         files, snapshots, bundles, namespace.yaml
internal/gitsync/
internal/flow/              model, parse, validate, schema, template
internal/execution/         engine, state machine, dispatcher, retention
internal/trigger/           manual, schedule, webhook, flow
internal/runner/            `sluice exec` client
internal/runnerapi/         server side of runner protocol
internal/executor/          inline, process, docker, kubernetes, detect
internal/secret/            registry, providers, keyring
internal/variable/
internal/metrics/           emitted metrics, aggregates for charts
internal/ai/                providers, tools, assistant, triage, mcp
api/openapi.yaml
db/migrations/  db/queries/
schemas/flow.schema.json
ui/                         Vite app
deploy/docker/  deploy/helm/sluice/  deploy/compose/
examples/elt/
tests/e2e/  tests/ui/  tests/k8s/  tests/perf/  tests/review/  tests/fixtures/
docs/sluice-sdd.md  docs/build/  docs/reference/
justfile
```

---

## 5. Data model

All IDs are UUIDv7 unless stated. All timestamps are `timestamptz`. State columns have `CHECK` constraints.

| Table | Key columns |
|---|---|
| `users` | id, email (citext unique), name, password_hash, role, must_change_password, disabled_at, created_at, last_login_at |
| `sessions` | id_hash (pk), user_id, created_at, last_seen_at, expires_at, ip, user_agent |
| `api_tokens` | id, user_id, name, token_hash (unique), prefix, role, expires_at, last_used_at, revoked_at |
| `login_attempts` | email, ip, attempted_at, success |
| `audit_events` | id, ts, actor_type (user, token, system, ai), actor_id, action, target_type, target_id, details jsonb, ip |
| `namespaces` | id, name (unique), source_type (managed, git), git_source_id, head_snapshot_id, description, deleted_at |
| `git_sources` | id, name, repo_url, branch, auth_type (none, https_token, ssh_key), credential_secret_key, known_hosts, poll_interval, webhook_secret_key, last_synced_sha, last_sync_at, last_sync_status, last_error, sync_requested_at |
| `git_mappings` | git_source_id, repo_path, namespace_id (unique) |
| `git_sync_runs` | id, git_source_id, started_at, ended_at, sha, status, error, snapshots_created |
| `file_objects` | hash (sha256 pk), size, created_at |
| `snapshots` | id, namespace_id, version (int, managed), git_sha, manifest_hash, message, created_by, created_at |
| `snapshot_files` | snapshot_id, path, hash, size, executable |
| `bundles` | manifest_hash (pk), storage_key, size, last_used_at |
| `flows` | id, namespace_id, flow_key, path, current_revision_id, valid, disabled, deleted_at |
| `flow_revisions` | id, flow_id, snapshot_id, source_hash, definition jsonb, errors jsonb, created_at |
| `triggers` | id, flow_id, revision_id, trigger_key, type, config jsonb, webhook_key_hash, next_fire_at, last_fired_at, active |
| `executions` | id, namespace_id, flow_id (null for file runs), flow_revision_id, snapshot_id, state, trigger_type, trigger_id, scheduled_for, trigger_payload jsonb, inputs jsonb, labels jsonb, outputs jsonb, error, parent_execution_id, parent_task_run_id, restart_of_id, chain_depth, created_by, created_at, started_at, ended_at, duration_ms, secret_keys_used text[], log_archived; UNIQUE (trigger_id, scheduled_for) |
| `task_runs` | id, execution_id, task_key, attempt, state, reason, executor_type, pool, claimed_by, external_ref, run_token_hash, token_expires_at, heartbeat_at, queued_at, started_at, ended_at, exit_code, error, outputs jsonb, reused_from_id |
| `log_chunks` | id (bigserial), task_run_id, execution_id, seq, first_line, line_count, data (bytea, gzip NDJSON), created_at; UNIQUE (task_run_id, seq) |
| `metrics` | id (bigserial), execution_id, task_run_id, flow_id, name, value (double), unit, tags jsonb, ts |
| `artifacts` | id, execution_id, task_run_id, name, storage_key, size, content_type, created_at |
| `secret_providers` | id, name (unique), type, config jsonb, created_at, updated_at |
| `secrets` | id, namespace_id (null = global), key, provider_id, provider_ref, ciphertext, key_id, description, created_by, updated_by, updated_at, last_resolved_at; UNIQUE (namespace_id, key) |
| `variables` | id, namespace_id (null = global), key, value, updated_by, updated_at; UNIQUE (namespace_id, key) |
| `instances` | id, hostname, version, pools text[], executors text[], started_at, heartbeat_at |
| `leases` | name (pk), holder, expires_at |
| `settings` | key (pk), value jsonb, updated_by, updated_at |
| `ai_conversations` | id, user_id, title, created_at |
| `ai_messages` | id, conversation_id, role, content jsonb, created_at |
| `ai_pending_actions` | id, conversation_id, tool, arguments jsonb, status (pending, confirmed, rejected), decided_by, decided_at |
| `ai_insights` | id, execution_id, kind (triage), status, summary, probable_cause, evidence jsonb, suggested_fix, confidence, model, error, created_at |
| `rate_limits` | key, window_start, count |
| `storage_objects` | key (pk), size, created_at (postgres driver) |
| `storage_chunks` | key, idx, data bytea (postgres driver, 1 MiB chunks) |

---

## 6. Flow specification

### 6.1 Files

1. A flow file matches `*.flow.yaml` or `*.flow.yml` anywhere in a namespace tree. One flow per file.
2. `namespace.yaml` at the namespace root is optional. It sets defaults.
3. Paths in flows are relative to the namespace root.

### 6.2 Example

```yaml
id: pg-to-bq
description: Load orders into BigQuery and transform.
labels: { team: data }
inputs:
  - { id: full_refresh, type: boolean, default: false }
variables: { DATASET: raw }
env:
  PG_URL: ${{ secret('PG_URL') }}
  DATASET: ${{ vars.DATASET }}
triggers:
  - { id: nightly, type: schedule, cron: "0 2 * * *", timezone: Europe/Zurich, catch_up: last }
  - { id: hook, type: webhook }
concurrency: { limit: 1, behavior: queue }
max_parallel: 4
timeout: 2h
retry: { max_attempts: 3, backoff: exponential, initial: 30s, max: 10m }
executor: { type: kubernetes, pool: cluster-a, image: ghcr.io/acme/elt:1.4.0 }
tasks:
  - id: extract
    type: script
    file: pipelines/orders.py
    args: ["--full-refresh=${{ inputs.full_refresh }}"]
  - id: transform
    type: command
    depends_on: [extract]
    command: ["uv", "run", "sqlmesh", "run"]
    workdir: sqlmesh
  - id: notify
    type: http
    depends_on: [transform]
    run_if: always
    method: POST
    url: https://hooks.example.com/x
    headers: { Authorization: "Bearer ${{ secret('HOOK_TOKEN') }}" }
    body: '{"rows": "${{ tasks.extract.outputs.rows }}"}'
outputs:
  rows: ${{ tasks.extract.outputs.rows }}
```

### 6.3 Flow fields

| Field | Type | Rules |
|---|---|---|
| `id` | string | Required. `^[a-z0-9][a-z0-9-]{0,62}$`. Unique in namespace. |
| `description` | string | ≤ 2000 chars. |
| `labels` | map string→string | ≤ 20 entries. |
| `inputs[]` | object | `id` (`^[a-z][a-z0-9_]{0,62}$`), `type` (string, int, number, boolean, select, json), `required`, `default`, `values` (select only), `description`. |
| `variables` | map string→string | Static values. Highest precedence for `vars`. |
| `env` | map string→template | Applies to all tasks. Task `env` overrides by key. |
| `triggers[]` | object | §6.5. |
| `concurrency` | object | `limit` (int ≥ 1), `behavior` (queue, skip). Absent = unlimited. |
| `max_parallel` | int | 0 = unlimited. Default 0. |
| `timeout` | duration | Execution wall time. Default none. |
| `retry` | object | Default task retry. §6.4. |
| `executor` | object | Default task executor. §6.4. |
| `tasks[]` | object | Required. 1–200 tasks. |
| `outputs` | map string→template | Resolved at SUCCESS. |

### 6.4 Task fields

Common: `id` (`^[a-z][a-z0-9_]{0,62}$`, unique), `type`, `depends_on[]`, `run_if` (success, failure, always; default success), `timeout` (default 24h), `retry`, `env`, `executor`.

| Type | Fields | Runs on |
|---|---|---|
| `script` | `file` (required), `runtime` (python, bash, bun, node; default from extension `.py`, `.sh`, `.ts`, `.js`), `args[]` | executor |
| `command` | `command[]` (argv, no shell, required), `workdir` (relative, default root) | executor |
| `http` | `method`, `url`, `headers`, `body`, `expect_status[]` (default 200–299); outputs `status`, `headers`, `body` (≤ 1 MiB, parsed when JSON) | inline |
| `subflow` | `flow` (`<namespace>/<flow_id>`), `inputs`, `wait` (default true); outputs = child flow outputs | inline |

Runtime commands: python → `uv run <file> <args>`; bash → `bash <file> <args>`; bun → `bun run <file> <args>`; node → `node <file> <args>`.

`retry`: `max_attempts` (1–20, default 1), `backoff` (fixed, exponential), `initial` (default 10s), `max` (default 10m).

`executor`:

| Field | Applies to | Rules |
|---|---|---|
| `type` | all | process, docker, kubernetes. Resolution order: task → flow → `namespace.yaml` → instance default. |
| `pool` | all | Default `default`. |
| `image` | docker, kubernetes | Required for these types. |
| `inject_runner` | docker, kubernetes | Default true. False = image contains `sluice` on `PATH`. |
| `pull` | docker | always, if_not_present (default), never. |
| `network` | docker | Docker network name. |
| `resources` | docker, kubernetes | `requests` and `limits` with `cpu`, `memory`. Docker uses `limits`. |
| `kubernetes` | kubernetes | `service_account`, `node_selector`, `tolerations[]`, `image_pull_secrets[]`, `labels`, `annotations`. |

`executor` on `http` or `subflow` tasks is a validation error.

### 6.5 Triggers

| Type | Fields |
|---|---|
| `schedule` | `cron` (5 fields or `@hourly`, `@daily`, `@weekly`, `@monthly`), `timezone` (IANA, default UTC), `catch_up` (none, last; default last), `inputs` |
| `webhook` | `inputs` (templates over `trigger.body`, `trigger.headers`) |
| `flow` | `flow` (`<namespace>/<flow_id>`), `states[]` (SUCCESS, FAILED, TIMED_OUT, CANCELLED), `inputs` (templates over `trigger.outputs`) |

Manual triggering needs no declaration.

### 6.6 States

Execution: `QUEUED → RUNNING → {SUCCESS, FAILED, TIMED_OUT, CANCELLED}`; `QUEUED → {SKIPPED, CANCELLED}`; `RUNNING → CANCELLING → CANCELLED`.

Task run: `PENDING → QUEUED → RUNNING → {SUCCESS, FAILED, TIMED_OUT, CANCELLED}`; `PENDING → {SKIPPED, CANCELLED}`; `QUEUED → CANCELLED`.

`SKIPPED` task runs carry `reason`: `run_if_not_met` or `upstream_failed`.

Execution result: SUCCESS when every task is SUCCESS or SKIPPED with `run_if_not_met`. Otherwise FAILED, or TIMED_OUT when the flow timeout ended it.

`run_if`: success = all dependencies SUCCESS; failure = at least one dependency FAILED or TIMED_OUT; always = all dependencies terminal.

### 6.7 Templates

Syntax: `${{ expr }}`. Literal `${{` is written `$${{`. Expressions are lookups only:

`inputs.<id>`, `vars.<KEY>`, `secret('<KEY>')`, `tasks.<task_id>.outputs.<key>`, `trigger.<path>`, `execution.id`, `execution.namespace`, `execution.flow_id`, `execution.created_at`.

1. Strings render raw. Other JSON values render as compact JSON.
2. `vars` precedence: flow `variables` → namespace → parent namespaces → global.
3. `tasks.X.outputs` is valid only when X is a transitive dependency.
4. `secret()` is valid only in `env` values and `http` `url`, `headers`, `body`.
5. Templates are valid in: `env`, `args`, `command`, `http` fields, `subflow.inputs`, trigger `inputs`, flow `outputs`.

### 6.8 `namespace.yaml`

```yaml
description: ELT pipelines
defaults:
  executor: { type: kubernetes, pool: cluster-a, image: ghcr.io/acme/elt:1.4.0 }
  env: { TZ: Europe/Zurich }
  retry: { max_attempts: 2 }
  timeout: 1h
```

### 6.9 Emit protocol

The runner creates a file and sets `SLUICE_OUTPUTS` to its path. Tasks append JSON Lines:

```json
{"type":"output","key":"rows","value":1234}
{"type":"metric","name":"rows_loaded","value":1234,"unit":"rows","tags":{"table":"orders"}}
{"type":"artifact","path":"report.html","name":"report","content_type":"text/html"}
```

Limits: outputs ≤ 1 MiB total per task; metric name `^[a-z][a-z0-9_.]{0,99}$`; ≤ 8 tags; tag value ≤ 128 chars; ≤ 10 000 metrics per task; artifact ≤ `SLUICE_MAX_ARTIFACT_BYTES`; artifact `path` relative to the workdir. An invalid line produces a warning log line and is ignored.

### 6.10 Task environment contract

Every executor-run task receives: `SLUICE_EXECUTION_ID`, `SLUICE_TASK_ID`, `SLUICE_ATTEMPT`, `SLUICE_NAMESPACE`, `SLUICE_FLOW_ID`, `SLUICE_OUTPUTS`, `SLUICE_WORKDIR`, plus resolved `env`.

---

## 7. Requirements and scenarios

### 7.1 Core (CORE)

| ID | Requirement |
|---|---|
| REQ-CORE-001 | The binary MUST provide the commands in §4.2. `sluice version` prints version, commit and build date. |
| REQ-CORE-002 | Configuration MUST come from environment variables (Appendix A). Invalid configuration MUST stop startup with exit code 2 and list all errors. |
| REQ-CORE-003 | Migrations MUST be embedded. `server` applies them at start. Concurrent starts MUST apply each migration once, serialized with a transaction-scoped advisory lock. |
| REQ-CORE-004 | The server MUST expose `/healthz` (process alive), `/readyz` (database, migrations current, storage round trip) and `/metrics` (Prometheus: executions by state, task runs by state, queue depth, HTTP duration). |
| REQ-CORE-005 | Database access MUST satisfy C-06. |
| REQ-CORE-006 | Leases MUST use the `leases` table: TTL 15 s, renew every 5 s. Leader-only writes MUST check the holder in the same statement. Leases: `scheduler`, `maintenance`, `git-sync`, `k8s-reconcile:<pool>`. |
| REQ-CORE-007 | Each instance MUST register pools, executors, version and hostname, and heartbeat every 10 s. An instance is offline after 60 s without heartbeat. Offline rows are deleted after 24 h. |
| REQ-CORE-008 | On SIGTERM the instance MUST stop claiming, send SIGTERM to its process tasks, mark them FAILED with reason `instance_shutdown` (retry policy applies), release leases and exit within `SLUICE_SHUTDOWN_GRACE`. Docker and Kubernetes tasks continue. |
| REQ-CORE-009 | Logs MUST be structured (`log/slog`, JSON default). Each HTTP request has a request ID returned in `X-Request-Id`. |
| REQ-CORE-010 | The server MUST serve the embedded UI at `/`. Unknown non-API paths return `index.html`. Hashed assets use `Cache-Control: public, max-age=31536000, immutable`. `index.html` uses `no-cache`. |

- **SCN-CORE-001** [E] (REQ-CORE-001, REQ-CORE-002) `sluice version` prints three fields. `sluice server` without `SLUICE_DATABASE_URL` and with `SLUICE_WORKER_SLOTS=-1` exits 2 and names both variables.
- **SCN-CORE-002** [I] (REQ-CORE-003) Three instances start at the same time on an empty database. Each migration applies once. All become ready.
- **SCN-CORE-003** [E] (REQ-CORE-004) `/readyz` is 200. The storage backend stops. `/readyz` is 503 and names the `storage` check. `/healthz` stays 200. `/metrics` has the four named series.
- **SCN-CORE-004** [I] (REQ-CORE-005, REQ-CORE-006) Through PgBouncer in transaction mode: create a user, a managed flow and an execution that reaches SUCCESS; hand over the `scheduler` lease between two instances.
- **SCN-CORE-005** [I] (REQ-CORE-006) Instance A holds `scheduler`. A stops renewing. B holds the lease within 20 s. A leader-only write from A is rejected.
- **SCN-CORE-006** [E] (REQ-CORE-007) Two instances appear in `GET /api/v1/instances` with pools and executors. One stops. It shows offline after 60 s.
- **SCN-CORE-007** [E] (REQ-CORE-008) SIGTERM during a running process task. The task becomes FAILED with `instance_shutdown`. Its retry runs on the second instance. The first process exits within grace.
- **SCN-CORE-008** [U] (REQ-CORE-010, REQ-CORE-009) A deep link to `/executions/<id>` loads the page. Asset and index cache headers match. API responses carry `X-Request-Id`.

### 7.2 Authentication and audit (AUTH)

| ID | Requirement |
|---|---|
| REQ-AUTH-001 | Login MUST use email and password. Hash: argon2id (m=19 MiB, t=2, p=1). Session cookie `sluice_session`: HttpOnly, SameSite=Lax, Secure when `SLUICE_PUBLIC_URL` is https. Sliding TTL `SLUICE_SESSION_TTL`. Logout deletes the session. |
| REQ-AUTH-002 | When `users` is empty, the server MUST create an admin from `SLUICE_BOOTSTRAP_ADMIN_EMAIL` and `SLUICE_BOOTSTRAP_ADMIN_PASSWORD`. Later starts MUST NOT change users. `sluice user create` and `sluice user reset-password` work against the database. |
| REQ-AUTH-003 | Admins MUST create users with a temporary password, change roles, disable and enable users, and reset passwords. A temporary password forces a change at next login. The last enabled admin MUST NOT be disabled or demoted (409 `last_admin`). |
| REQ-AUTH-004 | Users MUST change their own password (current password required) and sign out all other sessions. |
| REQ-AUTH-005 | API tokens: format `slu_` + 43 base62 chars (256 bits). Shown once. Stored as SHA-256. Fields: name, optional expiry (≤ 365 days), role ≤ owner role, last used time. Owners and admins revoke tokens. |
| REQ-AUTH-006 | Roles are `viewer < operator < editor < admin`. Every route and AI tool MUST enforce Appendix B on the server. Default deny. |
| REQ-AUTH-007 | The system MUST write audit events for: login success and failure, logout, user changes, token create and revoke, secret and variable changes, provider changes, git source changes, namespace create and delete, file changes, flow enable and disable, execution trigger, cancel, rerun and restart, AI mutating actions, settings changes. Admins list events with filters (actor, action, target, time). Audit retention is 365 days. |
| REQ-AUTH-008 | Login MUST be rate limited in Postgres: 10 failures per email and 50 per IP in 15 minutes return 429 with `Retry-After`. |
| REQ-AUTH-009 | Disabling a user, role changes and token revocation MUST take effect within 5 s on all instances. |

- **SCN-AUTH-001** [U] (REQ-AUTH-001) Wrong password shows an error. Correct login survives reload. Logout returns to login. The old cookie then gets 401.
- **SCN-AUTH-002** [E] (REQ-AUTH-002) Empty database with bootstrap variables creates the admin. Restart with another bootstrap password does not change it. `sluice user create --role editor` creates a user.
- **SCN-AUTH-003** [U] (REQ-AUTH-003) Admin creates an editor with a temporary password. The editor must set a new password at first login.
- **SCN-AUTH-004** [E] (REQ-AUTH-003) Disabling or demoting the last admin returns 409 `last_admin`.
- **SCN-AUTH-005** [E] (REQ-AUTH-005, REQ-AUTH-006) An operator requests an editor token: 403. A viewer token reads executions and gets 403 on trigger. A revoked token gets 401. `last_used_at` updates.
- **SCN-AUTH-006** [I] (REQ-AUTH-006, SI-03) Route inventory test: every router route and every OpenAPI operation has a permission entry. Each route is called with each role and the result matches Appendix B.
- **SCN-AUTH-007** [E] (REQ-AUTH-004) A user changes password with the wrong current password: 422. With the right one: other sessions get 401, the current session stays valid.
- **SCN-AUTH-008** [E] (REQ-AUTH-008, SI-11) The 11th failed login for one email within 15 minutes returns 429 with `Retry-After`.
- **SCN-AUTH-009** [E] (REQ-AUTH-009) Admin disables a user on instance A. The user's session and token get 401 on instance B within 5 s.
- **SCN-AUTH-010** [U] (REQ-AUTH-007) Audit page shows token creation and secret update with actor. Filters by action and actor work.
- **SCN-AUTH-011** [E] (SI-06) Cookie-authenticated POST with a foreign `Origin` gets 403. Token-authenticated POST without `Origin` succeeds.
- **SCN-AUTH-012** [I] (SI-02) Database inspection: password hashes are argon2id with the stated parameters. Session IDs and tokens exist only as hashes.
- **SCN-AUTH-013** [E] (SI-12) UI and API responses carry the headers in SI-12.

### 7.3 Storage (STO)

| ID | Requirement |
|---|---|
| REQ-STO-001 | Storage MUST have one interface (put stream, get stream, stat, delete, list by prefix) with drivers `postgres` (default), `fs`, `s3`, `azblob`. Key layout: `files/sha256/<hash>`, `bundles/<manifest_hash>.tar.gz`, `logs/<execution_id>/<task_run_id>.ndjson.gz`, `artifacts/<execution_id>/<task_run_id>/<name>`. |
| REQ-STO-002 | `s3` MUST support region, endpoint override (R2, MinIO), path-style addressing, static keys or the default AWS credential chain, and a key prefix. |
| REQ-STO-003 | `azblob` MUST support account URL with `DefaultAzureCredential`, or a connection string, plus container and key prefix. |
| REQ-STO-004 | Reads and writes MUST stream. A 150 MiB object MUST NOT be held fully in memory. |
| REQ-STO-005 | File objects MUST be deduplicated by content hash. |
| REQ-STO-006 | The maintenance leader MUST delete daily: bundles unused for 7 days, file objects not referenced by any snapshot, logs and artifacts of purged executions. |

- **SCN-STO-001** [I] (REQ-STO-001, REQ-STO-004) One conformance suite runs on postgres, fs, s3 (MinIO) and azblob (Azurite): put, get, stat, overwrite, delete, missing key, prefix list, 150 MiB stream with heap growth below 64 MiB.
- **SCN-STO-002** [I] (REQ-STO-002) s3 driver with endpoint override and path-style against MinIO. With a prefix, all created keys start with it.
- **SCN-STO-003** [I] (REQ-STO-003) azblob with a connection string against Azurite. azblob with a token credential against Azurite OAuth mode through the same credential factory that selects `DefaultAzureCredential`.
- **SCN-STO-004** [I] (REQ-STO-005) The same content saved in two namespaces creates one object.
- **SCN-STO-005** [I] (REQ-STO-006) With a fake clock, maintenance deletes unreferenced objects and keeps referenced ones.

### 7.4 Namespaces and files (NS)

| ID | Requirement |
|---|---|
| REQ-NS-001 | Namespace names MUST match `^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`, ≤ 128 chars. Dots form a hierarchy. Parents exist implicitly. Source type is fixed at creation. |
| REQ-NS-002 | Managed namespaces MUST support create, update, rename, delete and upload of files. Each save of one or more changes creates a new snapshot version with author and message. |
| REQ-NS-003 | Managed namespaces MUST support version history, file view at a version, diff between versions and revert (revert creates a new version). |
| REQ-NS-004 | Git namespaces MUST reject file writes with 409 `namespace_read_only`, except the push-branch action (REQ-GIT-005). |
| REQ-NS-005 | Paths MUST be relative, UTF-8, ≤ 512 chars, without `..` segments or leading `/`. Symlinks are not stored. (SI-08) |
| REQ-NS-006 | `namespace.yaml` MUST be parsed and validated. Its defaults apply per §6.4 and §6.8. |
| REQ-NS-007 | Files with runnable extensions MUST be runnable directly with optional args. This creates an execution with one `script` task that uses namespace defaults. Requires operator. |
| REQ-NS-008 | Managed namespace delete MUST fail with 409 while executions run. Delete marks flows deleted. Execution history stays. |
| REQ-NS-009 | Limits: file ≤ `SLUICE_MAX_FILE_BYTES`, snapshot total ≤ `SLUICE_MAX_BUNDLE_BYTES`. Exceeding returns 413. |

- **SCN-NS-001** [E] (REQ-NS-001) `Data`, `a..b` and `-a` are rejected. `data.elt` is accepted. `data` appears as its parent in the tree API.
- **SCN-NS-002** [U] (REQ-NS-002) An editor creates `pipelines/load.py` and `sync.flow.yaml` and saves with a message. Version 2 shows with author and message.
- **SCN-NS-003** [U] (REQ-NS-003) Diff between versions 1 and 3 shows the changes. Revert to 1 creates version 4 with the content of version 1.
- **SCN-NS-004** [E] (REQ-NS-004) A file write to a git namespace returns 409 `namespace_read_only`.
- **SCN-NS-005** [I] (REQ-NS-005, SI-08) `../x`, `/etc/passwd`, `a/../../b` are rejected by the file API. A git repository with a symlink and a traversal path syncs without those entries and records warnings. Bundle extraction rejects crafted tar entries outside the root.
- **SCN-NS-006** [E] (REQ-NS-006) `namespace.yaml` defaults for executor and env apply to a flow without its own. Flow values override them.
- **SCN-NS-007** [U] (REQ-NS-007) An operator runs `hello.py` with args. The execution reaches SUCCESS. Logs show the args.
- **SCN-NS-008** [E] (REQ-NS-008) Delete during a running execution returns 409. After completion delete succeeds. The execution stays visible.
- **SCN-NS-009** [E] (REQ-NS-009) Upload above the file limit returns 413. A save above the snapshot limit returns 413.

### 7.5 Flows (FLOW)

| ID | Requirement |
|---|---|
| REQ-FLOW-001 | Flow discovery MUST follow §6.1. Duplicate flow `id` in a namespace makes all duplicates invalid. |
| REQ-FLOW-002 | Structural validation MUST follow §6.3–§6.8 and report error code, YAML path, line and column. The JSON Schema MUST be generated from Go types into `schemas/flow.schema.json` and served at `GET /api/v1/schemas/flow.json`. |
| REQ-FLOW-003 | Semantic validation MUST check: unique task IDs, known dependencies, no cycles, referenced files exist in the snapshot, template references valid (§6.7), `secret()` placement, executor fields per type, cron and timezone, `subflow` and `flow` trigger reference format, input types and defaults. |
| REQ-FLOW-004 | An invalid revision MUST make the flow invalid: triggers inactive, manual trigger 422 `flow_invalid`, errors shown. |
| REQ-FLOW-005 | A new revision MUST be created when the flow source hash changes. Revisions are listed and diffed. |
| REQ-FLOW-006 | Flows MUST be enabled or disabled in the database. Disabled flows do not fire schedule, webhook (409 `flow_disabled`) or flow triggers. Manual trigger works. |
| REQ-FLOW-007 | `sluice validate <dir> [--json]` MUST validate a namespace directory offline and exit 0 (valid) or 1 (invalid). `--json` output MUST match `schemas/validate-result.schema.json`. |
| REQ-FLOW-008 | Inputs MUST be validated at trigger time against their types. The UI MUST render a run form from inputs. |

- **SCN-FLOW-001** [I] (REQ-FLOW-001, REQ-FLOW-002) Every fixture in `tests/fixtures/flows/valid/` parses to its expected model.
- **SCN-FLOW-002** [I] (REQ-FLOW-001, REQ-FLOW-002, REQ-FLOW-003) Every fixture in `tests/fixtures/flows/invalid/` returns its expected error code, path and line. Fixtures cover: cycle, unknown dependency, duplicate task ID, duplicate flow ID, bad cron, unknown timezone, missing file, `secret()` in `args`, output reference to a non-dependency, executor on an `http` task, bad input default.
- **SCN-FLOW-003** [E] (REQ-FLOW-004) Saving an invalid flow lists it as invalid with errors. Manual trigger returns 422 `flow_invalid`. Its schedule does not fire.
- **SCN-FLOW-004** [U] (REQ-FLOW-005) Two saves of a flow show two revisions and a diff.
- **SCN-FLOW-005** [E] (REQ-FLOW-006) A disabled flow does not fire its schedule. Its webhook returns 409 `flow_disabled`. Manual trigger works.
- **SCN-FLOW-006** [E] (REQ-FLOW-007) `sluice validate examples/elt/namespace` exits 0. An invalid fixture exits 1. `--json` output matches `schemas/validate-result.schema.json`.
- **SCN-FLOW-007** [I] (REQ-FLOW-002) The generated schema equals the committed file, accepts all valid fixtures and rejects structural invalid fixtures.
- **SCN-FLOW-008** [U] (REQ-FLOW-008) The run form renders string, int, boolean, select and json inputs. A missing required input shows an inline error. A valid submit starts an execution with typed inputs.

### 7.6 Execution engine (EXE)

| ID | Requirement |
|---|---|
| REQ-EXE-001 | An execution MUST pin its flow revision and snapshot at creation. Later changes MUST NOT affect it. |
| REQ-EXE-002 | State changes MUST go through one transition function that enforces §6.6. The database MUST reject unknown state values. |
| REQ-EXE-003 | Ready tasks MUST be queued in parallel, limited by `max_parallel`. |
| REQ-EXE-004 | `run_if` MUST follow §6.6. |
| REQ-EXE-005 | Retries MUST create a new task run attempt after the backoff delay. Execution outputs use the final attempt. |
| REQ-EXE-006 | Task and flow timeouts MUST stop the work and set TIMED_OUT. |
| REQ-EXE-007 | Concurrency MUST follow the flow `concurrency` block. `queue` holds executions in QUEUED. `skip` sets new executions to SKIPPED. |
| REQ-EXE-008 | Cancel MUST set CANCELLING, stop running tasks through their executor, cancel non-started tasks and end in CANCELLED. |
| REQ-EXE-009 | Rerun MUST create a new execution with the same snapshot and inputs. Restart from failed MUST create a new execution where SUCCESS tasks are reused (outputs copied, `reused_from_id` set) and other tasks run. |
| REQ-EXE-010 | Claims MUST use `FOR UPDATE SKIP LOCKED` filtered by instance pools, executor types and free slots (`SLUICE_WORKER_SLOTS` for process and docker, `SLUICE_K8S_MAX_JOBS` per pool for kubernetes). |
| REQ-EXE-011 | A running task run without heartbeat for `SLUICE_HEARTBEAT_TIMEOUT` MUST be checked with its executor. If the work is gone, the attempt becomes FAILED with reason `lost` and retry applies. |
| REQ-EXE-012 | Templates MUST resolve at dispatch. A resolution error MUST fail the task with reason `template_error` before any process starts. |
| REQ-EXE-013 | Flow outputs MUST resolve at SUCCESS. A resolution error sets FAILED with reason `output_error`. |
| REQ-EXE-014 | The maintenance leader MUST delete executions that ended more than `SLUICE_RETENTION_DAYS` ago, with task runs, logs, metrics and artifacts. |
| REQ-EXE-015 | Executions MUST carry flow labels merged with trigger labels. The API and UI filter by label. |
| REQ-EXE-016 | `subflow` MUST link child and parent. Cancelling the parent cancels the child. Depth > 10 fails with reason `depth_exceeded`. |
| REQ-EXE-017 | `http` tasks MUST run inline with the fields in §6.4 and fail on unexpected status. |

- **SCN-EXE-001** [E] (REQ-EXE-001) A long execution runs. The flow file changes. The running execution finishes with the old snapshot. Its bundle hash equals the pinned manifest.
- **SCN-EXE-002** [I] (REQ-EXE-002) Table test over all state pairs: allowed transitions succeed, all others return an error. A direct insert with an unknown state fails.
- **SCN-EXE-003** [E] (REQ-EXE-003) Diamond A→(B,C)→D: B and C overlap in time, D starts after both end. With `max_parallel: 1` no tasks overlap.
- **SCN-EXE-004** [E] (REQ-EXE-004) A fails. B (`success`) is SKIPPED `upstream_failed`. C (`failure`) runs. D (`always`) runs. Execution is FAILED.
- **SCN-EXE-005** [E] (REQ-EXE-005) A task fails twice and then succeeds with `max_attempts: 3`, exponential, initial 1 s. Three attempts exist. Gaps are ≥ 1 s and ≥ 2 s. Execution is SUCCESS with outputs of attempt 3.
- **SCN-EXE-006** [E] (REQ-EXE-006) `sleep 60` with task timeout 2 s is TIMED_OUT within 5 s and the process is gone. A flow timeout of 3 s ends the execution TIMED_OUT.
- **SCN-EXE-007** [E] (REQ-EXE-007) `limit: 1, queue`: three triggers run one after another. `limit: 1, skip`: the second is SKIPPED.
- **SCN-EXE-008** [U] (REQ-EXE-008) Cancel from the UI: running task CANCELLED within 10 s, pending tasks CANCELLED, execution CANCELLED.
- **SCN-EXE-009** [U] (REQ-EXE-009) Restart from failed reuses SUCCESS tasks (marked reused) and runs the failed task and its downstream. Rerun runs all tasks with the same inputs and snapshot.
- **SCN-EXE-010** [I] (REQ-EXE-010) Two instances with 4 slots each and 100 queued tasks: no task is claimed twice, at most 8 run at once, tasks for another pool are never claimed.
- **SCN-EXE-011** [E] (REQ-EXE-011) SIGKILL of the runner process. After the heartbeat timeout the attempt is FAILED `lost` and the retry reaches SUCCESS.
- **SCN-EXE-012** [E] (REQ-EXE-012) Inputs, vars precedence, trigger, execution and task outputs resolve in args and env. A reference to a missing output fails the task with `template_error` and no process starts.
- **SCN-EXE-013** [E] (REQ-EXE-013, REQ-EXE-016) Flow outputs show on the execution. A parent `subflow` task receives the child outputs.
- **SCN-EXE-014** [I] (REQ-EXE-014) With a fake clock, old executions and their logs, metrics and artifacts are deleted. Newer ones stay.
- **SCN-EXE-015** [E] (REQ-EXE-015) Executions filter by label from flow and from trigger.
- **SCN-EXE-016** [E] (REQ-EXE-016) Cancelling a parent cancels the running child. A self-calling subflow fails at depth 11 with `depth_exceeded`.
- **SCN-EXE-017** [E] (REQ-EXE-017) An `http` task against a local test server sends resolved headers, parses a JSON body into outputs and fails when the status is not expected.

### 7.7 Triggers (TRG)

| ID | Requirement |
|---|---|
| REQ-TRG-001 | Manual trigger MUST accept inputs and labels through API and UI. |
| REQ-TRG-002 | Schedules MUST follow §6.5 with DST-correct timezone handling. `catch_up: last` fires only the latest missed time. `none` fires no missed time. |
| REQ-TRG-003 | Each schedule time MUST produce at most one execution across all instances (D-14). |
| REQ-TRG-004 | Webhooks: `POST /hooks/<key>`. Key = 256 random bits, stored hashed, shown once to editors, rotatable. Body ≤ 1 MiB. Wrong key returns 404. Success returns 202 with the execution ID. |
| REQ-TRG-005 | Flow triggers MUST fire when the upstream execution ends in a listed state and pass `trigger.execution_id`, `trigger.state`, `trigger.outputs`. `chain_depth` > 10 MUST NOT fire and MUST write an audit event. |
| REQ-TRG-006 | `GET /api/v1/schedules/upcoming` MUST list next fire times of active schedules, sorted. |

- **SCN-TRG-001** [E] (REQ-TRG-001) Manual trigger through the API with inputs and labels returns 201 and the execution shows both.
- **SCN-TRG-002** [I] (REQ-TRG-002) Fake clock: `30 2 * * *` in Europe/Zurich over the spring and autumn DST dates fires once per day at the correct instant.
- **SCN-TRG-003** [I] (REQ-TRG-002) Downtime over three fire times: `last` creates one execution for the latest time, `none` creates none.
- **SCN-TRG-004** [I] (REQ-TRG-003) Three instances with forced leader changes every 2 fake seconds over 60 fake minutes of `* * * * *` create exactly 60 executions.
- **SCN-TRG-005** [E] (REQ-TRG-004, SI-05) Valid key returns 202. Wrong key returns 404. After rotation the old key returns 404. A 2 MiB body returns 413. `trigger.body` is available to the flow.
- **SCN-TRG-006** [E] (REQ-TRG-005) A downstream flow fires on upstream FAILED and receives `trigger.execution_id`. It does not fire on SUCCESS when not listed. A→B→A stops at depth 10 with an audit event.
- **SCN-TRG-007** [E] (REQ-TRG-006) Upcoming schedules list the next times sorted and omit disabled flows.

### 7.8 Runner protocol (RUN)

| ID | Requirement |
|---|---|
| REQ-RUN-001 | `sluice exec` MUST: get the spec, download and extract the bundle into the workdir, create the outputs file, start the command in its own process group, stream logs and events, heartbeat, upload artifacts, post `complete` and exit with the child exit code. |
| REQ-RUN-002 | Logs MUST be sent in batches (≤ 500 ms or 256 KiB) with increasing `seq`. Ingest is idempotent per `(task_run_id, seq)`. Each line has timestamp and stream (stdout, stderr, system). Lines > 16 KiB are truncated with a marker. |
| REQ-RUN-003 | The runner MUST read the outputs file per §6.9 and send outputs, metrics and artifacts. |
| REQ-RUN-004 | Heartbeat every 10 s. A heartbeat response with `cancel: true` MUST send SIGTERM to the process group and SIGKILL after 10 s. |
| REQ-RUN-005 | The runner MUST mask secret values in log lines, outputs and error text before sending. The server MUST mask again before storage (SI-10). |
| REQ-RUN-006 | When the API is unreachable, the runner MUST retry with backoff for up to 5 minutes and keep up to 64 MiB of pending data. Beyond that it drops the oldest lines and sends a marker with the dropped count. |
| REQ-RUN-007 | Missing runtime tools MUST fail the task with reason `runtime_not_found` and name the tool. |
| REQ-RUN-008 | Logs MUST stream live through SSE. At terminal state, chunks MUST be archived to storage and removed from Postgres. The UI and API read both sources through one endpoint. Full logs download as a file. |
| REQ-RUN-009 | Tasks MUST receive the environment in §6.10. |
| REQ-RUN-010 | Run tokens MUST follow SI-04. |

- **SCN-RUN-001** [E] (REQ-RUN-001, REQ-RUN-002) A script writes 10 000 interleaved stdout and stderr lines. All lines are stored in order with stream tags and no `seq` gaps.
- **SCN-RUN-002** [E] (REQ-RUN-002) A 100 KiB line is stored truncated to 16 KiB with a marker.
- **SCN-RUN-003** [E] (REQ-RUN-003) A script emits outputs, a tagged metric and an artifact. All show in the API. An invalid JSON line creates a warning line and the task is SUCCESS.
- **SCN-RUN-004** [E] (REQ-RUN-004) On cancel, a child with a SIGTERM trap writes its trap line. A child that ignores SIGTERM is killed after 10 s.
- **SCN-RUN-005** [E] (REQ-RUN-005, SI-10) A script prints a secret raw, base64 standard, base64 URL, URL-encoded and JSON-escaped, and writes it to an output and to its error text. Every stored form shows `***`.
- **SCN-RUN-006** [E] (REQ-RUN-006) A proxy blocks the API for 30 s during a run. After recovery no line is lost.
- **SCN-RUN-007** [E] (REQ-RUN-007) In `sluice-uv`, python, bash and bun scripts reach SUCCESS. In `sluice` (no bun), a bun script fails with `runtime_not_found` naming `bun`.
- **SCN-RUN-008** [U] (REQ-RUN-008) Live tail shows new lines within 2 s. After completion the page loads the archive and Postgres has no chunks for the run. Search and task filter work. The download matches the stored lines.
- **SCN-RUN-009** [E] (REQ-RUN-009) A script prints its environment. All §6.10 variables exist with correct values.
- **SCN-RUN-010** [E] (REQ-RUN-010, SI-04) A token after completion gets 401. Task A's token on task B gets 403. An expired token gets 401.

### 7.9 Executors (EXR)

| ID | Requirement |
|---|---|
| REQ-EXR-001 | Executors MUST implement one interface: start, wait, cancel, status by external reference. Types: inline, process, docker, kubernetes. |
| REQ-EXR-002 | With `SLUICE_EXECUTORS=auto` the instance MUST enable: inline and process always; docker when the Docker API answers; kubernetes when in-cluster config or `SLUICE_K8S_KUBECONFIG` works and a Job create dry run is allowed. An explicit list overrides detection. The instance registers the result. |
| REQ-EXR-003 | `process` MUST start `sluice exec` from `os.Executable()` with loopback `SLUICE_API_URL`, and cancel the whole process group. |
| REQ-EXR-004 | `docker` MUST use the Engine API from `DOCKER_HOST`. It creates the task container without volumes, copies the runner binary from `SLUICE_RUNNER_IMAGE` into it through the API, applies pull policy, network, limits and labels, and removes the container after completion unless `SLUICE_DOCKER_KEEP_CONTAINERS=true`. Image pull failure sets reason `image_pull_failed`. |
| REQ-EXR-005 | `kubernetes` MUST create one Job per attempt: `backoffLimit: 0`, `restartPolicy: Never`, `activeDeadlineSeconds` from timeout, `ttlSecondsAfterFinished` from `SLUICE_K8S_JOB_TTL`, labels `sluice.dev/execution-id`, `sluice.dev/task-run-id`, `sluice.dev/pool`; an init container from `SLUICE_RUNNER_IMAGE` runs `sluice runner-install /sluice-bin` into an `emptyDir`; workdir is a second `emptyDir`; resources and `kubernetes` fields per §6.4. The Job spec MUST contain no secret values. |
| REQ-EXR-006 | The `k8s-reconcile:<pool>` leader MUST run every 60 s: delete Jobs without an active task run; mark task runs FAILED `lost` when their Job is gone; fail pods pending longer than `SLUICE_K8S_PENDING_TIMEOUT` with `pod_pending_timeout`; fail image pull errors with `image_pull_failed`. |
| REQ-EXR-007 | `inject_runner: false` MUST run `sluice exec` from the image `PATH` with no init container or copy step. |
| REQ-EXR-008 | A queued task with no online instance for its pool and executor MUST show reason `no_instance_for_pool`. |
| REQ-EXR-009 | Cancel on docker stops and removes the container. Cancel on kubernetes deletes the Job with background propagation. |

- **SCN-EXR-001** [I] (REQ-EXR-001, REQ-EXR-002) Detection without Docker or cluster enables inline and process. With a Docker daemon it adds docker. With a kubeconfig to kind it adds kubernetes. `SLUICE_EXECUTORS=process` enables only inline and process.
- **SCN-EXR-002** [E] (REQ-EXR-003) A process task spawns a grandchild. Cancel removes both processes.
- **SCN-EXR-003** [E] (REQ-EXR-004) A task in `python:3.12-slim` with injection reaches SUCCESS with logs and outputs. `docker inspect` during the run shows no mounts. The container is gone after completion.
- **SCN-EXR-004** [E] (REQ-EXR-004, REQ-EXR-007, REQ-EXR-009) A task in `sluice-uv` with `inject_runner: false` succeeds. A missing image fails with `image_pull_failed`. Cancel removes the container.
- **SCN-EXR-005** [K] (REQ-EXR-005, SI-01) A kubernetes task reaches SUCCESS with logs and outputs. The Job has the required fields, labels and only `emptyDir` volumes. The Job and Pod JSON do not contain the secret canary.
- **SCN-EXR-006** [K] (REQ-EXR-009) Cancel deletes the Job. The Pod is gone within 30 s.
- **SCN-EXR-007** [K] (REQ-EXR-006) The server pod restarts during a run and the task still reaches SUCCESS. An orphan Job is deleted. A Job deleted by hand makes the attempt FAILED `lost` and the retry succeeds. An unschedulable pod fails with `pod_pending_timeout` (timeout 20 s in test). A bad image fails with `image_pull_failed`.
- **SCN-EXR-008** [E] (REQ-EXR-008) A task for pool `gpu` stays QUEUED with `no_instance_for_pool`. An instance with pool `gpu` starts and the task runs.

### 7.10 Secrets and variables (SEC)

| ID | Requirement |
|---|---|
| REQ-SEC-001 | Providers MUST be: `builtin` and `env` (always present), `kubernetes` (ref `secret-name/key`, namespace in config), `azure_key_vault` (ref `name` or `name/version`, vault URL in config, `DefaultAzureCredential`), `vault` (KV v2, ref `path#field`, mount in config, auth by `SLUICE_VAULT_TOKEN` or Kubernetes auth role). Admins manage provider configuration. Configuration holds no credentials. |
| REQ-SEC-002 | Secrets MUST be created, updated and deleted per scope (global or namespace). Values are write-only. `builtin` values use AES-256-GCM with key ID and associated data `scope/key`. External secrets store only the reference. |
| REQ-SEC-003 | Resolution MUST search the task namespace, then each parent, then global. Nearest match wins. |
| REQ-SEC-004 | A check action MUST report `ok`, `not_found`, `access_denied` or `provider_error` without the value. |
| REQ-SEC-005 | External values MUST be cached for at most `SLUICE_SECRET_CACHE_TTL` (default 60 s). Changes apply to later dispatches without restart. |
| REQ-SEC-006 | `SLUICE_MASTER_KEYS` MUST hold `kid:base64key` pairs. The first is active. `sluice secrets rekey` re-encrypts all builtin secrets with the active key. `/readyz` fails with `master_key_missing` when a stored key ID has no key. |
| REQ-SEC-007 | Variables MUST be created, updated and deleted per scope, readable by viewers, inherited like secrets, and audited. |
| REQ-SEC-008 | Executions MUST record the secret keys they used, never values. |
| REQ-SEC-009 | A missing secret at dispatch MUST fail the task with reason `secret_not_found`, naming the key and the scopes searched. |
| REQ-SEC-010 | Without master keys, builtin secret writes MUST return 409 `builtin_provider_disabled`. Other providers work. (SI-09) |

- **SCN-SEC-001** [I] (REQ-SEC-001) Provider conformance (resolve, not found, access denied where the provider supports it) for builtin, env, azure_key_vault (lowkey-vault) and vault (dev server, token auth).
- **SCN-SEC-002** [U] (REQ-SEC-002, REQ-AUTH-007) An editor creates a builtin namespace secret. The UI never shows the value. The API response has no value field. Update works. Audit events exist.
- **SCN-SEC-003** [E] (REQ-SEC-003) Key `PG_URL` exists globally, in `data` and in `data.elt`. A flow in `data.elt.x` gets the `data.elt` value. After deleting that, it gets the `data` value.
- **SCN-SEC-004** [E] (REQ-SEC-004) Check returns `ok` for an existing Vault ref and `not_found` for a missing one. No value is in the response.
- **SCN-SEC-005** [E] (REQ-SEC-005) A Vault value changes. With cache TTL 1 s, the next execution after 2 s gets the new value. No restart happens.
- **SCN-SEC-006** [E] (REQ-SEC-006) Add a new key first, run `rekey`, remove the old key, restart: secrets resolve. Remove the old key before `rekey`: `/readyz` fails with `master_key_missing`.
- **SCN-SEC-007** [U] (REQ-SEC-007) Variables created at global and namespace scope show inherited values. A flow uses `vars.X` and logs the expected value.
- **SCN-SEC-008** [E] (REQ-SEC-008) The execution API lists `secret_keys_used` without values.
- **SCN-SEC-009** [E] (REQ-SEC-009) A missing secret fails the task with `secret_not_found` and the scope list.
- **SCN-SEC-010** [E] (SI-01) A canary secret is used by process and docker tasks, an `http` header and an AI triage. After the runs, a text `pg_dump`, all storage objects, `docker inspect` output and all recorded LLM requests do not contain the canary.
- **SCN-SEC-011** [E] (REQ-SEC-010, SI-09) Without master keys, a builtin secret write returns 409 `builtin_provider_disabled`. An env secret resolves.
- **SCN-SEC-012** [K] (REQ-SEC-001) In kind: the kubernetes provider resolves a Secret key. Vault Kubernetes auth resolves a value.

### 7.11 Git (GIT)

| ID | Requirement |
|---|---|
| REQ-GIT-001 | A git source MUST have: repo URL (https or ssh), branch, auth (none, https token, ssh key with optional known_hosts), credentials as global secret keys, mappings `repo_path → namespace`, poll interval (default 60 s, minimum 15 s), optional webhook secret key. |
| REQ-GIT-002 | Sync MUST fetch the branch head into a temporary directory, build a manifest per mapping, create a snapshot only when the manifest changed, move the namespace head, refresh flows and triggers, record a `git_sync_runs` row and delete the temporary directory. A failed sync MUST keep the previous snapshot active. |
| REQ-GIT-003 | `POST /hooks/git/<source_id>` MUST verify GitHub `X-Hub-Signature-256` HMAC or `X-Sluice-Token`. Pushes to other branches do nothing. |
| REQ-GIT-004 | Editors MUST trigger "Sync now". The UI shows the last 50 sync runs. |
| REQ-GIT-005 | Editing files of a git namespace in the UI or through AI MUST commit to a new branch `sluice/<user-slug>/<yyyymmdd-hhmmss>` from the last synced SHA with the user as author, push it and return the branch name. The tracked branch MUST NOT change. |
| REQ-GIT-006 | A flow file removed in git MUST mark the flow deleted and its triggers inactive. History stays. |
| REQ-GIT-007 | A mapping to a managed namespace or to a namespace mapped by another source MUST return 409. |

- **SCN-GIT-001** [E] (REQ-GIT-001, REQ-GIT-002) An https-token source maps `pipelines/elt` to `data.elt`. Sync creates a snapshot with the SHA. Flows appear.
- **SCN-GIT-002** [E] (REQ-GIT-001, REQ-GIT-002) An ssh-key source syncs. A wrong key records an error and the previous snapshot stays active.
- **SCN-GIT-003** [E] (REQ-GIT-002) A new commit changes one mapping. Polling creates a snapshot for that mapping only.
- **SCN-GIT-004** [E] (REQ-GIT-003, SI-05) A valid HMAC webhook syncs within 5 s. An invalid signature returns 401. A push to another branch does not sync.
- **SCN-GIT-005** [U] (REQ-GIT-004, REQ-GIT-005) An editor edits a file in a git namespace and pushes. The remote has the new branch with the editor as author. The tracked branch is unchanged. "Sync now" adds a sync run to the list.
- **SCN-GIT-006** [E] (REQ-GIT-006) Removing a flow file and syncing marks the flow deleted with triggers inactive. Old executions stay visible.
- **SCN-GIT-007** [E] (REQ-GIT-007) Both conflicting mappings return 409.
- **SCN-GIT-008** [I] (REQ-GIT-002) After sync the temporary root is empty. Sync run history keeps records per source.

### 7.12 API (API)

| ID | Requirement |
|---|---|
| REQ-API-001 | `api/openapi.yaml` MUST define all `/api/v1` and `/api/runner/v1` operations. Generated Go and TS code MUST match the spec (`just gen-check`). |
| REQ-API-002 | Errors MUST use `{"error":{"code","message","details"}}`. Validation errors use 422 `validation_failed`. Unknown API routes return JSON 404. |
| REQ-API-003 | List endpoints MUST use cursor pagination, `limit` ≤ 200. |
| REQ-API-004 | SSE endpoints for execution events and logs MUST support `Last-Event-ID` resume. |

- **SCN-API-001** [I] (REQ-API-001) Regenerating Go and TS code gives no diff.
- **SCN-API-002** [E] (REQ-API-002) An invalid body returns 422 with `validation_failed` and field details. An unknown API route returns JSON 404.
- **SCN-API-003** [E] (REQ-API-003) 450 executions page with limit 200 into 3 pages while new executions arrive. No row repeats or is missing.
- **SCN-API-004** [E] (REQ-API-004) A log stream reconnects with `Last-Event-ID` and gets no duplicate or missing lines.

### 7.13 UI (UI)

| ID | Requirement |
|---|---|
| REQ-UI-001 | Stack: Vite, React 19, TypeScript strict, Bun, TanStack Router (file routes), TanStack Query, TanStack Table, TanStack Virtual, shadcn/ui, Tailwind CSS v4, shadcn charts (Recharts), CodeMirror 6, generated API client. |
| REQ-UI-002 | Routes MUST follow Appendix E. Actions not allowed for the role MUST be hidden. |
| REQ-UI-003 | Dashboard (range 24 h, 7 d, 30 d; namespace filter): KPI cards (executions, success rate, failed, median duration, running now), stacked bar of executions per bucket by terminal state, line of p50 and p95 duration, running now table, recent failures table with triage summary, next 10 schedules. |
| REQ-UI-004 | Executions list: filters (state, namespace, flow, trigger type, label, time range), sort (created, duration), server pagination, filter state in URL. |
| REQ-UI-005 | Execution detail: header (state, duration, trigger, snapshot version or git SHA, inputs, labels), Gantt of task attempts, tabs Logs, Outputs, Metrics, Artifacts, AI triage; actions cancel, rerun, restart from failed. |
| REQ-UI-006 | Flows list with state of last run. Flow detail: strip of last 50 execution states, duration chart, custom metric chart (metric name, aggregation sum/avg/max, group by one tag key), triggers with next fire time and webhook URL rotation, source view, revisions, run form, enable toggle. |
| REQ-UI-007 | Namespaces: tree, file browser, CodeMirror editor with YAML, Python, shell, TS and SQL highlighting, inline flow validation, versions and diff (managed), source info and push to branch (git), run file. |
| REQ-UI-008 | Secrets and variables pages per scope with inheritance view. |
| REQ-UI-009 | Settings: profile, users, API tokens, git sources, secret providers, storage status, AI provider, instances with pools and executors, audit log. |
| REQ-UI-010 | Visual system (below). Light, dark and system themes. Sentence case. Empty, loading and error states on every data view. |
| REQ-UI-011 | Main routes MUST have no serious or critical axe violations in both themes. |
| REQ-UI-012 | Execution list and detail MUST reflect state changes within 2 s without reload. |
| REQ-UI-013 | Dashboard, executions list and execution detail MUST have no page-level horizontal scroll at 390 px width. |

Visual system (REQ-UI-010):

| Token | Value |
|---|---|
| Base light | `#FAFAF9` background, `#1C1917` text, `#E7E5E4` borders |
| Base dark | `#161A1D` background, `#E6E8EA` text, `#2A3036` borders |
| Accent | `#2F6FEB` (actions, focus ring, selected state) |
| States | success `#1F9D55`, failed `#D64545`, running `#2F6FEB`, queued `#8A94A6`, cancelled `#6B7280`, timed out `#C27C0E`, skipped `#A8A29E` |
| Type | IBM Plex Sans for UI. JetBrains Mono only in logs, code and the editor. |
| Density | Tables 36 px rows. Radius 6 px on inputs and buttons, 8 px on panels. No decorative gradients or shadows. |
| Motion | Only on user action. Respect `prefers-reduced-motion`. |

State color is never the only signal: each state has an icon and a text label.

- **SCN-UI-001** [U] (REQ-UI-001, REQ-UI-003) With seeded data, KPI values equal API aggregates. The stacked chart bucket totals match. Changing the range changes the query.
- **SCN-UI-002** [U] (REQ-UI-004) Filters, sort and pagination work. Reload keeps filters from the URL.
- **SCN-UI-003** [U] (REQ-UI-005) The Gantt shows attempts with durations. Outputs, metrics and artifact download work.
- **SCN-UI-004** [U] (REQ-UI-006) The state strip shows 50 states. The duration chart renders. A custom metric grouped by `table` shows one series per table.
- **SCN-UI-005** [U] (REQ-UI-002) A viewer sees no run, edit or cancel actions. An operator sees run and cancel. An editor sees edit.
- **SCN-UI-006** [U] (REQ-UI-009) Instances show pools and executors. Storage shows driver and health. Git source create works. Provider create and check work. AI provider test works.
- **SCN-UI-007** [U] (REQ-UI-011, REQ-UI-010) axe passes on dashboard, executions, execution detail, flows, flow detail, namespace files, secrets and settings in light and dark themes. Theme choice persists after reload.
- **SCN-UI-008** [U] (REQ-UI-012) A state change shows on list and detail within 2 s.
- **SCN-UI-009** [U] (REQ-UI-013) At 390 px the three pages have no page-level horizontal scroll. Logs scroll inside their panel.
- **SCN-UI-010** [U] (REQ-UI-007, REQ-UI-008) An editor types an invalid flow. The error line marker shows within 1 s after typing stops. The secrets page shows an inherited key with its source scope.

### 7.14 AI (AI)

| ID | Requirement |
|---|---|
| REQ-AI-001 | Admins MUST configure one provider: type (anthropic, openai_compatible), base URL, model, API key as a global secret key, and test it. |
| REQ-AI-002 | Without a provider, AI endpoints MUST return 409 `ai_disabled` and the UI MUST hide AI actions. MCP read tools still work. |
| REQ-AI-003 | One tool registry (Appendix D) MUST serve the assistant and MCP. Each tool has a permission and a mutating flag. Tool results MUST be masked. |
| REQ-AI-004 | `/mcp` MUST serve MCP over streamable HTTP with bearer API tokens. Mutating tools run directly when the token role allows. |
| REQ-AI-005 | The assistant panel MUST stream responses, show tool calls, persist conversations per user and stop after 20 tool steps per turn with `step_limit_reached`. Mutating tool calls MUST wait for user confirm or reject. |
| REQ-AI-006 | Flow authoring MUST validate proposed files with the flow validator, return errors to the model (≤ 3 retries), show a diff, and apply by creating a managed version or pushing a git branch. |
| REQ-AI-007 | Triage MUST run on demand, and automatically on FAILED or TIMED_OUT when `ai.auto_triage` is on. Context: flow source, failed task spec, first 50 and last 400 masked log lines, error, exit code, outputs, metrics, file diff against the last SUCCESS execution of the flow (≤ 200 lines), last 10 durations. Result schema: summary, probable_cause, evidence (task, line, text), suggested_fix, confidence (low, medium, high). Evidence lines not found in the logs MUST be removed. |
| REQ-AI-008 | Model context MUST stay within `SLUICE_AI_MAX_CONTEXT_CHARS`. Truncation keeps log head and tail. |
| REQ-AI-009 | Provider calls MUST retry 429 and 5xx with backoff (3 attempts) and show a clear error after that. |

- **SCN-AI-001** [E] (REQ-AI-002) Without a provider, AI endpoints return 409 `ai_disabled`, the UI hides AI actions and MCP `list_flows` works.
- **SCN-AI-002** [I] (REQ-AI-001, REQ-AI-009) Both adapters against scripted servers: streaming text, tool call round trip, structured JSON output, 429 then success, persistent 500 gives an error.
- **SCN-AI-003** [E] (REQ-AI-003, REQ-AI-004) An MCP client lists tools. A viewer token calling `trigger_execution` gets a permission error. An editor token `apply_change` on a managed namespace creates a version. A log tool result is masked.
- **SCN-AI-004** [U] (REQ-AI-005) The scripted model calls `get_execution` and `get_logs`. The panel shows both calls and the answer. After reload the conversation is there.
- **SCN-AI-005** [U] (REQ-AI-005, SI-07) A `trigger_execution` call shows confirm and reject. Reject does not run it and the model receives the rejection. Confirm creates the execution and an audit event.
- **SCN-AI-006** [U] (REQ-AI-006) The scripted model first proposes an invalid flow, receives errors, then proposes a valid one. The diff shows. Apply on managed creates a version. Apply on git pushes a branch.
- **SCN-AI-007** [E] (REQ-AI-007) With auto triage on, a failed execution gets an insight with all fields. An evidence line not in the logs is removed. The recorded request contains the file diff against the last success.
- **SCN-AI-008** [E] (REQ-AI-008) With a 5 000-char limit and 50 000 log lines, the recorded request is within the limit and has head and tail lines.
- **SCN-AI-009** [E] (REQ-AI-005) A scripted model that loops on tools stops after 20 steps with `step_limit_reached`.

### 7.15 Deployment (DEP)

| ID | Requirement |
|---|---|
| REQ-DEP-001 | Two images MUST build from `deploy/docker`: `sluice` (distroless, non-root UID 65532) and `sluice-uv` (Debian slim, non-root, with bash, uv, Python managed by uv, bun). Both hold the binary at `/usr/local/bin/sluice`. |
| REQ-DEP-002 | `deploy/helm/sluice` MUST install: Deployment (replicas value), Service, ServiceAccount, Role and RoleBinding (jobs: create, get, list, watch, delete; pods: get, list, watch; pods/log: get; secrets: get when the kubernetes provider is enabled), optional Ingress. No PVC. `readOnlyRootFilesystem: true` with `emptyDir` at `/tmp`. Values for database URL secret, master keys secret, storage, pools, executors, internal URL. |
| REQ-DEP-003 | Single-container mode MUST work with only environment variables and no volumes (C-07). |
| REQ-DEP-004 | Instances in different clusters on one database MUST cooperate through pools and leases with no extra configuration. |
| REQ-DEP-005 | `deploy/compose` MUST start Postgres and Sluice for local use. |

- **SCN-DEP-001** [E] (REQ-DEP-001) Both images build. Both run as non-root. `sluice version` works in both.
- **SCN-DEP-002** [K] (REQ-DEP-002) Helm installs into kind with 2 replicas. The namespace has no PVC. Pods have a read-only root filesystem. `/readyz` is 200. `helm lint` passes.
- **SCN-DEP-003** [E] (REQ-DEP-003) `docker run` of `sluice-uv` with only env vars runs a uv flow to SUCCESS. After a container restart, history and logs are intact.
- **SCN-DEP-004** [I] (REQ-DEP-004) Instances with pools `cluster-a` and `cluster-b` on one database: tasks run only on their pool, schedules fire once, and both instances list all executions.
- **SCN-DEP-005** [E] (REQ-DEP-005) `docker compose up` gives `/readyz` 200 within 60 s.

### 7.16 Example project (EX)

| ID | Requirement |
|---|---|
| REQ-EX-001 | `examples/elt/namespace` MUST contain a flow with task `extract` (dlt `sql_database` source from Postgres schema `source` to Postgres destination schema `raw`, emits `rows_loaded` tagged by table) and task `transform` (SQLMesh project with Postgres gateway and Postgres state, emits `sqlmesh_run_seconds`). |
| REQ-EX-002 | The example MUST run on the process executor in `sluice-uv` and on the kubernetes executor with image `sluice-uv`. |

- **SCN-EX-001** [E] (REQ-EX-001, REQ-EX-002) In single-container mode the flow succeeds. `rows_loaded` per table equals the source counts. The SQLMesh model table has the expected rows.
- **SCN-EX-002** [K] (REQ-EX-002) The same flow succeeds with the kubernetes executor.

### 7.17 Reference docs (DOC)

| ID | Requirement |
|---|---|
| REQ-DOC-001 | `docs/reference/env.md` MUST be generated from config definitions. Every variable has a description and default. |
| REQ-DOC-002 | `docs/reference/flow.md` MUST be generated from the flow schema descriptions. |

- **SCN-DOC-001** [I] (REQ-DOC-001, REQ-DOC-002) Generation gives no diff. A test fails when a config field or schema property has no description.

---

## 8. Security invariants

| ID | Invariant |
|---|---|
| SI-01 | No secret value is stored or sent in plaintext outside the task process: not in Postgres (builtin ciphertext only), logs, outputs, metrics, errors, audit, storage, Kubernetes objects, Docker container config, AI requests or API responses. |
| SI-02 | Passwords use argon2id. Session IDs, API tokens, run tokens and webhook keys are stored only as SHA-256 hashes. |
| SI-03 | Every route has an explicit permission. Unmapped routes fail the route inventory test. |
| SI-04 | A run token is valid for one task run only, expires at task timeout plus 10 minutes, and is revoked at terminal state. |
| SI-05 | Webhook keys and git webhook signatures are compared in constant time. Wrong webhook keys return 404. |
| SI-06 | Cookie-authenticated unsafe methods require same-origin (`Origin` or `Sec-Fetch-Site`). |
| SI-07 | Assistant mutating tool calls run only after user confirmation. All AI mutating actions are audited with the user as actor. |
| SI-08 | File APIs, git sync and bundle extraction reject absolute paths, `..` segments and symlinks. |
| SI-09 | Without master keys the builtin provider refuses writes. |
| SI-10 | Masking covers raw, base64 standard, base64 URL, URL-encoded and JSON-escaped forms of each secret value of 4 or more characters. The UI warns when a secret value is shorter. |
| SI-11 | Login failures are rate limited per email and per IP across instances. |
| SI-12 | Responses set `Content-Security-Policy: default-src 'self'; frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`. |

---

## 9. Non-functional requirements

| ID | Requirement |
|---|---|
| NFR-001 | `GET /api/v1/executions` (default filters, limit 50) p95 ≤ 300 ms with 100 000 executions and 400 000 task runs on local Postgres, measured over 50 requests after warmup. |
| NFR-002 | Log ingest of 5 000 lines/s for 60 s from one runner on one instance: zero lost lines, p95 batch ingest ≤ 500 ms. |
| NFR-003 | Initial UI route JavaScript ≤ 350 KiB gzip. Charts and editor load lazily. |

- **SCN-NFR-001** [P] (NFR-001) Seed the data set and measure. Result is written to the perf report.
- **SCN-NFR-002** [P] (NFR-002) Run the ingest load and verify line count and latency.
- **SCN-NFR-003** [I] (NFR-003) The build size check reads the Vite manifest and fails above the limit.

---

## 10. Verification

### 10.1 Tools

Go (version in `go.mod`), Bun, uv, Docker, kind, kubectl, Helm, just, golangci-lint, sqlc, gotestsum. `just setup` checks all tools and prints missing ones. PyPI and container registries must be reachable for image builds and examples.

### 10.2 `justfile` recipes

| Recipe | Content |
|---|---|
| `setup` | Tool check. |
| `gen` | sqlc, oapi-codegen, openapi-typescript, flow schema, validate-result schema, reference docs. |
| `gen-check` | `gen`, then `git diff --exit-code`. |
| `lint` | golangci-lint, `bun run lint`, `tsc --noEmit`, `helm lint`, `forbid`. |
| `forbid` | Fails on `TODO`, `FIXME`, `XXX`, `HACK`, `not implemented`, `unimplemented` in `.go`, `.ts`, `.tsx`, `.py`, `.sh`, `.yaml` files outside generated code and `docs/` (word list in `scripts/forbid-words.txt`, the only excluded file); fails on `t.Skip`, `test.skip`, `test.only`, `describe.skip` in any test file. No bypass marker exists. |
| `test` | Go unit tests with `-race`, Vitest. |
| `test-int` | Go integration tests with testcontainers. Includes `tests/review/`. |
| `build` | UI build, size check, Go build with embedded UI, both images. |
| `e2e` | API end-to-end and Playwright against the built binary and images. |
| `e2e-k8s` | kind cluster, image load, Helm install, `[K]` scenarios, cluster delete. |
| `perf` | `[P]` scenarios. |
| `trace` | §10.3. |
| `ledger-check` | §12.1 rules. |
| `check` | `gen-check lint test` (fast loop). |
| `verify` | `gen-check lint test test-int build e2e e2e-k8s perf trace ledger-check`. Writes `build/reports/verify.json` with HEAD SHA, tree state, each gate result and all JUnit results. |
| `evidence` | §12.3. |
| `evidence-check` | §12.3. |

All test gates write JUnit XML to `build/reports/junit/`.

### 10.3 Trace rules

`just trace` fails unless all are true:

1. Every `REQ-*`, `NFR-*` and `SI-*` ID in this SDD appears in the ID list of at least one scenario.
2. Every scenario ID has at least one test in the JUnit reports whose name contains the ID.
3. Every such test passed in the current verify run. No test for a scenario was skipped.
4. No test name references an ID that does not exist in this SDD (tests under `tests/review/` use finding IDs).

`just trace` writes `build/reports/trace.json`.

### 10.4 Test substitutes

| External system | Substitute |
|---|---|
| Neon / pooled Postgres | Postgres container behind PgBouncer (transaction mode) |
| AWS S3, Cloudflare R2 | MinIO |
| Azure Blob | Azurite (connection string and OAuth modes) |
| Azure Key Vault | lowkey-vault |
| HashiCorp Vault | Vault dev server |
| Kubernetes | kind |
| Git host | Local git server fixture with smart HTTP (token) and SSH (key) |
| LLM providers | Scripted HTTP servers speaking Anthropic Messages and OpenAI Chat Completions, recording requests |
| Network outage | Toxiproxy |

Substitutes replace only the external system. Sluice code paths under test are the production paths.

---

## 11. Build plan

Work proceeds in slice order. Each slice is vertical: schema, API, engine, UI and tests for its IDs.

| Slice | Content | Primary IDs |
|---|---|---|
| S0 | Repository, `justfile`, config, DB and migrations, PgBouncer path, leases, instance registry, health, metrics, OpenAPI pipeline, UI shell and theme, `forbid`, `trace`, `ledger-check` | CORE, API, DOC-001, UI-001, UI-010 |
| S1 | Users, sessions, tokens, RBAC, rate limit, audit, security headers, settings pages for these | AUTH, SI-02, SI-03, SI-06, SI-11, SI-12, UI-002 |
| S2 | Storage drivers and GC | STO |
| S3 | Namespaces, files, snapshots, bundles, flow parsing, validation, schema, revisions, `sluice validate`, editor, flows list and detail (no charts) | NS, FLOW, DOC-002, UI-007, SI-08 |
| S4 | Engine, dispatcher, inline and process executors, runner protocol, logs, outputs, metrics, artifacts, manual trigger, executions list and detail | EXE, RUN, EXR-001–003, EXR-008, TRG-001, UI-004, UI-005, UI-012, SI-04, NFR-002 |
| S5 | Schedules, webhooks, flow triggers, concurrency, two-instance cooperation | TRG, DEP-004, SI-05 |
| S6 | Secret providers, secrets, variables, master keys, masking | SEC, UI-008, SI-01, SI-09, SI-10 |
| S7 | Git sources, sync, webhooks, push branch | GIT |
| S8 | Docker executor, images, compose, single-container mode | EXR-004, EXR-007, EXR-009, DEP-001, DEP-003, DEP-005 |
| S9 | Kubernetes executor, reconciler, Helm chart, kind tests | EXR-005, EXR-006, DEP-002, SEC-012 |
| S10 | Dashboard, charts, flow charts, settings completion, accessibility, mobile width, UI size, list performance | UI-003, UI-006, UI-009, UI-011, UI-013, NFR-001, NFR-003 |
| S11 | AI providers, tool registry, MCP, assistant, authoring, triage | AI, SI-07 |
| S12 | Example ELT project | EX |

Slice assignment: IDs named in the table belong to that slice. An area name (for example `CORE`) covers the remaining IDs of that area. A scenario belongs to the slice of its first listed ID. A scenario that needs a later slice stays OPEN until that slice exists.

After S12: `just verify`, then §13, then §14.

---

## 12. Build records

`docs/build/` is owned by the implementer. The SDD is not.

### 12.1 Ledger — `docs/build/ledger.md`

```markdown
# Sluice v1 build ledger

status: IN_PROGRESS
sdd_sha256: <sha256 of docs/sluice-sdd.md>
current_slice: S0
review_round: 0
review_complete: false
blocking_findings_open: 0

## Blocked

## Items

| ID | Status | Slice | Evidence |
|---|---|---|---|
| REQ-CORE-001 | OPEN | S0 | |
| SCN-CORE-001 | OPEN | S0 | |
```

- `status`: `IN_PROGRESS`, `REVIEW`, `BLOCKED`, `DONE`.
- Item status: `OPEN`, `IN_PROGRESS`, `PASS`, `BLOCKED`.
- Scenario evidence: test file and test name. Requirement evidence: the scenario IDs.
- Blocked entry: `B-<n>`, date, item IDs, question, options with effect of each.

`just ledger-check` fails unless:

1. All header fields exist with valid values.
2. Every REQ, NFR, SI and SCN ID of the SDD appears exactly once. No other IDs appear.
3. A PASS scenario names a test that exists in the repository and contains the ID.
4. A PASS requirement, NFR or SI has all its mapped scenarios PASS.
5. `status: DONE` implies all items PASS, `review_complete: true`, `blocking_findings_open: 0` and no open blocked entries.
6. `sdd_sha256` equals the current SDD hash.

### 12.2 Decisions — `docs/build/decisions.md`

Two tables. Implementation decisions: `ID (DI-n) | date | area or IDs | decision | reason`. Human answers: `H-<n> | answers B-<n> | date | answer`. The human writes answers. The implementer reads them.

### 12.3 Evidence — `build/evidence/v1/`

`just evidence` fails when the tree is dirty or `build/reports/verify.json` is not for HEAD or has a failed gate. It writes:

- `evidence.json`: schema version, HEAD SHA, SDD hash, generated time, tool versions, gate results with durations, scenario totals and failures, trace matrix, review rounds and open blocking findings (from the ledger), NFR measurements.
- Copies of `verify.json`, `trace.json`, JUnit reports and perf report.

`just evidence-check` fails unless: `evidence.json` HEAD equals current HEAD, the tree is clean, all gates passed, all scenarios passed, the ledger at HEAD has `status: DONE`.

`build/` is in `.gitignore`.

---

## 13. Bounded review protocol

1. **Start.** When `just verify` passes for the first time: set ledger `status: REVIEW`, `review_round: 1`, commit.
2. **Reviewer.** A new agent context. It reads this SDD, the ledger, `decisions.md` and the repository at the recorded SHA. It writes only to `tests/review/round-<n>/` and `docs/build/review/round-<n>.md`. Use a different model family when the tool supports it.
3. **Focus.** Round 1 in order: §8 invariants; state, concurrency and lease logic (EXE, TRG, EXR, CORE-006); up to 10 requirements the reviewer judges weakly tested. Round 2: the round 1 fixes and files they touched.
4. **Finding format.** In `round-<n>.md`: `F-<n>-<k>`, reviewed SHA, IDs, claim, test path, blocking claim yes/no.
5. **Blocking criteria.** A finding is blocking only when all are true:
   - it names at least one REQ, NFR or SI ID;
   - the behavior it expects is stated in this SDD;
   - it has a test in `tests/review/round-<n>/` whose name contains the finding ID;
   - the implementer runs the test on the reviewed SHA and it fails for the stated reason.
   Otherwise the finding is a note. Notes are not implemented in v1. A test that fails for another reason is recorded as `rejected: invalid test` with the output.
6. **Fix.** The implementer fixes blocking findings, keeps the review tests, updates `blocking_findings_open`, and runs `just verify`.
7. **Round 2** runs only when round 1 had at least one blocking finding.
8. **Limit.** No round 3. Blocking findings of round 2 are fixed and verified with no new review.
9. **Complete.** All rounds run, `blocking_findings_open: 0`, `just verify` passes: set `review_complete: true` and commit.
10. A blocking finding that cannot be fixed within this SDD is a human interruption.

---

## 14. Definition of done

v1 is DONE when all are true at one commit:

1. `docs/build/ledger.md` has `status: DONE`, every item PASS, `review_complete: true`, `blocking_findings_open: 0`, no open blocked entries.
2. `just verify` exits 0 on that commit with a clean tree.
3. `just evidence` has written `build/evidence/v1/evidence.json` for that commit.
4. `just evidence-check` exits 0.

Order: set `status: DONE` and commit, run `just verify`, run `just evidence`, run `just evidence-check`. If a gate fails, set `status: IN_PROGRESS`, fix, repeat.

---

## Appendix A — Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `SLUICE_DATABASE_URL` | required | Postgres URL (pooled URL allowed) |
| `SLUICE_LISTEN_ADDR` | `:8080` | HTTP listen address |
| `SLUICE_PUBLIC_URL` | required | External base URL for links, cookies, webhooks |
| `SLUICE_INTERNAL_URL` | `http://127.0.0.1:<port>` | Base URL runners call (Service URL in Kubernetes) |
| `SLUICE_MASTER_KEYS` | empty | `kid:base64key[,kid:base64key]` |
| `SLUICE_BOOTSTRAP_ADMIN_EMAIL` / `_PASSWORD` | empty | First admin |
| `SLUICE_SESSION_TTL` | `168h` | Sliding session lifetime |
| `SLUICE_LOG_LEVEL` / `SLUICE_LOG_FORMAT` | `info` / `json` | Server logs |
| `SLUICE_POOLS` | `default` | Pools served by this instance |
| `SLUICE_EXECUTORS` | `auto` | `auto` or list |
| `SLUICE_WORKER_SLOTS` | `8` | Process and docker slots |
| `SLUICE_QUEUE_POLL_INTERVAL` | `1s` | Claim poll |
| `SLUICE_HEARTBEAT_TIMEOUT` | `60s` | Lost task threshold |
| `SLUICE_SHUTDOWN_GRACE` | `30s` | SIGTERM grace |
| `SLUICE_RETENTION_DAYS` | `90` | Execution retention |
| `SLUICE_STORAGE_TYPE` | `postgres` | `postgres`, `fs`, `s3`, `azblob` |
| `SLUICE_FS_ROOT` | empty | fs driver root |
| `SLUICE_S3_BUCKET`, `_REGION`, `_ENDPOINT`, `_FORCE_PATH_STYLE`, `_ACCESS_KEY_ID`, `_SECRET_ACCESS_KEY`, `_PREFIX` | — | s3 driver |
| `SLUICE_AZBLOB_ACCOUNT_URL`, `_CONTAINER`, `_CONNECTION_STRING`, `_PREFIX` | — | azblob driver |
| `SLUICE_MAX_FILE_BYTES` | `10MiB` | File limit |
| `SLUICE_MAX_BUNDLE_BYTES` | `200MiB` | Snapshot limit |
| `SLUICE_MAX_ARTIFACT_BYTES` | `100MiB` | Artifact limit |
| `SLUICE_RUNNER_IMAGE` | `sluice:<version>` | Source of the injected runner binary |
| `SLUICE_DOCKER_API_URL` | `http://host.docker.internal:<port>` | Runner callback URL from containers |
| `SLUICE_DOCKER_KEEP_CONTAINERS` | `false` | Keep task containers |
| `SLUICE_K8S_KUBECONFIG` | empty | Out-of-cluster access |
| `SLUICE_K8S_NAMESPACE` | own namespace | Job namespace |
| `SLUICE_K8S_MAX_JOBS` | `50` | Running Jobs per pool |
| `SLUICE_K8S_JOB_TTL` | `600s` | `ttlSecondsAfterFinished` |
| `SLUICE_K8S_PENDING_TIMEOUT` | `10m` | Pending pod limit |
| `SLUICE_SECRET_CACHE_TTL` | `60s` | External secret cache |
| `SLUICE_SECRET_<KEY>` | — | Values for the env provider |
| `SLUICE_VAULT_ADDR`, `SLUICE_VAULT_TOKEN`, `SLUICE_VAULT_K8S_ROLE` | — | Vault auth |
| `SLUICE_AI_MAX_CONTEXT_CHARS` | `120000` | AI context limit |

Azure credentials use the standard `AZURE_*` variables, workload identity or managed identity.

## Appendix B — Permission matrix

| Capability | viewer | operator | editor | admin |
|---|---|---|---|---|
| Read dashboards, flows, files, executions, logs, metrics, variables | ✓ | ✓ | ✓ | ✓ |
| List secret keys and metadata | ✓ | ✓ | ✓ | ✓ |
| Trigger, cancel, rerun, restart, run file | | ✓ | ✓ | ✓ |
| Git "Sync now" | | ✓ | ✓ | ✓ |
| Edit managed files, push branch, enable or disable flows, rotate webhook keys | | | ✓ | ✓ |
| Namespace secrets and variables write, secret check | | | ✓ | ✓ |
| Create managed namespaces | | | ✓ | ✓ |
| AI assistant (read tools) | ✓ | ✓ | ✓ | ✓ |
| AI tools that mutate | per tool (Appendix D) | | | |
| Own profile, password, own tokens | ✓ | ✓ | ✓ | ✓ |
| Users, all tokens, global secrets and variables, secret providers, git sources, storage view, AI provider, settings, audit log, delete namespaces | | | | ✓ |

## Appendix C — Runner API (`/api/runner/v1`, bearer run token)

| Method and path | Purpose |
|---|---|
| `GET /task-runs/{id}/spec` | Command, workdir, resolved env, mask values, limits |
| `GET /task-runs/{id}/bundle` | Snapshot bundle (`tar.gz`) |
| `POST /task-runs/{id}/logs` | `{seq, lines:[{ts, stream, text}]}` |
| `POST /task-runs/{id}/events` | `{seq, events:[output or metric]}` |
| `PUT /task-runs/{id}/artifacts/{name}` | Streamed artifact body |
| `POST /task-runs/{id}/heartbeat` | Returns `{cancel: bool}` |
| `POST /task-runs/{id}/complete` | `{exit_code, error}` |

## Appendix D — AI tool registry

| Tool | Permission | Mutating |
|---|---|---|
| `list_namespaces`, `list_flows`, `get_flow`, `validate_flow`, `list_files`, `read_file` | viewer | no |
| `list_executions`, `get_execution`, `get_logs`, `get_metrics`, `get_insight` | viewer | no |
| `trigger_execution`, `cancel_execution` | operator | yes |
| `propose_change` (assistant; returns diff, no write) | editor | no |
| `apply_change` (managed version or git branch push) | editor | yes |

## Appendix E — UI routes

`/login`, `/` (dashboard), `/executions`, `/executions/$id`, `/flows`, `/flows/$namespace/$flowId` (overview, executions, triggers, source, revisions), `/namespaces`, `/namespaces/$namespace` (files, versions, variables, secrets), `/secrets`, `/variables`, `/settings/profile`, `/settings/tokens`, `/settings/users`, `/settings/git`, `/settings/secret-providers`, `/settings/storage`, `/settings/ai`, `/settings/instances`, `/settings/audit`. The assistant is a drawer on all routes.
