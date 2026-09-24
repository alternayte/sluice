# Agent readiness

## What it does
Three kinds of agent operate Sluice: a coding agent in the user's repo, an ops agent over MCP, and a CI job. The `sluice` binary gets client commands for a remote server, with JSON output and stable exit codes. A GitHub Action runs a flow and fails the job when the flow fails. `sluice init` writes a skill that matches the server version into the user's repo. MCP gets the missing tools and a discovery document. The assistant takes `@` context and opens from a failed execution.

## Decisions
- Agents operate Sluice. Sluice does not run agents as tasks — AI task types change the flow model and the executor, so they are a separate feature.
- Client commands read `SLUICE_URL` and `SLUICE_TOKEN`. A token is required. There is no `login` command and no profile file — CI and agents both use env vars.
- `sluice run <ns>/<flow> [--input k=v] [--label k=v] [--wait] [--timeout d]` starts an execution. With `--wait` it streams the logs to stderr and prints the result to stdout — a CI job needs the end state.
- `sluice executions list|get|logs|cancel|rerun|restart` and `sluice flows list|get` wrap the existing endpoints — no new server API.
- `sluice namespaces push <dir>` compares a local directory with the head version and sends the changes to `saveChanges` as one new version — CI can deploy flows without git sync.
- Every client command takes `--output json` (default `text`). The JSON shape is the API shape — no second model to keep in sync.
- Exit codes: 0 success, 1 API or network error, 2 usage or config error, 10 FAILED, 11 TIMED_OUT, 12 CANCELLED, 13 SKIPPED, 14 `--timeout` reached before the end. The reference docs list them — scripts branch on the code, not on text.
- `action.yml` at the repo root runs `sluice run --wait`. It writes the execution link and the end state to the job summary. Its inputs are `url`, `token`, `flow`, `inputs`, `labels` and `timeout` — the same pattern as speccy.
- `skills/sluice/SKILL.md` holds the flow authoring rules, the task types, the template syntax and the validate-run-read loop through the CLI. The binary embeds it — the skill always matches the server version.
- `sluice init [dir]` writes `.claude/skills/sluice/SKILL.md`, adds a Sluice section to `AGENTS.md` (it creates the file if it is missing), and prints the `# yaml-language-server: $schema=` line for flow files. It never overwrites a changed file without `--force`.
- New MCP tools: `rerun_execution` and `restart_execution` (operator, mutating), and `get_flow_schema` (viewer). The assistant gets them too — one registry serves both.
- `get_logs` gets a `grep` filter and a `failed_only` option — an agent needs the failure, not 1000 lines.
- `GET /.well-known/mcp.json` describes the `/mcp` endpoint and its bearer auth — clients discover the server.
- The assistant accepts `@` mentions of a flow, an execution or a namespace file. The mention attaches that object as context — the user does not paste IDs.
- A "Fix with assistant" button on a FAILED or TIMED_OUT execution opens the assistant with the triage and the failed task attached.
- Every schema `$id` changes from `https://sluice.dev/...` to `https://sluice-docs.pages.dev/schemas/...` — `sluice.dev` is not in the user's account. `docs-site.md` serves the files.

## Out
- AI task types and flows exposed as MCP tools.
- Ask, Plan and Edit modes in the assistant. Confirm before a mutation already gives that control.
- A `login` command, profile files and OS keychain storage.
- Docs pages. `docs-site.md` holds them.

## How I know it works
- `SLUICE_URL=… SLUICE_TOKEN=… sluice run demo/hello --wait` streams the logs and exits 0. The same command on a flow that fails exits 10.
- `sluice run demo/hello --wait --output json | jq .state` prints `"SUCCESS"`.
- `sluice executions logs <id> --output json` returns the log lines as a JSON array.
- `sluice namespaces push ./flows/demo` creates one new namespace version with only the changed files. A second push with no change creates no version.
- A workflow that uses the action on a failing flow shows a red job and a summary with the execution link.
- `sluice init` in an empty directory writes the skill and `AGENTS.md`. A second run changes nothing.
- `claude mcp add` with the Sluice URL lists `rerun_execution`, `restart_execution` and `get_flow_schema`. A viewer token gets `forbidden` on `rerun_execution`.
- `curl /.well-known/mcp.json` returns the endpoint URL and the auth scheme.
- Typing `@` in the assistant lists flows, executions and files. A mention adds a chip, and the model answer uses that object.
- "Fix with assistant" on a failed execution opens the assistant with the triage in the first message.
- `just check` passes.
