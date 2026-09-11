# HTTP API

This document holds the reference of the Sluice HTTP API. `README.md` links here. The design is in SDD §7.12, Appendix B and Appendix C, and in decision D-09.

## Overview

| Path | Purpose | Authentication |
|---|---|---|
| `/api/v1/*` | The API of the UI, the CLI and scripts. | Session cookie or bearer API token |
| `/api/runner/v1/*` | The runner protocol of `sluice exec`. | Bearer run token |
| `/hooks/{key}`, `/hooks/git/{sourceId}` | Webhooks from other systems. | The key in the path, or a signature |
| `/mcp` | MCP server for AI clients. | Bearer API token |
| `/healthz`, `/readyz`, `/metrics` | Health and Prometheus. See [operations/runbook.md](operations/runbook.md#health) and [operations/metrics.md](operations/metrics.md). | None |

Every response carries an `X-Request-Id` header. The server log line of the request has the same ID. Every response also carries the security headers of SI-12: `Content-Security-Policy: default-src 'self'; frame-ancestors 'none'`, `X-Content-Type-Options: nosniff` and `Referrer-Policy: strict-origin-when-cross-origin`.

## OpenAPI document

The API is code-first (D-09). Each feature registers its operations with huma on the chi router. `sluice openapi` prints the OpenAPI 3.1 document of all `/api/v1`, `/api/runner/v1` and `/hooks` operations. The file [`api/openapi.yaml`](../api/openapi.yaml) holds its output and is committed. The server does not serve the document.

```sh
sluice openapi > api/openapi.yaml
```

`just gen` writes the file, and `just gen-check` fails when the file differs from the code. `TestSCN_API_001_GeneratedSpecMatches` compares the file with the generated document.

## Authentication

### Session cookie

`POST /api/v1/auth/login` with an email and a password sets the cookie `sluice_session`. The cookie is `HttpOnly` and `SameSite=Lax`, and `Secure` when `SLUICE_PUBLIC_URL` is https. The session lifetime slides with each request, up to `SLUICE_SESSION_TTL` after the last request. `POST /api/v1/auth/logout` deletes the session.

A cookie request with an unsafe method, that is not `GET`, `HEAD` or `OPTIONS`, must come from the same origin (SI-06, DI-12). The server accepts the request when one of these conditions is true:

- The `Origin` header equals the origin of `SLUICE_PUBLIC_URL`.
- The host of the `Origin` header equals the request host.
- The request has no `Origin` header and has `Sec-Fetch-Site: same-origin`.

Any other cookie request gets 403 `csrf_failed`. A request with neither header also gets 403. Browsers send one of the headers. `TestSCN_AUTH_011_SameOriginForCookies` proves this rule.

```console
$ curl -s -X POST -b jar -H 'Origin: https://evil.example' \
    http://localhost:18082/api/v1/executions/01a0914a-03b3-7219-8a00-0fef91bd6e87/rerun
{"error":{"code":"csrf_failed","message":"cross-origin request rejected"}}
```

Login is rate limited in Postgres. Ten failures for one email or 50 failures from one IP within 15 minutes return 429 with `Retry-After` (REQ-AUTH-008, `TestSCN_AUTH_008_LoginRateLimit`).

### Bearer API token

Scripts, the CLI and MCP clients use an API token in the `Authorization` header:

```
Authorization: Bearer slu_…
```

A token is `slu_` and 43 base62 characters. The server shows the token once, in the `secret` field of the create response, and stores only its SHA-256 hash (SI-02). A token has a name, a role and an optional expiry of at most 365 days. The role of a token cannot be higher than the role of its owner. The effective role is the lower of the token role and the current role of the owner (DI-14). Bearer requests need no `Origin` header.

Create a token in the UI at **Settings → API tokens**, or with the API:

```console
$ curl -s -b jar -H 'Content-Type: application/json' -H 'Origin: http://localhost:18082' \
    -d '{"name":"ci","role":"operator","expires_in_days":30}' \
    http://localhost:18082/api/v1/tokens
{"token":{"id":"01a0914f-…","name":"ci","prefix":"slu_GO…","role":"operator",…},"secret":"slu_GO…"}
```

Check which principal a token gives:

```console
$ curl -s -H "Authorization: Bearer $SLUICE_TOKEN" http://localhost:18082/api/v1/auth/me
{"id":"01a09149-…","email":"admin@local.test","name":"Admin","role":"operator","must_change_password":false,"auth_type":"token"}
```

A revoked token, an expired token and a token of a disabled user get 401. These changes take effect on all instances within 5 s (REQ-AUTH-009).

### Roles

The roles are `viewer < operator < editor < admin` (REQ-AUTH-006). Each operation has one minimum role. A higher role can call all operations of a lower role.

| Role | Adds these operations |
|---|---|
| `viewer` | Read dashboards, flows, files, executions, logs, metrics, variables and secret keys. Use the AI assistant with read tools. Manage the own profile, password and tokens. |
| `operator` | Trigger, cancel, rerun and restart executions. Run files. Request a triage. Git "Sync now". |
| `editor` | Edit managed files, push branches, enable and disable flows, rotate webhook keys. Write namespace secrets and variables, check namespace secrets. Create managed namespaces. List secret providers. |
| `admin` | Users, all tokens, global secrets and variables, secret providers, git sources, storage status, AI provider, instances, audit log, delete namespaces. |

A user with a temporary password gets 403 `password_change_required` on every operation except the own profile, password and logout operations. The full permission matrix is SDD Appendix B.

## Errors

Every error uses one envelope (REQ-API-002):

```json
{"error": {"code": "execution_not_found", "message": "execution not found", "details": null}}
```

| Status | Code | Cause |
|---|---|---|
| 401 | `unauthorized` | No credential, or a credential that is not valid. |
| 403 | `forbidden` | The role is too low. |
| 403 | `csrf_failed` | A cookie request failed the same-origin rule. |
| 403 | `password_change_required` | The user must set a new password first. |
| 404 | `not_found` | An unknown API route. The body is JSON. |
| 422 | `validation_failed` | The request does not match the schema. `details` lists `{field, message}`. |
| 429 | `rate_limited` | Too many login failures. `Retry-After` gives the seconds to wait. |
| 500 | `internal` | An unexpected error. The server log has the cause with the request ID. |

Features add their own codes, for example `execution_not_found`, `flow_invalid`, `flow_disabled`, `namespace_read_only`, `last_admin`, `builtin_provider_disabled` and `ai_disabled`. The OpenAPI document lists the error response of each operation as `ErrorEnvelope`.

The server checks the access of an operation before it reads the request. A caller without permission never sees validation details (SI-03).

### `validation_failed`

A request that fails the schema gets 422 with one entry for each field:

```console
$ curl -s -H "Authorization: Bearer $SLUICE_TOKEN" -H 'Content-Type: application/json' \
    -d '{"labels":"x"}' http://localhost:18082/api/v1/flows/sales/weekly-report/executions
{"error":{"code":"validation_failed","message":"validation failed","details":[{"field":"labels","message":"expected object"}]}}
```

A body that is not JSON gets the field `body` with the message `invalid JSON`. Query and path parameters use the parameter name as the field. `TestSCN_API_002_ErrorEnvelope` proves the envelope and the JSON 404:

```console
$ curl -s -H "Authorization: Bearer $SLUICE_TOKEN" http://localhost:18082/api/v1/nope
{"error":{"code":"not_found","message":"unknown API route GET /api/v1/nope"}}
```

## Pagination

List operations use cursor pagination (REQ-API-003). The response has `items` and, when more rows follow, `next_cursor`. Send the cursor back as the `cursor` query parameter.

| Parameter | Rule |
|---|---|
| `limit` | 1 to 200. The default is 50. A value above 200 gets 422. |
| `cursor` | The opaque `next_cursor` of the previous page. A cursor that does not decode gets 422 with the field `cursor`. |

The cursor holds the sort key of the last row. New rows do not move a page, so no row repeats or is lost between pages (`TestSCN_API_003_StablePagination`).

```console
$ curl -s -H "Authorization: Bearer $SLUICE_TOKEN" "http://localhost:18082/api/v1/executions?limit=2"
{"items":[{"id":"01a0914a-1601-…","namespace":"sales","flow_id":"weekly-report","state":"SUCCESS",…},…],"next_cursor":"MjAyNi0wOS0x…"}

$ curl -s -H "Authorization: Bearer $SLUICE_TOKEN" \
    "http://localhost:18082/api/v1/executions?limit=2&cursor=MjAyNi0wOS0x…"

$ curl -s -H "Authorization: Bearer $SLUICE_TOKEN" "http://localhost:18082/api/v1/executions?limit=500"
{"error":{"code":"validation_failed","message":"validation failed","details":[{"field":"limit","message":"expected number <= 200"}]}}
```

The log page operation `GET /api/v1/executions/{executionId}/logs` uses its own `limit` from 1 to 5000, with a default of 1000.

## Operations

All operations are in [`api/openapi.yaml`](../api/openapi.yaml) with their request and response schemas. The table gives the path, the operation ID and the minimum role. "Public" operations need no credential. "Self" operations need any logged-in user, also one with a temporary password.

### Authentication, users and tokens

| Method and path | Operation | Access |
|---|---|---|
| `POST /api/v1/auth/login` | `login` | public |
| `POST /api/v1/auth/logout` | `logout` | self |
| `GET /api/v1/auth/me`, `PATCH /api/v1/auth/me` | `getMe`, `updateMe` | self |
| `POST /api/v1/auth/password` | `changePassword` | self |
| `POST /api/v1/auth/sessions/revoke-others` | `revokeOtherSessions` | self |
| `GET /api/v1/tokens`, `POST /api/v1/tokens` | `listTokens`, `createToken` | viewer |
| `DELETE /api/v1/tokens/{tokenId}` | `revokeToken` | viewer |
| `GET /api/v1/users`, `POST /api/v1/users` | `listUsers`, `createUser` | admin |
| `PATCH /api/v1/users/{userId}` | `updateUser` | admin |
| `POST /api/v1/users/{userId}/reset-password` | `resetUserPassword` | admin |
| `GET /api/v1/audit` | `listAuditEvents` | admin |

`listTokens` returns the tokens of the caller. An admin adds `all=true` to list the tokens of all users. The owner of a token and an admin can revoke it.

### Namespaces and files

| Method and path | Operation | Access |
|---|---|---|
| `GET /api/v1/namespaces` | `listNamespaces` | viewer |
| `POST /api/v1/namespaces` | `createNamespace` | editor |
| `GET /api/v1/namespaces/{namespace}` | `getNamespace` | viewer |
| `DELETE /api/v1/namespaces/{namespace}` | `deleteNamespace` | admin |
| `GET /api/v1/namespaces/{namespace}/files` | `listFiles` | viewer |
| `GET /api/v1/namespaces/{namespace}/file` | `getFile` | viewer |
| `PUT /api/v1/namespaces/{namespace}/file` | `uploadFile` | editor |
| `POST /api/v1/namespaces/{namespace}/changes` | `saveChanges` | editor |
| `GET /api/v1/namespaces/{namespace}/versions` | `listVersions` | viewer |
| `GET /api/v1/namespaces/{namespace}/diff` | `diffVersions` | viewer |
| `POST /api/v1/namespaces/{namespace}/revert` | `revertVersion` | editor |
| `POST /api/v1/namespaces/{namespace}/validate` | `validateFile` | viewer |
| `POST /api/v1/namespaces/{namespace}/run` | `runFile` | operator |
| `GET /api/v1/namespaces/{namespace}/git` | `getNamespaceGit` | viewer |
| `POST /api/v1/namespaces/{namespace}/git/push` | `pushNamespaceBranch` | editor |

### Flows and schedules

| Method and path | Operation | Access |
|---|---|---|
| `GET /api/v1/flows` | `listFlows` | viewer |
| `GET /api/v1/flows/{namespace}/{flowId}` | `getFlow` | viewer |
| `PATCH /api/v1/flows/{namespace}/{flowId}` | `updateFlow` | editor |
| `GET /api/v1/flows/{namespace}/{flowId}/revisions` | `listFlowRevisions` | viewer |
| `GET /api/v1/flows/{namespace}/{flowId}/revisions/{revisionId}` | `getFlowRevision` | viewer |
| `GET /api/v1/flows/{namespace}/{flowId}/diff` | `diffFlowRevisions` | viewer |
| `POST /api/v1/flows/{namespace}/{flowId}/executions` | `triggerFlow` | operator |
| `GET /api/v1/flows/{namespace}/{flowId}/stats` | `getFlowStats` | viewer |
| `GET /api/v1/flows/{namespace}/{flowId}/metrics` | `getFlowMetrics` | viewer |
| `POST /api/v1/flows/{namespace}/{flowId}/triggers/{triggerId}/webhook-key` | `rotateWebhookKey` | editor |
| `GET /api/v1/schedules/upcoming` | `listUpcomingSchedules` | viewer |
| `GET /api/v1/schemas/flow.json` | `getFlowSchema` | public |

### Executions

| Method and path | Operation | Access |
|---|---|---|
| `GET /api/v1/executions` | `listExecutions` | viewer |
| `GET /api/v1/executions/{executionId}` | `getExecution` | viewer |
| `POST /api/v1/executions/{executionId}/cancel` | `cancelExecution` | operator |
| `POST /api/v1/executions/{executionId}/rerun` | `rerunExecution` | operator |
| `POST /api/v1/executions/{executionId}/restart` | `restartExecution` | operator |
| `GET /api/v1/executions/{executionId}/events` | `streamExecutionEvents` | viewer |
| `GET /api/v1/executions/{executionId}/logs` | `getExecutionLogs` | viewer |
| `GET /api/v1/executions/{executionId}/logs/stream` | `streamExecutionLogs` | viewer |
| `GET /api/v1/executions/{executionId}/logs/download` | `downloadExecutionLogs` | viewer |
| `GET /api/v1/executions/{executionId}/metrics` | `listExecutionMetrics` | viewer |
| `GET /api/v1/executions/{executionId}/artifacts` | `listExecutionArtifacts` | viewer |
| `GET /api/v1/executions/{executionId}/artifacts/{artifactId}` | `downloadArtifact` | viewer |
| `GET /api/v1/executions/{executionId}/insights` | `listExecutionInsights` | viewer |
| `POST /api/v1/executions/{executionId}/insights` | `requestTriage` | operator |

`listExecutions` accepts these filter parameters:

| Parameter | Value |
|---|---|
| `state` | A comma-separated list of execution states. |
| `namespace` | A namespace. The filter includes its children. |
| `flow` | `<namespace>/<flow_id>` |
| `trigger_type` | `manual`, `schedule`, `webhook`, `flow`, `file`, `subflow`, `rerun` or `restart`. |
| `label` | `key=value`. Repeat the parameter for more labels. |
| `from`, `to` | RFC 3339 times. |
| `sort` | `created`, the default, or `duration`. |

```console
$ curl -s -H "Authorization: Bearer $SLUICE_TOKEN" \
    "http://localhost:18082/api/v1/executions?state=FAILED&limit=5"
```

Trigger a flow with inputs and labels. The response is 201 with the execution detail:

```console
$ curl -s -H "Authorization: Bearer $SLUICE_TOKEN" -H 'Content-Type: application/json' \
    -d '{"inputs":{"full_refresh":true},"labels":{"team":"data"}}' \
    http://localhost:18082/api/v1/flows/sales/nightly-load/executions
```

The execution actions have these results:

| Action | Success | Error |
|---|---|---|
| Cancel | 202 with the execution. | 409 `execution_active` when the execution already ended. See the note below the table. |
| Rerun | 201 with a new execution that has the same snapshot, definition, inputs and labels. | 404 `execution_not_found`. |
| Restart from failed | 201 with a new execution. `SUCCESS` task runs are copied with reason `reused`. | 409 `not_restartable` unless the old execution is `FAILED`, `TIMED_OUT` or `CANCELLED`. |

> **Note:** A cancel of an ended execution returns `{"error":{"code":"execution_active","message":"the execution has not ended","details":{"state":"FAILED"}}}`. The execution is not active. Read `details.state` to see the real state.

### Secrets and variables

| Method and path | Operation | Access |
|---|---|---|
| `GET /api/v1/secrets` | `listGlobalSecrets` | viewer |
| `PUT /api/v1/secrets/{key}`, `DELETE /api/v1/secrets/{key}` | `putGlobalSecret`, `deleteGlobalSecret` | admin |
| `POST /api/v1/secrets/{key}/check` | `checkGlobalSecret` | admin |
| `GET /api/v1/namespaces/{namespace}/secrets` | `listNamespaceSecrets` | viewer |
| `PUT /api/v1/namespaces/{namespace}/secrets/{key}`, `DELETE …` | `putNamespaceSecret`, `deleteNamespaceSecret` | editor |
| `POST /api/v1/namespaces/{namespace}/secrets/{key}/check` | `checkNamespaceSecret` | editor |
| `GET /api/v1/variables` | `listGlobalVariables` | viewer |
| `PUT /api/v1/variables/{key}`, `DELETE /api/v1/variables/{key}` | `putGlobalVariable`, `deleteGlobalVariable` | admin |
| `GET /api/v1/namespaces/{namespace}/variables` | `listNamespaceVariables` | viewer |
| `PUT /api/v1/namespaces/{namespace}/variables/{key}`, `DELETE …` | `putNamespaceVariable`, `deleteNamespaceVariable` | editor |
| `GET /api/v1/secret-providers` | `listSecretProviders` | editor |
| `POST /api/v1/secret-providers` | `createSecretProvider` | admin |
| `PUT /api/v1/secret-providers/{name}`, `DELETE …` | `updateSecretProvider`, `deleteSecretProvider` | admin |
| `POST /api/v1/secret-providers/{name}/check` | `checkSecretProvider` | admin |

Secret values are write-only. No response contains a value. See [secrets-and-variables.md](secrets-and-variables.md).

### Git sources

| Method and path | Operation | Access |
|---|---|---|
| `GET /api/v1/git-sources`, `POST /api/v1/git-sources` | `listGitSources`, `createGitSource` | admin |
| `GET`, `PUT`, `DELETE /api/v1/git-sources/{sourceId}` | `getGitSource`, `updateGitSource`, `deleteGitSource` | admin |
| `GET /api/v1/git-sources/{sourceId}/runs` | `listGitSyncRuns` | viewer |
| `POST /api/v1/git-sources/{sourceId}/sync` | `syncGitSource` | operator |

See [git-sync.md](git-sync.md).

### Settings, dashboard and AI

| Method and path | Operation | Access |
|---|---|---|
| `GET /api/v1/stats/dashboard` | `getDashboard` | viewer |
| `GET /api/v1/instances` | `listInstances` | admin |
| `GET /api/v1/storage` | `getStorageStatus` | admin |
| `GET /api/v1/ai/status` | `getAIStatus` | viewer |
| `GET`, `PUT`, `DELETE /api/v1/ai/provider` | `getAIProvider`, `putAIProvider`, `deleteAIProvider` | admin |
| `POST /api/v1/ai/provider/test` | `testAIProvider` | admin |
| `GET /api/v1/ai/conversations`, `POST …` | `listAIConversations`, `createAIConversation` | viewer |
| `GET`, `DELETE /api/v1/ai/conversations/{conversationId}` | `getAIConversation`, `deleteAIConversation` | viewer |
| `POST /api/v1/ai/conversations/{conversationId}/messages` | `sendAIMessage` | viewer |
| `POST …/actions/{actionId}/confirm`, `POST …/actions/{actionId}/reject` | `confirmAIAction`, `rejectAIAction` | viewer |

The dashboard fields are in [operations/metrics.md](operations/metrics.md#dashboard-kpis). The AI operations are in [ai.md](ai.md#api).

## Server-sent events

Three operations stream `text/event-stream` (REQ-API-004):

| Operation | Events | `id` of an event |
|---|---|---|
| `GET /api/v1/executions/{executionId}/events` | `execution` with the execution detail, each time the state of the execution or of a task run changes. | A hash of the states. |
| `GET /api/v1/executions/{executionId}/logs/stream` | `line` with one log line. The optional `task` query parameter filters by task ID. | The read position of each task run. |
| `POST /api/v1/ai/conversations/{conversationId}/messages` | The assistant events. See [ai.md](ai.md#assistant). | none |

The execution streams send `event: end` when the execution ended and no new data follows, and then close. They send a `: keep-alive` comment after about 15 s without data.

To resume a stream, send the last `id` that you received in the `Last-Event-ID` header. The log stream then sends only the lines after that position. `TestSCN_API_004_LogStreamResume` proves that no line repeats and no line is lost.

```console
$ curl -s -N -H "Authorization: Bearer $SLUICE_TOKEN" \
    http://localhost:18082/api/v1/executions/01a0914a-03b3-7219-8a00-0fef91bd6e87/logs/stream
id: eyIwMWEwOTE0YS0wNjFiLTc1NDAtOWIzOS1lNGU2ZTY4MTYwYmIiOjF9
event: line
data: {"task_run_id":"01a0914a-061b-…","task_key":"post","attempt":1,"n":1,"ts":"2026-09-11T16:25:43.987704131Z","stream":"stdout","text":"posting alert with token ***"}

id: eyIwMWEwOTE0YS0wNjFiLTc1NDAtOWIzOS1lNGU2ZTY4MTYwYmIiOjJ9
event: line
data: {"task_run_id":"01a0914a-061b-…","task_key":"post","attempt":1,"n":2,…,"stream":"stderr","text":"ERROR: connection refused to hooks.example.com:443"}

event: end
data: {}

$ curl -s -N -H "Authorization: Bearer $SLUICE_TOKEN" \
    -H 'Last-Event-ID: eyIwMWEwOTE0YS0wNjFiLTc1NDAtOWIzOS1lNGU2ZTY4MTYwYmIiOjF9' \
    http://localhost:18082/api/v1/executions/01a0914a-03b3-7219-8a00-0fef91bd6e87/logs/stream
id: eyIwMWEwOTE0YS0wNjFiLTc1NDAtOWIzOS1lNGU2ZTY4MTYwYmIiOjJ9
event: line
data: {…,"n":2,…}

event: end
data: {}
```

The browser `EventSource` sends `Last-Event-ID` itself when it reconnects. The UI uses the streams to show state changes within 2 s (REQ-UI-012).

## Webhooks

| Method and path | Purpose | Result |
|---|---|---|
| `POST /hooks/{key}` | Starts the flow of a webhook trigger. The body is at most 1 MiB. | 202 with `{"execution_id": …}`. |
| `POST /hooks/git/{sourceId}` | Requests a sync of a git source from a push. | See [git-sync.md](git-sync.md). |

A webhook key has 256 random bits. The server stores only its hash and compares it in constant time (SI-05). An editor rotates the key with `rotateWebhookKey`, and the response shows the key and the URL once (DI-28). A trigger has no key until the first rotation.

| Case | Status and code |
|---|---|
| Wrong or rotated key | 404 `not_found` |
| Body above 1 MiB | 413 `body_too_large` |
| Flow disabled | 409 `flow_disabled` |
| Flow invalid | 422 `flow_invalid` |

```console
$ curl -s -X POST -d '{}' http://localhost:18082/hooks/wrong-key
{"error":{"code":"not_found","message":"webhook not found"}}
```

The flow gets the body as `trigger.body` and the headers as `trigger.headers`. The server does not store `Authorization`, `Cookie` and `Proxy-Authorization`. `TestSCN_TRG_005_Webhook` proves this behaviour. See [triggers.md](triggers.md).

## Runner API

The runner API is SDD Appendix C. Only `sluice exec` calls it. Its base path is `/api/runner/v1`.

| Method and path | Operation | Purpose |
|---|---|---|
| `GET /task-runs/{id}/spec` | `runnerGetSpec` | Command, workdir, resolved env, mask values and limits. |
| `GET /task-runs/{id}/bundle` | `runnerGetBundle` | The snapshot bundle as `tar.gz`. |
| `POST /task-runs/{id}/logs` | `runnerPostLogs` | `{seq, lines:[{ts, stream, text}]}` |
| `POST /task-runs/{id}/events` | `runnerPostEvents` | `{seq, events:[…]}` with outputs and metrics. |
| `PUT /task-runs/{id}/artifacts/{name}` | `runnerPutArtifact` | A streamed artifact body. |
| `POST /task-runs/{id}/heartbeat` | `runnerHeartbeat` | Returns `{cancel: bool}`. |
| `POST /task-runs/{id}/complete` | `runnerComplete` | `{exit_code, error}` |

Each request carries the run token of its task run as `Authorization: Bearer <token>`. The dispatcher creates the token at the claim and gives it to the runner in `SLUICE_RUN_TOKEN`. The run token rules (SI-04):

- The token is valid only while the task run is `RUNNING`.
- The token expires at the task timeout plus 10 minutes.
- The server deletes the token hash when the task run ends.
- A token of another task run gets 403.
- A token that is not valid, expired or revoked gets 401.

```console
$ curl -s -H 'Authorization: Bearer nope' \
    http://localhost:18082/api/runner/v1/task-runs/01a0914a-061b-7540-9b39-e4e6e68160bb/spec
{"error":{"code":"unauthorized","message":"authentication required"}}
```

`TestSCN_RUN_010_RunTokens` proves these rules. The protocol limits are in [architecture.md](architecture.md#log-ingest-and-archive) and SDD §7.8.

## MCP

`/mcp` serves the Model Context Protocol over stateless streamable HTTP with JSON answers. It accepts only bearer API tokens. The token role limits the tools that a client can call. A request without a token gets 401 `unauthenticated`.

```console
$ curl -s -X POST -H "Authorization: Bearer $SLUICE_TOKEN" -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' \
    -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' http://localhost:18082/mcp
```

The tools, the client setup and the audit events are in [ai.md](ai.md#mcp-server).

## Related documents

- [architecture.md](architecture.md): components and the execution lifecycle.
- [operations/security.md](operations/security.md): security configuration.
- [reference/env.md](reference/env.md): environment variables.
