# Changelog

All notable changes to Sluice are in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Sluice uses [Semantic Versioning](https://semver.org/). Before 1.0.0, a minor version can break compatibility.

## [Unreleased]

## [0.2.1] - 2026-09-25

### Added

- Install Sluice, a docs page for Docker Compose, Docker, Helm and the CLI.
- The release publishes the Helm chart to `oci://ghcr.io/alternayte/charts/sluice`. The `Chart` workflow publishes the chart of an existing tag.

### Changed

- `deploy/compose/compose.yml` uses the released image `ghcr.io/alternayte/sluice-uv:latest` and needs no checkout. `SLUICE_IMAGE` picks another image, for example `sluice-uv:dev` from `just build-images`.
- `install.sh` installs the latest release.

### Fixed

- A release without a suffix, such as v0.2.0, is the latest release on GitHub. Before, every `v0.*` release was a prerelease, so GitHub showed no latest release.
- The Helm chart deploys the image of its release. A release fails when the chart version differs from the tag. The chart of v0.2.0 still names 0.1.2, so use the chart of 0.2.1.

## [0.2.0] - 2026-09-24

### Added

- A command palette (Cmd+K or Ctrl+K) searches flows, namespaces, executions, files and pages, and runs actions such as run flow, rerun, restart from failed, new file and theme.
- Keyboard shortcuts: `?` shows the sheet, `g` chords open pages, and `j`, `k` and Enter move through lists.
- The execution page shows a live waterfall of the task runs and a resizable inspector with the attempts, outputs, metrics and logs of the selected task. It has "Jump to first failure" and "Download JSON".
- The log viewer follows new lines, reconnects after a lost stream, links URLs, marks search matches, colours stderr and wraps lines on request.
- The executions list shows the duration of the loaded executions over time.
- Client commands for a remote server: `sluice run`, `sluice executions list|get|logs|cancel|rerun|restart`, `sluice flows list|get` and `sluice namespaces push`. They read `SLUICE_URL` and `SLUICE_TOKEN`, take `--output json` and exit with the end state of the execution (10 FAILED, 11 TIMED_OUT, 12 CANCELLED, 13 SKIPPED, 14 wait timeout).
- A GitHub Action (`alternayte/sluice`) runs a flow, streams its logs and writes the result to the job summary.
- `sluice init` writes the Sluice agent skill and an AGENTS.md section into a repository.
- MCP tools `rerun_execution`, `restart_execution` and `get_flow_schema`, and the `grep` and `failed_only` options of `get_logs`. `/.well-known/mcp.json` describes the MCP endpoint.
- The assistant takes `@` mentions of flows, executions and files as attachments. "Fix with assistant" on a failed execution starts a conversation with the execution, its triage and the failed task logs.

- `install.sh` installs the `sluice` binary for Linux and macOS in one line: `curl -fsSL https://raw.githubusercontent.com/alternayte/sluice/main/install.sh | sh`. It checks the download against the published checksum. The GitHub Action installs with it.
- Sluice is licensed under the GNU AGPL 3.0 (`LICENSE`).

### Fixed

- A task run that the engine ends after its timeout and the grace time is `TIMED_OUT` with reason `timeout`, not `CANCELLED`, and its retry policy applies.
- A subflow input of type `string` keeps the rendered text. A value such as `42` or `true` no longer fails with "must be a string".
- The default runner image exists: a release uses `ghcr.io/alternayte/sluice:<version>`, and a build from source uses `sluice:dev`. Before, the default was `sluice:<version>`, which no registry had.

### Changed

- The web UI has a new visual system: Inter, a translucent sidebar, hairline panels, spring motion that stops under reduced motion, toasts, skeletons and empty states.
- The JSON Schema `$id`s are URLs on the docs site, `https://sluice-docs.pages.dev/schemas/`. `schemas/namespace.schema.json` is new.

## [0.1.2] - 2026-09-15

### Changed

- The namespace file browser is a tree with folders, file icons and keyboard navigation. It keeps the open folders per namespace. The New file dialog starts with the current folder.
- The file browser column is resizable from 224 px to 576 px, and the browser keeps the width.

### Fixed

- The namespace editor fills the window height. The file browser, the editor and the validation errors scroll inside it, so the editor no longer leaves empty space below it.

## [0.1.1] - 2026-09-15

### Changed

- The secret value field is a textarea. It grows with the value up to 320 px, then scrolls, and it can be resized. An eye button shows or hides the value.
- The release workflow uses the Node 24 versions of the Docker actions.

## [0.1.0] - 2026-09-15

### Added

The first version of Sluice. It is one Go binary with an embedded React UI, and Postgres holds all state. The SDD §13 run passed with 139 of 139 scenarios.

#### Flows and validation

- A task `files` map for `script` and `command` tasks. Sluice renders each value as a template and writes it to its path in the workdir before the task starts. `secret()` is allowed in the values.
- Flows in YAML: a DAG of tasks with inputs, outputs, retries, timeouts and concurrency.
- Task types `script`, `command`, `http` and `subflow`, and a direct run of script files.
- Structural and semantic validation, a flow JSON schema, templates and `sluice validate`.
- A JSON Lines emit protocol for outputs, metrics and artifacts.
- `run_if` rules for dependencies that were skipped or failed (DI-21).

#### Namespaces and snapshots

- Namespaces with files, in managed mode or git mode.
- Content-addressed file objects and snapshot manifests. Each execution pins one snapshot.
- Bundles, flow revisions, versions and diffs.
- A new execution pins the namespace head snapshot (DI-41).
- Storage drivers `postgres`, `fs`, `s3` and `azblob`, a readiness check and garbage collection (DI-17).

#### Execution engine

- An engine and a dispatcher that claim work with `SELECT … FOR UPDATE SKIP LOCKED`, without a broker (DI-19).
- Leases for the scheduler, the reconciler and the maintenance leader.
- A runner protocol for logs, outputs, metrics, artifacts, heartbeats and cancel.
- A task timeout and cancel with SIGTERM, then SIGKILL after 10 s (DI-20).
- Any number of server replicas on one database, also across clusters.
- A database layer that works through PgBouncer in transaction mode (DI-5).

#### Executors

- `inline` and `process` executors.
- A `docker` executor that uses the Engine API and copies the runner into each container (DI-34).
- A `kubernetes` executor with one Job per attempt, an init container and a reconciler (DI-35).
- Auto detection of the executors, and pools.

#### Triggers

- Manual, schedule, webhook and flow completion triggers.
- Exactly-once schedule fires across leader changes, with time zone and DST rules (DI-26, DI-27).
- Webhook keys that an editor rotates and that the server shows once (DI-28).
- Flow triggers with a chain depth limit of 10 (DI-29).

#### Secrets and variables

- Scoped and inherited secrets and variables.
- Providers `builtin`, `env`, `kubernetes`, `azure_key_vault` and `vault`.
- AES-256-GCM encryption of builtin values with master keys, and `sluice secrets rekey`.
- Secret values are masked in logs, outputs and AI requests.

#### Authentication and audit

- User accounts, sessions, API tokens and four fixed roles.
- Login rate limits across instances (DI-15), CSRF checks (DI-12) and security headers.
- An audit log.

#### Git sync

- Git sources with poll and webhook sync, with go-git (DI-33).
- Push of UI edits to a new branch.
- Rejection of symlinks, submodules and invalid paths.

#### Dashboard and UI

- Dashboard, executions list, execution detail with a Gantt timeline, flows, files and editor, secrets, variables and settings.
- A dashboard success rate (DI-37) and flow charts.
- A namespace editor that stages files and saves them as one version (DI-42).
- A generated API client with TanStack Query.
- A UI size check, accessibility checks and support for mobile width.
- Icons in the side nav. The side nav collapses to icons only, shows a tooltip on hover and keeps the state in the browser.

#### AI and MCP

- Anthropic and OpenAI-compatible provider adapters (DI-38).
- One tool registry for the assistant and the MCP server at `/mcp` (DI-39).
- An assistant panel that asks the user to confirm each action that changes data (DI-39).
- An assistant that writes and changes flows, and failure triage (DI-40).

#### Deployment

- The `sluice` and `sluice-uv` container images.
- A production compose file and a single-container mode.
- A Helm chart in `deploy/helm/sluice`.
- Configuration from environment variables only.

#### Example project

- An ELT example in `examples/elt` with dlt and SQLMesh, and demo data.

#### API and build tools

- A code-first API with huma and chi, and a generated `api/openapi.yaml`.
- sqlc with one generated package per feature.
- `just` recipes for setup, development, generation, lint, tests, build and trace.
- A CI workflow with the jobs `check`, `e2e` and `image`.
- A release workflow. A `v*.*.*` tag attaches the binaries for Linux and macOS to a GitHub release, and pushes the `sluice` and `sluice-uv` images to `ghcr.io/alternayte` for amd64 and arm64.

[Unreleased]: https://github.com/alternayte/sluice/compare/v0.2.1...HEAD
[0.2.1]: https://github.com/alternayte/sluice/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/alternayte/sluice/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/alternayte/sluice/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/alternayte/sluice/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/alternayte/sluice/releases/tag/v0.1.0
