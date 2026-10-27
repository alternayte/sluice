# Assistant background turns

## What it does
An assistant turn runs on the server, apart from the browser. A refresh, a closed tab or a second tab attaches to the running turn and shows its text as it streams. A user stops a running turn, retries a failed turn, and copies messages and tool arguments. A conversation has an accept-all switch that runs mutating tool calls without a confirm. An admin gives the assistant web access through a `fetch_url` tool.

This spec replaces the transport of REQ-AI-005 in `docs/sluice-sdd.md`: the POST no longer carries the event stream. The streaming, the 20-step limit and the confirm of mutating calls stay, except where accept-all applies.

## Decisions
- `POST /api/v1/ai/conversations/{id}/messages` and `POST …/actions/{actionId}/confirm|reject` start the turn in a goroutine and return 202. The turn context comes from the server, not the request. Reason: a closed request must not cancel the model call.
- `ai_conversations` gets `turn_status` (idle, running, failed), `turn_error`, `draft_text`, `turn_heartbeat_at` and `auto_accept boolean NOT NULL DEFAULT false`. Reason: every instance must see the state of a turn.
- A turn starts with a conditional update from idle or failed to running, and the update replaces the in-memory `sync.Map` lock. A second start gives 409 `turn_running`. Reason: the lock must hold across instances.
- While text streams, the turn writes `draft_text` and `turn_heartbeat_at` at most every 250 ms. A saved assistant message clears `draft_text`. Reason: an attached browser reads the text from Postgres.
- `GET /api/v1/ai/conversations/{id}/events` is an SSE stream. It polls every 250 ms and sends a `conversation` event with the conversation detail and the turn fields each time they change. It ends with `end` when the turn is no longer running. Reason: this is the pattern of `/executions/{id}/events`, and it works on every instance.
- A running turn whose heartbeat is older than 60 s becomes failed with the error `the turn stopped before it ended`. Each instance resets stale turns on its existing tick. Reason: a dead instance must not lock a conversation.
- `POST …/conversations/{id}/stop` asks a running turn to stop. The turn checks between text deltas and between steps. It saves the draft as the assistant message with the block `stopped: true` and ends idle. Reason: a background turn needs a way to stop it.
- `POST …/conversations/{id}/retry` runs the turn again from the saved messages, when `turn_status` is failed. It adds no user message. A `step_limit_reached` stop ends idle and gets no Retry. Reason: a failed step saves nothing, so the history ends at the last user message or tool result.
- `PATCH …/conversations/{id}` takes `{auto_accept}`. The drawer header shows the switch. With the switch on, a mutating call runs at once with the user as actor, inside the user's role. The audit log records `ai.action.auto_confirmed`, and the tool card shows "Auto-accepted". Reason: the turn runs on the server, so the server decides.
- After a `fetch_url` call in a turn, the mutating calls of that turn wait for a confirm, even with accept-all on. Reason: a fetched page is untrusted text and must not start an action alone.
- `fetch_url` is a read-only tool for both provider types. The provider settings get `web_access boolean` (default false) and `web_allowed_domains text[]`. An empty list allows every public domain. Reason: the admin decides whether the server reaches the web.
- `fetch_url` sends GET over http or https, follows at most 5 redirects and stops after 10 s. It refuses loopback, private, link-local, unique-local and metadata addresses after DNS resolution, on each redirect. It converts HTML to text and caps the result at 100 KB. Reason: the server must not reach its own network for the model.
- The UI shows a copy button on hover on each user message and each assistant text, and on the arguments of each tool card. Copy takes the raw markdown. A failed turn shows its error with a Retry button. A running turn shows a Stop button in place of Send. Reason: this is the action row of Claude and ChatGPT.
- MCP tool calls do not change. `fetch_url` is not an MCP tool. Reason: an MCP client has its own web access.

## Out
- Web search through a search API or a provider feature.
- A job queue library.
- Retry or edit of a successful answer.
- An accept-all setting for each user or for the instance.
- A turn that moves to another instance when its instance dies.

## How I know it works
- Send a message that makes the scripted model call two tools, and reload during the turn. The drawer shows the streamed text, the turn ends, and the answer stays after one more reload.
- Stop during the stream. The partial answer stays, marked as stopped, and a new message works.
- A provider 500 shows the error and Retry. After the provider recovers, Retry gives an answer with no second user message.
- With accept-all on, `trigger_execution` creates an execution without a confirm, and the audit log has `ai.action.auto_confirmed`. After a `fetch_url` in the same turn, `trigger_execution` waits for a confirm.
- With web access off, the model gets no `fetch_url` tool. With it on, `fetch_url` of `http://127.0.0.1` and of `http://169.254.169.254` returns an error, and a public page returns text.
- Copy on an assistant message puts its markdown on the clipboard.
