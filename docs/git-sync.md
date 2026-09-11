# Git sync

This document holds the reference of git sources, sync runs, git webhooks and the push of edits to a branch. `README.md` links here. The design is in SDD §7.11 (REQ-GIT-001 to REQ-GIT-007) and in decision DI-33 of [build/decisions.md](build/decisions.md).

A **git source** is one branch of one repository. A **mapping** connects a directory of that branch to a namespace. Sluice reads the branch, builds one snapshot per mapping and makes it the head of the namespace. Git namespaces are read-only in Sluice. An edit goes to a new branch in the repository, and the change reaches Sluice after you merge that branch.

| Term | Description |
|---|---|
| Git source | Repository URL, branch, credentials, poll interval, webhook secret and mappings. |
| Mapping | `repo_path → namespace`. One namespace has at most one mapping. |
| Sync run | One read of the branch head. Sluice records each run with its result. |
| Git namespace | A namespace with source type `git`. Its files come only from sync. |

## Create a git source

An admin creates git sources on **Settings → Git sources** (`/settings/git`) with **Add git source**, or through the API.

| Field | Rule |
|---|---|
| `name` | `^[a-z0-9][a-z0-9_-]{0,62}$`. Unique. It cannot change after creation. |
| `repo_url` | `https://…`, `ssh://…` or `user@host:path`. Plain `http://` is allowed only for `localhost` and loopback IP addresses. At most 2048 characters. |
| `branch` | The tracked branch. A valid branch name, at most 255 characters. |
| `auth_type` | `none`, `https_token` or `ssh_key`. |
| `credential_secret_key` | The key of a **global** secret with the token or the private key. Required unless `auth_type` is `none`. |
| `known_hosts` | Optional. Lines in OpenSSH `known_hosts` format for `ssh_key` sources. At most 65 536 characters. |
| `poll_interval` | Seconds between polls. 15 to 86 400. Default 60. |
| `webhook_secret_key` | Optional. The key of a **global** secret with the webhook secret. |
| `mappings` | 1 to 100 entries of `repo_path` and `namespace`. |

The API response holds the secret **keys**, never the secret values. It also holds `webhook_url`, `last_synced_sha`, `last_sync_at`, `last_sync_status` and `last_error`.

### Credentials

Sluice resolves `credential_secret_key` and `webhook_secret_key` in the global scope only. The secret can use any provider, for example `builtin`, `env` or `vault`. See [secrets-and-variables.md](secrets-and-variables.md).

| `auth_type` | URL | Credential | Behaviour |
|---|---|---|---|
| `none` | any allowed URL | none | Sluice clears `credential_secret_key`. |
| `https_token` | `https://` or loopback `http://` | A token, for example a GitHub or GitLab access token. | Sluice sends HTTP basic auth with the user `x-access-token` and the token as the password. |
| `ssh_key` | `ssh://` or `user@host:path` | A private key in PEM format. | The SSH user comes from the URL. Without a user in the URL, the user is `git`. Keys with a passphrase are not supported. |

**Host keys.** With `known_hosts`, Sluice checks the host key of the server against it. Without `known_hosts`, Sluice does not check the host key. Each sync run of such a source then records the warning `no known_hosts: the host key was not checked`. For production, always set `known_hosts`. You can get the lines with `ssh-keyscan <host>`.

### Mappings

- `repo_path` is a directory of the branch. An empty value or `.` is the repository root. Sluice removes a `/` at the start and at the end.
- `repo_path` must obey the path rules of namespace files: relative, at most 512 characters, no `..` segment.
- In one source, two mappings cannot use the same `repo_path` or the same namespace.
- A mapped namespace that does not exist is created as a git namespace.
- A mapping to a managed namespace returns `409 namespace_managed`. A mapping to a namespace of another source returns `409 namespace_mapped`. The test `SCN-GIT-007` proves both.

An update of a source replaces all mappings. A namespace that loses its mapping stays a read-only git namespace with its history, and it does not sync again. A delete of a source keeps its namespaces in the same way.

### Example

Create a token source that maps `pipelines/elt` to the namespace `data.elt`. The global secret `GIT_TOKEN` must exist.

```sh
curl -X POST http://localhost:8080/api/v1/git-sources \
  -H "Authorization: Bearer $SLUICE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "pipelines",
    "repo_url": "https://github.com/acme/pipelines.git",
    "branch": "main",
    "auth_type": "https_token",
    "credential_secret_key": "GIT_TOKEN",
    "poll_interval": 300,
    "webhook_secret_key": "GIT_WEBHOOK_SECRET",
    "mappings": [{"repo_path": "pipelines/elt", "namespace": "data.elt"}]
  }'
```

The answer is `201` with the source. The first sync starts within about one second, because the source has no sync yet. The test `SCN-GIT-001` proves this path: a snapshot with the commit SHA and the flows of the directory.

## Sync

### When a sync runs

The instance that holds the `git-sync` lease checks the sources every second. A source is due when one of these is true:

- It has never synced.
- A user or a webhook requested a sync.
- `poll_interval` seconds passed since the start of the last sync.

One check takes at most 20 due sources. A source syncs only once at a time. The claim checks the lease in the same statement, so two instances never sync one source together.

### What a sync does

1. Sluice creates a temporary directory below `<temp dir>/sluice-git`.
2. It fetches the head of the branch as a bare, shallow clone with depth 1 and no tags. It does not check out files.
3. For each mapping, it reads the regular files below `repo_path` from the git object store.
4. It compares the files with the head of the namespace. It creates a snapshot only when a file changed.
5. The snapshot message is `git <first 12 characters of the SHA>: <commit subject>`. The snapshot records the SHA.
6. Flows and triggers of the namespace refresh from the new snapshot.
7. Sluice records the sync run and deletes the temporary directory.

A new commit that changes only one mapping creates a snapshot for that mapping only. The test `SCN-GIT-003` proves this. After a sync the temporary directory is empty. The test `SCN-GIT-008` proves this.

A flow file that you remove in git makes the flow deleted and its triggers inactive. Old executions of the flow stay visible. The test `SCN-GIT-006` proves this.

### What a sync skips

Sluice reads only regular and executable files. It skips these entries and records a warning in the sync run:

| Entry | Warning |
|---|---|
| Symbolic link | `skipped symlink <path>` |
| Submodule | `skipped submodule <path>` |
| Path that fails the path rules, for example a `..` segment | `skipped <path>: <reason>` |
| Other file mode | `skipped <path>: unsupported file mode` |

Sluice never writes repository content to a file system, so a crafted tree cannot write outside the temporary directory. The test `SCN-NS-005` proves that a repository with a symlink and a traversal path syncs without those entries and records warnings.

A file larger than `SLUICE_MAX_FILE_BYTES` (default `10MiB`) is not skipped. It makes the sync fail.

### Failures

A failed sync sets the run status to `failed` and records the error on the run and in `last_error` of the source. The namespace keeps its previous snapshot as the head, so new executions use the last good files. `last_synced_sha` changes only after a successful sync. The test `SCN-GIT-002` proves that a wrong SSH key records an error and keeps the previous snapshot active.

Sluice handles the mappings one after another. When mapping B fails, a snapshot that the same run already created for mapping A stays the head of A.

A run that stays `running` for more than 30 minutes, for example because its instance stopped, becomes `failed` with the error `the sync stopped before it ended`. The source can then sync again.

### Sync runs

Each sync run has these fields:

| Field | Description |
|---|---|
| `started_at`, `ended_at` | Start and end of the run. |
| `sha` | The commit that the run read. |
| `status` | `running`, `success` or `failed`. |
| `error` | The error of a failed run. |
| `warnings` | Skipped entries and the `known_hosts` warning. |
| `snapshots_created` | The number of mappings that got a new snapshot. |

The API and the UI show the last 50 runs of a source, newest first. Sluice does not delete sync runs.

### Sync now

**Sync now** requests a sync at once. It is on **Settings → Git sources** and on the git panel of each git namespace. An operator or a higher role can use it.

```sh
curl -X POST http://localhost:8080/api/v1/git-sources/<source_id>/sync \
  -H "Authorization: Bearer $SLUICE_TOKEN"
```

The answer is `202`. The sync starts at the next check of the lease holder, normally within one second. The request writes the audit event `git_source.sync`. The test `SCN-GIT-005` proves that **Sync now** adds a sync run to the list.

## Webhooks

A webhook makes a push sync at once, so you can use a long poll interval.

- URL: `POST <SLUICE_PUBLIC_URL>/hooks/git/<source_id>`. The source shows it as `webhook_url`.
- The source must have `webhook_secret_key`. A source without it refuses every webhook call with `401 invalid_signature`.
- The body can be at most 1 MiB. A larger body returns `413 body_too_large`.

Sluice accepts one of two proofs:

| Header | Check |
|---|---|
| `X-Hub-Signature-256` | `sha256=` and the hex HMAC-SHA256 of the raw body, with the webhook secret as the key. GitHub sends this header. |
| `X-Sluice-Token` | The webhook secret itself. |

Sluice compares both in constant time.

| Request | Answer |
|---|---|
| Unknown source ID | `404 git_source_not_found` |
| No webhook secret on the source, or a wrong signature or token | `401 invalid_signature` |
| Valid, with `X-GitHub-Event: ping` | `202 {"queued":false}` |
| Valid, with a JSON body whose `ref` is not `refs/heads/<branch>` | `202 {"queued":false}` |
| Valid, all other requests | `202 {"queued":true}` and a sync request |

A body without a `ref` field requests a sync too. The test `SCN-GIT-004` proves that a valid HMAC webhook syncs within 5 s, that an invalid signature returns 401 and that a push to another branch does not sync.

### Connect GitHub

1. Create a global secret, for example `GIT_WEBHOOK_SECRET`, with a long random value.
2. Set `webhook_secret_key` of the source to `GIT_WEBHOOK_SECRET`.
3. In the GitHub repository, open **Settings → Webhooks → Add webhook**.
4. Set **Payload URL** to the `webhook_url` of the source.
5. Set **Content type** to `application/json`.
6. Set **Secret** to the value of `GIT_WEBHOOK_SECRET`.
7. Select the push event.

### Call from another system

A CI job or another git host can use the token header:

```sh
curl -X POST https://sluice.example.com/hooks/git/<source_id> \
  -H "X-Sluice-Token: $GIT_WEBHOOK_SECRET"
```

## Read-only git namespaces

A file write to a git namespace returns `409 namespace_read_only`. This applies to create, update, rename, delete and upload, in the UI and in the API. The test `SCN-NS-004` proves this. The namespace page shows the badge **Read-only**.

The only way to change the files is a commit in the repository, or **Push to branch**.

## Push to branch

An editor can edit a file of a git namespace in the editor and click **Push to branch**. The dialog asks for a **Commit message**. Sluice then:

1. Fetches the tracked branch.
2. Starts from the last synced commit, not from the newest commit of the branch.
3. Builds the new commit in memory, with the user as author and committer.
4. Pushes only the new branch `sluice/<user-slug>/<yyyymmdd-hhmmss>`.
5. Returns the branch name and the commit SHA.

| Part | Value |
|---|---|
| `<user-slug>` | The part of the user email before `@`, in lower case. Each run of other characters than `a-z` and `0-9` becomes one `-`. |
| `<yyyymmdd-hhmmss>` | The push time in UTC. |
| Author name | The user slug. |
| Author email | The user email. |

For example, `dana@example.com` at 14:03:07 UTC on 11 September 2026 pushes to `sluice/dana/20260911-140307`.

The tracked branch does not change, and the namespace does not change. Merge the branch, for example through a pull request. The next sync then brings the change into Sluice. The test `SCN-GIT-005` proves the new branch, the author and the unchanged tracked branch.

The AI tool `apply_change` uses the same push for git namespaces. See [ai.md](ai.md).

### Push through the API

`POST /api/v1/namespaces/{namespace}/git/push` needs editor.

| Field | Rule |
|---|---|
| `message` | Required. 1 to 2000 characters. |
| `changes[]` | 1 to 1000 changes. Paths are relative to the namespace root. |
| `changes[].op` | `put`, `delete` or `rename`. |
| `changes[].path` | The file. It must obey the path rules. |
| `changes[].new_path` | `rename` only. The new path. It must not exist. |
| `changes[].content` | `put` only. UTF-8 text. |
| `changes[].content_base64` | `put` only. Binary content as standard base64. |
| `changes[].executable` | Optional. Without it, a changed file keeps its mode and a new file is not executable. |

```sh
curl -X POST http://localhost:8080/api/v1/namespaces/data.elt/git/push \
  -H "Authorization: Bearer $SLUICE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"message": "Raise the timeout", "changes": [{"op": "put", "path": "load.flow.yaml", "content": "id: load\n..."}]}'
```

The answer is `201 {"branch": "sluice/…", "sha": "…"}`.

| Error | Cause |
|---|---|
| `404 git_source_not_found` | No git source maps the namespace. |
| `409 not_synced` | The source has not synced yet, so no base commit exists. |
| `413 file_too_large` | A `put` is larger than `SLUICE_MAX_FILE_BYTES`. |
| `422 validation_failed` | A bad path, a `delete` or `rename` of a file that does not exist, or changes that change no file. |

A successful push writes the audit event `git.push` with the branch, the SHA and the number of files.

## Permissions

| Operation | Role |
|---|---|
| List, create, read, update and delete git sources | admin |
| `POST /api/v1/git-sources/{sourceId}/sync` (**Sync now**) | operator |
| `GET /api/v1/git-sources/{sourceId}/runs` | viewer |
| `GET /api/v1/namespaces/{namespace}/git` (source of a namespace) | viewer |
| `POST /api/v1/namespaces/{namespace}/git/push` | editor |
| `POST /hooks/git/{sourceId}` | no user. The signature or token proves the caller. |

See [operations/security.md](operations/security.md) for the full permission matrix.

## Audit events

| Action | When |
|---|---|
| `git_source.create` | An admin creates a source. The details hold the URL and the branch. |
| `git_source.update` | An admin changes a source. |
| `git_source.delete` | An admin deletes a source. |
| `git_source.sync` | A user requests **Sync now**. |
| `git.push` | A user or a confirmed AI action pushes a branch. |

## Tests

The local git server fixture serves smart HTTP with a token and SSH with a key. It stands in for the git host in every test.

| Test | Proves |
|---|---|
| `SCN-GIT-001` | A token source syncs a mapped directory. The snapshot has the SHA. Flows appear. |
| `SCN-GIT-002` | An SSH key source syncs. A wrong key records an error and keeps the previous snapshot. |
| `SCN-GIT-003` | A commit that changes one mapping creates a snapshot for that mapping only. |
| `SCN-GIT-004` | HMAC webhook, invalid signature, push to another branch. |
| `SCN-GIT-005` | **Push to branch** from the UI, and **Sync now**. |
| `SCN-GIT-006` | A removed flow file makes the flow deleted and its triggers inactive. |
| `SCN-GIT-007` | Mapping conflicts return 409. |
| `SCN-GIT-008` | The temporary root is empty after sync. Run history stays. |
| `SCN-NS-004` | File writes to a git namespace return `409 namespace_read_only`. |
| `SCN-NS-005` | Symlinks and traversal paths are skipped with warnings. |
