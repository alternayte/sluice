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

## More

- ELT example with dlt and SQLMesh: [examples/elt](examples/elt/README.md)
- Flow reference: [docs/reference/flow.md](docs/reference/flow.md)
- Environment variables: [docs/reference/env.md](docs/reference/env.md)
- Kubernetes: the Helm chart in [deploy/helm/sluice](deploy/helm/sluice)
- Design: [docs/sluice-sdd.md](docs/sluice-sdd.md)
