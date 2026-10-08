---
name: sluice
description: Write, validate, deploy, run and debug Sluice flows (*.flow.yaml) and namespace files. Use when a task touches a Sluice namespace directory, a flow file, namespace.yaml, the sluice CLI, the Sluice MCP server or a Sluice execution.
---

# Sluice

Sluice runs flows of tasks. A flow is a YAML file in a namespace. A namespace is a directory of flows, scripts and other files. This skill matches the `sluice` binary that wrote it (`sluice version`).

Reference: https://sluice-docs.pages.dev. Flow schema: https://sluice-docs.pages.dev/schemas/flow.schema.json. All docs as one file: https://sluice-docs.pages.dev/llms-full.txt.

## The loop

Do these steps in this order for every change to a namespace directory:

1. Edit the files.
2. Validate offline: `sluice validate <dir> --json`. Exit 0 means valid. Exit 1 lists each error with `path`, `line`, `column`, `code` and `message`. Fix every error before the next step.
3. Deploy: `sluice namespaces push <dir> --namespace <name>`. It sends only the changed files as one new version. It creates no version when nothing changed. Add `--create` the first time, when the namespace does not exist yet.
4. Run: `sluice run <namespace>/<flow> --wait --input key=value`. The logs stream to stderr. The exit code is the end state. A `string` or `select` input takes the value as text, so `--input year=2026` is the string `2026`. For other input types a JSON value keeps its type.
5. On a failure, read the failed task: `sluice executions get <id>`, then `sluice executions logs <id> --task <task>`.
6. Fix the files, then go to step 2 and start a new run with `sluice run`. `sluice executions rerun <id>` and `sluice executions restart <id>` reuse the files of the old execution. Use them only when the cause is outside the files, for example a network error. `restart` keeps the successful tasks and runs only the rest.

Install or update the binary with `curl -fsSL https://raw.githubusercontent.com/alternayte/sluice/main/install.sh | sh`.

The client commands read `SLUICE_URL` (for example `https://sluice.example.com`) and `SLUICE_TOKEN` (an API token from Settings, API tokens). Add `--output json` to get the API JSON on stdout.

| Exit code | Meaning |
|---|---|
| 0 | Success. |
| 1 | API or network error, or `sluice validate` found an invalid file. |
| 2 | Usage or configuration error. |
| 10 | The execution ended FAILED. |
| 11 | The execution ended TIMED_OUT. |
| 12 | The execution ended CANCELLED. |
| 13 | The execution ended SKIPPED (a concurrency limit with `behavior: skip`). |
| 14 | `--timeout` ended the wait. The execution continues. |

An API error prints its code, its message and one line for each detail, for example `inputs.space: must be one of [all 1]`. With `--output json`, stdout holds the error envelope `{"error":{"code","message","details"}}`.

## Ignored files

`sluice validate` and `sluice namespaces push` skip `.git/`, `__pycache__/`, `*.pyc`, `.venv/`, `node_modules/` and `.DS_Store`. Add more rules to `.sluiceignore` at the root of the namespace directory. The syntax is that of `.gitignore`. Sluice does not read `.gitignore`. `--verbose` prints each skipped path.

`push` refuses a file that looks like a secret (`.env`, `.env.*`, `*.pem`, `*.key`, `*.p12`) and sends nothing. Add the path to `.sluiceignore` to skip the file. Add `!<path>` to permit a file that is not a secret, for example `!.env.example`.

## Secrets

```sh
sluice secrets list [--namespace <ns>]
sluice secrets set <KEY> --from-file <path> [--namespace <ns>] [--description <text>]
printf '%s' "$VALUE" | sluice secrets set <KEY> --stdin
sluice secrets check <KEY>
sluice secrets delete <KEY>
```

Without `--namespace` the secret is global, and a write needs the admin role. The value comes from a file or stdin only and is stored as it is, with a final newline when the input has one. No command prints a value.

## Flow file

A flow file is `<name>.flow.yaml` anywhere in the namespace. Put this line first, so editors validate it:

```yaml
# yaml-language-server: $schema=https://sluice-docs.pages.dev/schemas/flow.schema.json
id: nightly-load
description: Load orders, then build the report.
inputs:
  - { id: run_date, type: string, required: true }
triggers:
  - { id: nightly, type: schedule, cron: "0 2 * * *", timezone: Europe/Zurich }
retry: { max_attempts: 3, backoff: exponential, initial: 30s }
tasks:
  - id: extract
    type: script
    file: pipelines/extract.py
    args: ["--date", "${{ inputs.run_date }}"]
    env:
      DB_URL: ${{ secret('DB_URL') }}
  - id: report
    type: command
    depends_on: [extract]
    command: ["echo", "rows: ${{ tasks.extract.outputs.rows }}"]
outputs:
  rows: ${{ tasks.extract.outputs.rows }}
```

Rules that validation enforces:

- Flow `id`: lower case letters, digits and hyphens. Unique in the namespace.
- Task and input `id`: `^[a-z][a-z0-9_]{0,62}$`. Use underscores, not hyphens.
- A flow has 1 to 200 tasks. `depends_on` names tasks of the same flow and has no cycle.
- `run_if` is `success` (default), `failure` or `always`.
- A field of another task type is an error (`field_not_allowed`), for example `url` on a `script` task.
- `executor` is not allowed on `http` and `subflow` tasks.

## Task types

| Type | Required | Optional | Runs |
|---|---|---|---|
| `script` | `file` (path from the namespace root) | `runtime`, `args` | the file; the extension selects the runtime (.py, .sh, .ts, .js) |
| `command` | `command` (argv list, no shell) | `workdir` | the argv |
| `http` | `url` | `method`, `headers`, `body`, `expect_status` | an HTTP request on the server |
| `subflow` | `flow` (`<namespace>/<flow_id>`) | `inputs`, `wait` | a child execution |
| `wait` | — | `message`, `fields` | nothing; it waits for an answer |

A `command` has no shell. For pipes or `&&`, use `command: ["sh", "-c", "a | b"]`.

Every task also takes `depends_on`, `run_if`, `timeout` (default `24h`), `retry`, `env` and `executor`. `script` and `command` take `files`: a map from a path to a template that Sluice writes before the task starts. They also take `artifacts`: files that a dependency emitted.

`script`, `command` and `subflow` take `each`: a list, or one template that gives a JSON list of at most 1000 items. Sluice runs the task one time for each item, and `max_parallel` on the task limits the items that run at the same time:

```yaml
- id: load
  type: script
  file: pipelines/load.py
  each: ${{ inputs.tables }}       # or a literal list, or ${{ tasks.<id>.outputs.<key> }}
  max_parallel: 2
  args: ["--table", "${{ item }}", "--part", "${{ item_index }}"]
```

A `wait` task pauses its part of the flow until a person, a script or an agent answers. `fields` has the shape of flow inputs:

```yaml
- id: approve
  type: wait
  depends_on: [build]
  timeout: 8h                     # default 24h; then the task ends TIMED_OUT
  message: "Deploy ${{ inputs.version }}?"
  fields:
    - { id: reason, type: string, required: true }
```

The task run is WAITING and the execution stays RUNNING. `sluice executions list --waiting` finds them, and `sluice executions get <id>` prints the question. `sluice executions resume <id> --task approve --input reason=ok` ends the task SUCCESS, and the values are its outputs (`tasks.approve.outputs.reason`). `sluice executions reject <id> --task approve --message "no"` ends it FAILED with reason `rejected`, so a task with `run_if: failure` handles it. A `wait` task sends no message: put an `http` task before it to notify people. `sluice run --wait` does not end while a task waits.

A later task reads `${{ tasks.load.outputs.rows }}` as a list in item order, with `null` for an item that did not emit the key. The task is SUCCESS only when every item is. An empty list ends the task SUCCESS, and each key gives `[]`. `sluice executions restart` runs only the items that did not succeed. A task that declares an artifact of `load` gets one file for each item: `path: data/part.parquet` gives `data/0/part.parquet`, `data/1/part.parquet`. Do not write a loop in a script when each item needs its own retry and log: use `each`.

## Templates

A template is `${{ expr }}` in a string. An expression is a lookup. There are no operators.

| Expression | Value |
|---|---|
| `inputs.<id>` | A declared flow input. |
| `vars.<KEY>` | A variable: flow `variables`, then the namespace, its parents, then global. |
| `secret('<KEY>')` | A secret. Allowed only in `env` values, `files` values and the `http` fields `url`, `headers` and `body`. |
| `tasks.<task_id>.outputs.<key>` | An output of a task that is a dependency, directly or through other tasks. |
| `item`, `item_index` | The item of a task with `each` and its position from 0. `item.<field>` reads a field of an object. |
| `trigger.<path>` | The trigger payload, for example `trigger.body` of a webhook. |
| `execution.id`, `execution.namespace`, `execution.flow_id`, `execution.created_at` | Facts of the execution. |

Write `$${{` for a literal `${{`.

## Outputs, metrics and artifacts

A task writes one JSON object per line to the file in `$SLUICE_OUTPUTS`:

```sh
echo '{"type":"output","key":"rows","value":42}' >> "$SLUICE_OUTPUTS"
echo '{"type":"metric","name":"rows_loaded","value":42,"unit":"rows"}' >> "$SLUICE_OUTPUTS"
echo '{"type":"artifact","path":"report.html","name":"report"}' >> "$SLUICE_OUTPUTS"
```

Outputs of one task are limited to 1 MiB. To pass a file to a later task, emit it as an artifact and declare it on the task that reads it:

```yaml
- id: render
  type: script
  file: scripts/render.py
  depends_on: [transform]
  artifacts:
    - from: transform           # a script or command task in depends_on
      name: report.duckdb       # the artifact name that transform emitted
      path: data/report.duckdb  # optional; default is the name
```

Sluice writes the artifact to `path` in the workdir before the task starts. `from`, `name` and `path` are literals, not templates. When the artifact does not exist, the task fails with reason `artifact_not_found` and its command does not start. `sluice executions restart` keeps the artifacts of the reused tasks.

Other variables of every task: `SLUICE_EXECUTION_ID`, `SLUICE_TASK_ID`, `SLUICE_ATTEMPT`, `SLUICE_NAMESPACE`, `SLUICE_FLOW_ID` and `SLUICE_WORKDIR`.

## namespace.yaml

`namespace.yaml` at the namespace root sets defaults for all flows. A flow or task value overrides a default.

```yaml
# yaml-language-server: $schema=https://sluice-docs.pages.dev/schemas/namespace.schema.json
description: ELT pipelines
defaults:
  executor: { type: docker, image: ghcr.io/acme/elt:1.4.0 }
  env: { TZ: Europe/Zurich }
  retry: { max_attempts: 2 }
  timeout: 1h
```

## MCP

The server serves MCP at `<SLUICE_URL>/mcp` with a bearer API token. `<SLUICE_URL>/.well-known/mcp.json` describes it. Useful tools: `get_flow_schema`, `validate_flow`, `list_executions`, `get_execution`, `get_logs` (with `failed_only: true` and `grep`), `get_insight`, `rerun_execution`, `restart_execution`, `resume_execution` and `reject_execution`. A tool runs with the role of the token.

## Do not

- Do not put a secret value in a flow file, a script or a label. Use `secret('KEY')` in `env` and read the variable.
- Do not call the secrets API by hand or pass a value as an argument. Use `sluice secrets set` with `--from-file` or `--stdin`.
- Do not skip `sluice validate` before a push. An invalid flow gets no active triggers, and a run returns 422 `flow_invalid`.
- Do not push to a namespace that comes from git. It is read-only; change the git repository instead.
