# Sluice

Sluice runs flows of tasks: Python, shell and bun scripts, commands, HTTP calls and subflows. It runs them on a process, docker or kubernetes executor, with schedules, webhooks, secrets, logs, metrics and a web UI. It is one Go binary with Postgres.

![Sign in, open a flow, run it and watch the execution](docs/images/quickstart.gif)

## Getting started

You need Docker and [just](https://just.systems).

1. Build the images:

   ```sh
   just build-images
   ```

2. Start Sluice and Postgres:

   ```sh
   export SLUICE_BOOTSTRAP_ADMIN_PASSWORD=change-me-now-1
   export SLUICE_MASTER_KEYS="k1:$(openssl rand -base64 32)"
   docker compose -f deploy/compose/compose.yml up -d
   ```

3. Open <http://localhost:8080> and sign in as `admin@local.test` with the password of step 2.
4. Open **Namespaces**, create a namespace, and add a flow file, for example `hello.flow.yaml`:

   ```yaml
   id: hello
   tasks:
     - id: say
       type: command
       command: ["echo", "hello from sluice"]
   ```

5. Save the file, open **Flows**, select the flow, and click **Run**. The execution page shows the timeline and the logs.

| Dashboard | Flow |
|---|---|
| ![Dashboard](docs/images/dashboard.png) | ![Flow overview](docs/images/flow.png) |
| **Editor** | **Execution** |
| ![Namespace editor](docs/images/editor.png) | ![Execution](docs/images/execution.png) |

## Documentation

| Document | What it holds |
|---|---|
| [docs/getting-started.md](docs/getting-started.md) | The full walkthrough: start Sluice, write a flow, run it and read the execution. |
| [docs/flows.md](docs/flows.md) | How to write flows: tasks, inputs, templates, outputs, metrics, retries and concurrency. |
| [docs/triggers.md](docs/triggers.md) | Schedules, webhooks and flow triggers. |
| [docs/executors.md](docs/executors.md) | The process, docker and kubernetes executors, pools and the runner. |
| [docs/secrets-and-variables.md](docs/secrets-and-variables.md) | Secrets, providers, variables, scopes and masking. |
| [docs/git-sync.md](docs/git-sync.md) | Git sources, mappings, sync and push to a branch. |
| [docs/ai.md](docs/ai.md) | The assistant, flow authoring, failure triage and the MCP server. |
| [docs/deployment.md](docs/deployment.md) | Single container, compose, Kubernetes with Helm, storage and the CLI. |
| [docs/architecture.md](docs/architecture.md) | The components, the execution lifecycle, snapshots and leases. |
| [docs/api.md](docs/api.md) | The HTTP API, authentication, errors, pagination and event streams. |
| [docs/operations/runbook.md](docs/operations/runbook.md) | Health, logs, backups, upgrades and common errors. |
| [docs/operations/metrics.md](docs/operations/metrics.md) | Every metric of `/metrics` and the dashboard figures. |
| [docs/operations/security.md](docs/operations/security.md) | Authentication, roles, secret handling and hardening. |
| [docs/reference/flow.md](docs/reference/flow.md) | The flow file reference. |
| [docs/reference/env.md](docs/reference/env.md) | Every environment variable. |
| [examples/elt](examples/elt/README.md) | An ELT example with dlt and SQLMesh, with demo data. |
| [docs/sluice-sdd.md](docs/sluice-sdd.md) | The design: requirements, scenarios and decisions. |
| [CONTRIBUTING.md](CONTRIBUTING.md) | How to build, test and submit a change. |
| [TESTING.md](TESTING.md) | The test layers, the local substitutes and the definition of done. |
| [SECURITY.md](SECURITY.md) | How to report a security defect. |
| [CHANGELOG.md](CHANGELOG.md) | What changed. |
