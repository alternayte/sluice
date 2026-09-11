# AI

This document holds the reference of the AI features. `README.md` links here. The design is in SDD §7.14 and in the decisions DI-38 to DI-40 of [build/decisions.md](build/decisions.md).

Sluice has four AI features. They share one provider and one tool registry.

| Feature | What it does | Needs a provider |
|---|---|---|
| Assistant | A chat panel on all pages. It reads flows, executions and logs with tools, and it changes Sluice only after you confirm. | yes |
| Flow authoring | The assistant proposes file changes, validates them, shows a diff and applies them. | yes |
| Failure triage | A summary of why an execution failed, with evidence from the logs. | yes |
| MCP server | `/mcp` gives the same tools to an external MCP client, for example Claude Code. | no |

## Configure the provider

Sluice uses one provider. An admin configures it on **Settings → AI provider** (`/settings/ai`).

| Field | Rule |
|---|---|
| Type | `anthropic` (Messages API) or `openai_compatible` (Chat Completions API). |
| Base URL | Empty uses `https://api.anthropic.com` or `https://api.openai.com/v1`. Use `https://`. Plain `http://` is allowed only for a loopback host, for example a local model server. |
| Model | The model name that the provider expects. |
| API key secret key | The key of a **global secret** that holds the API key. The page never shows the key. |
| Triage automatically | When on, every FAILED and TIMED_OUT execution gets a triage. |

1. Create the global secret, for example `ANTHROPIC_API_KEY`, on **Secrets**.
2. Open **Settings → AI provider**, fill in the fields and click **Save**.
3. Click **Test provider**. Sluice sends one short request. The page shows "The provider answered." or the error of the provider.

An OpenAI-compatible server gets no provider-specific fields, so local model servers with the Chat Completions API work too.

**Retries.** A 429 answer, a 5xx answer or a network error gets 3 attempts in total, with 0.5 s and 1 s between them. After the third attempt the feature shows the error. The test `SCN-AI-002` proves both adapters against scripted servers.

### Without a provider

Without a provider the UI hides the assistant and the triage panel. The AI operations answer `409 ai_disabled`. MCP still works, because MCP needs no model. `GET /api/v1/ai/status` tells a client whether AI is on. The test `SCN-AI-001` proves this.

## Tools

One registry serves the assistant and MCP. Each tool has a minimum role and a mutating flag.

| Tool | Role | Mutating | What it does |
|---|---|---|---|
| `list_namespaces` | viewer | no | Lists the namespaces with their source type. |
| `list_flows` | viewer | no | Lists the flows of a namespace and its children. |
| `get_flow` | viewer | no | Reads one flow with its YAML source. |
| `validate_flow` | viewer | no | Validates the content of a flow or `namespace.yaml` against the head version. |
| `list_files` | viewer | no | Lists the files of the head version of a namespace. |
| `read_file` | viewer | no | Reads one file of the head version. |
| `list_executions` | viewer | no | Lists recent executions. Filters: namespace, flow as `<namespace>/<flow_id>`, states. At most 50. |
| `get_execution` | viewer | no | Reads one execution with its task runs. |
| `get_logs` | viewer | no | Reads the last log lines, optionally of one task. `tail` is 1 to 1000, default 200. |
| `get_metrics` | viewer | no | Reads the metrics of an execution. |
| `get_insight` | viewer | no | Reads the latest triage of an execution. |
| `trigger_execution` | operator | yes | Starts a flow with inputs and labels. |
| `cancel_execution` | operator | yes | Cancels a running execution. |
| `propose_change` | editor | no | Validates proposed files and returns the issues and a diff. It writes nothing. Assistant only. |
| `apply_change` | editor | yes | Applies file changes: a new version of a managed namespace, or a new branch of a git namespace. It refuses invalid flows. |

**Masking.** Execution results (`get_execution`, `get_logs`, `get_metrics`, `get_insight`) are masked with the secret values of every task run of the execution. A secret shows as `***`. The tests `SCN-AI-003` and `SCN-SEC-010` prove that no tool result and no model request holds a secret value.

**Size.** A tool result longer than 20 000 characters is cut.

## MCP server

`/mcp` serves MCP over streamable HTTP. It is stateless and answers with JSON.

- It accepts only a **bearer API token**. A session cookie gets `401`. Create a token on **Settings → API tokens**.
- The tools run with the role of the token. A call without the role of the tool is a tool error `forbidden`.
- A mutating tool runs at once, with no confirmation, and writes the audit event `ai.tool.call`. Give an MCP client a token with the lowest role that it needs.
- `propose_change` is not served. MCP clients use `validate_flow` and `apply_change`.

Connect Claude Code:

```sh
claude mcp add --transport http sluice http://localhost:8080/mcp \
  --header "Authorization: Bearer <api-token>"
```

The test `SCN-AI-003` proves the tool list, the permission error of a viewer token, `apply_change` with an editor token and the masked log result.

## Assistant

Click **Assistant** at the bottom right of any page. The panel keeps your conversations. Other users do not see them.

- The assistant gets only the tools that your role allows. A viewer gets the read tools.
- A turn stops after **20 model answers** with tool calls, with the error `step_limit_reached`. The test `SCN-AI-009` proves the limit.
- A **mutating** tool call does not run. The panel shows it with **Confirm** and **Reject**:
  - Confirm runs the tool as you and writes the audit event `ai.action.confirmed`. The tool also writes its own audit event, for example the creation of an execution, with you as the actor.
  - Reject writes `ai.action.rejected`. The model gets the rejection and continues.
  - You cannot send a new message while an action waits.

  The test `SCN-AI-005` proves both paths.
- After a page reload the conversation is still there. The test `SCN-AI-004` proves it.

### Flow authoring

Ask the assistant to write or change a flow. The assistant calls `propose_change`. The panel shows the diff of each file and the validation issues. When a proposal is invalid, the model gets the issues and can try again. After **3 invalid proposals** in one turn, the tool answers `retry_limit_reached`. When the proposal is valid, the assistant calls `apply_change`, which you confirm:

- A managed namespace gets a new version.
- A git namespace gets a new branch `sluice/<user>/<time>` from the last synced commit. The tracked branch does not change.

The test `SCN-AI-006` proves an invalid proposal, the diff, a version and a branch.

## Failure triage

A triage explains why an execution failed. It has a summary, a probable cause, evidence lines, a suggested fix and a confidence (`low`, `medium`, `high`).

- **On demand:** an operator clicks **Triage** on the page of a FAILED or TIMED_OUT execution. Another execution state answers `409 not_failed`. A second request while a triage runs does not start a new one.
- **Automatic:** with "Triage automatically" on, the end of every FAILED and TIMED_OUT execution queues a triage.

Every instance takes queued triages. A triage has 3 minutes. A running triage older than 15 minutes, for example of a stopped instance, becomes `failed`.

**Context.** The model gets:

- the flow source and the spec of the failed task;
- the execution error, the exit code, the outputs and the metrics;
- the first 50 and the last 400 log lines of the failed attempt;
- the file diff against the last SUCCESS execution of the flow, at most 200 lines;
- the durations of the last 10 executions.

All text is masked.

**Context limit.** The system text and the user text together stay within `SLUICE_AI_MAX_CONTEXT_CHARS` (default 120 000). The facts, the flow source and the diff get at most half of the limit. The log lines get the rest: a quarter for the first lines and the remainder for the last lines. Cut lines show as `…(N lines omitted)`. The test `SCN-AI-008` proves a 5 000-character limit with 50 000 log lines.

**Evidence filter.** An evidence line whose text is not in a log line of the failed task is removed. The line number is corrected to the first line with the text. The test `SCN-AI-007` proves the fields, the filter and the diff in the request.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `SLUICE_AI_MAX_CONTEXT_CHARS` | `120000` | The limit of the triage context in characters. |

All other settings are on the settings page, so every instance uses the same provider.

## Audit events

| Action | When |
|---|---|
| `ai.provider.update` | An admin saves the provider. |
| `ai.provider.delete` | An admin removes the provider. |
| `ai.triage.request` | An operator requests a triage. |
| `ai.action.confirmed` | A user confirms a mutating assistant action. |
| `ai.action.rejected` | A user rejects a mutating assistant action. |
| `ai.tool.call` | An MCP client runs a mutating tool. |

## API

| Operation | Role | Purpose |
|---|---|---|
| `GET /api/v1/ai/status` | viewer | Whether AI is on, and whether triage is automatic. |
| `GET`, `PUT`, `DELETE /api/v1/ai/provider` | admin | Read, save and remove the provider. |
| `POST /api/v1/ai/provider/test` | admin | Test the provider. |
| `GET /api/v1/ai/conversations` | viewer | Your conversations. |
| `POST /api/v1/ai/conversations` | viewer | Start a conversation. |
| `GET`, `DELETE /api/v1/ai/conversations/{id}` | viewer | Read or delete a conversation with its messages and actions. |
| `POST /api/v1/ai/conversations/{id}/messages` | viewer | Send a message. The answer is a server-sent event stream. |
| `POST /api/v1/ai/conversations/{id}/actions/{actionId}/confirm` | viewer | Confirm an action. The stream continues the turn. The tool still needs its role. |
| `POST /api/v1/ai/conversations/{id}/actions/{actionId}/reject` | viewer | Reject an action. The stream continues the turn. |
| `GET /api/v1/executions/{id}/insights` | viewer | The triages of an execution, newest first. |
| `POST /api/v1/executions/{id}/insights` | operator | Request a triage. |

The stream of a turn has these events:

| Event | Data |
|---|---|
| `text` | `{"delta"}`: a piece of the answer. |
| `tool_call` | `{"id", "name", "input", "mutating"}` |
| `tool_result` | `{"id", "name", "is_error", "text"}`. The text is cut at 4 000 characters. |
| `pending_action` | The action that waits for confirm or reject. |
| `error` | `{"code", "message"}`, for example `provider_error` or `step_limit_reached`. |
| `done` | `{"stop"}`: `end_turn`, `pending_action`, `step_limit_reached` or `error`. |

`api/openapi.yaml` holds the full schemas.

## Tests

The scripted LLM servers of `internal/testutil/llmserver` stand in for the providers in every test. They speak both APIs and record each request. `tests/fixtures/llmserver` is the same substitute as a program with a control API, for the Playwright tests.

| Test | Proves |
|---|---|
| `SCN-AI-001` | No provider: `409 ai_disabled`, hidden UI, working MCP. |
| `SCN-AI-002` | Both adapters: streaming, tool calls, JSON output, retries. |
| `SCN-AI-003` | MCP tools, permissions, `apply_change`, masked logs. |
| `SCN-AI-004` | The assistant panel shows tool calls and keeps conversations. |
| `SCN-AI-005` | Confirm and reject of a mutating call, with audit. |
| `SCN-AI-006` | Flow authoring on managed and git namespaces. |
| `SCN-AI-007` | Automatic triage, evidence filter, diff in the request. |
| `SCN-AI-008` | The context limit keeps head and tail lines. |
| `SCN-AI-009` | The 20-step limit. |
| `SCN-SEC-010` | No secret value reaches a model request. |
