# Sluice

## What this is
Sluice is a self-hosted flow orchestrator: one Go binary with Postgres, a React web UI embedded from `ui/dist`, a CLI and an MCP server. It runs the tasks of YAML flows on pluggable executors, with triggers, secrets, logs and metrics.

## Run
- `just setup` checks the tools, writes `.env` and installs the UI packages.
- `just up` starts Postgres. `just dev` starts the Go server on 8080 with live reload and Vite on 5173, which proxies `/api`, `/hooks` and `/mcp`.
- `just build-ui build-go` builds `bin/sluice` with the embedded UI.

## Test
- `just check` is the gate: `gen-check`, `lint` (golangci-lint, `just forbid`, Prettier, ESLint, tsc) and `test`.
- `just test` needs a running Docker daemon: Go tests use the integration tag and testcontainers.
- `just e2e` runs the Go e2e suite and Playwright in `tests/ui` against `bin/sluice`. Build the binary first.
- A test calls a public boundary and checks an outcome. Tests do not mock Sluice code.
- A test verifies an SDD scenario only when its name holds the scenario ID, for example SCN-EXE-003. `just trace` reads these.

## Stack rules
- `docs/sluice-sdd.md` is the authoritative design. Never edit it. A spec in `docs/specs/` states what it replaces.
- `just gen` writes `api/openapi.yaml`, the sqlc packages, `ui/src/api`, `schemas` and `site/src/content/docs/reference`. Never edit these by hand.
- The server sends a Content-Security-Policy of default-src 'self'. UI code never injects style or script elements. `ui/src/components/editor/csp-styles.ts` shows the pattern for libraries that do.

## Domain words
- namespace: a named tree of files that holds flows and scripts, edited in Sluice or synced from git.
- snapshot: one immutable version of a namespace's files.
- flow: a YAML file in a namespace that declares tasks and triggers.
- flow revision: the parsed definition of a flow at one snapshot.
- execution: one run of a flow or of a namespace file.
- task run: one attempt of one task in an execution.
- rerun: a new execution with the same snapshot and inputs.
- restart: a new execution that reuses the SUCCESS task runs of an ended execution.
- trigger: a declaration in a flow that creates executions from an outside event.
- executor: the backend that runs a task run.
- pool: a named group of worker slots that claims task runs.
- instance: one running sluice server process.
- runner: the sluice exec process inside a task that talks to the runner API.
- triage: an AI explanation of a failed execution, stored as an insight.
