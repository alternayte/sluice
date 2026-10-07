# Task artifact inputs and CLI fixes

## What it does
A task reads artifacts of its dependencies: the runner downloads each declared artifact into the workdir before the command starts. `sluice namespaces push` and `sluice validate` skip ignored files and stop a file that looks like a secret. The CLI gets `secrets list|set|check|delete`. `sluice run --input` keeps a value as a string when the input is `string` or `select`. The CLI prints the details of an API error. This spec closes GitHub issues #17, #18, #19, #20 and #21.

## Decisions

### Artifact inputs (#21)
- A `script` or `command` task takes `artifacts: [{from, name, path}]`. `path` is optional and defaults to `name` — the shape matches `files` and `depends_on`.
- This spec extends REQ-RUN-001 in `docs/sluice-sdd.md`: the runner downloads the declared artifacts after it extracts the bundle and before it starts the command — a namespace file at the same path is replaced, as with `files`.
- `from`, `name` and `path` are literals, not templates — the server can check them at dispatch and limit the run token to the declared artifacts.
- `from` must name a `script` or `command` task in `depends_on`. Validation rejects `artifacts` on `http` and `subflow` tasks — those tasks have no workdir and produce no artifacts.
- `path` is relative to the workdir and passes the same check as a namespace file path — a task cannot write outside its workdir.
- The source is the latest task run of `from` in the execution. For a reused task run, the server follows `reused_from_id` to the task run that holds the artifact — restart copies outputs only, not artifacts.
- When a declared artifact does not exist, the consuming task run fails before its command starts, with a reason that names the task and the artifact. The producing task run stays SUCCESS — existing flows do not change.
- The runner gets artifacts from a new runner API route that takes the run token — the runner has no storage credentials on docker and kubernetes.

### Ignore file (#20)
- `push` and `validate` read `.sluiceignore` at the namespace root, with gitignore syntax including negation — one file has one meaning.
- Built-in rules apply without the file: `.git/`, `__pycache__/`, `*.pyc`, `.venv/`, `node_modules/`, `.DS_Store`. A negation line in `.sluiceignore` overrides a built-in rule.
- The CLI does not read `.gitignore` — git also reads parent, nested and global files, so one file gives a different result from git.
- `.sluiceignore` itself is not uploaded — it is a rule for the CLI, not a namespace file.
- `push` refuses before it uploads when a file matches `.env`, `.env.*`, `*.pem`, `*.key` or `*.p12` and no rule covers it. It names each file and exits non-zero — a snapshot is immutable, so a warning is too late. An ignore line skips the file. A negation line permits the upload.
- `validate` reports the same files as warnings and does not fail — it uploads nothing.
- `--verbose` prints each skipped path.
- `sluice init` writes a starter `.sluiceignore` when none exists.

### CLI (#17, #18, #19)
- `sluice secrets list|set|check|delete [--namespace ns]` wrap the existing secrets endpoints — no new server API.
- `secrets set KEY` takes the value from `--from-file path` or `--stdin` only, and stores the bytes as they are — the value stays out of shell history and `ps`.
- `sluice run` gets the flow and converts each `--input` by its declared type: `string` and `select` stay strings, other types parse as JSON — the CLI then matches the Run dialog, and the API stays strict.
- An `--input` key that the flow does not declare parses as JSON as before — the server reports the unknown input.
- The CLI prints one indented `field: message` line for each detail of an API error. With `--output json` it prints the full error envelope to stdout — scripts and agents can parse it.
- The agent skill in `skills/` describes the secrets commands, `.sluiceignore` and `artifacts`.

## Out
- A shared execution workspace.
- An artifact of a child execution as the source (`from` on a `subflow` task).
- Patterns in `name`, or all artifacts of a dependency.
- A change to the producing task when an artifact upload fails.
- Server-side conversion of numbers to strings for `string` and `select` inputs.
- Ignore rules for namespaces synced from git or edited in the UI.

## How I know it works
- A flow with `extract → render`, where `extract` emits artifact `data.parquet` and `render` declares it, ends SUCCESS and `render` reads the file at the declared path. The same flow passes on the docker executor.
- `extract` succeeds and `render` fails. After `sluice executions restart`, `render` gets `data.parquet` and `extract` shows as reused.
- `render` declares an artifact that `extract` does not emit. The `render` task run fails with a reason that names `extract` and the artifact, and its command does not start.
- `sluice validate` reports `artifacts.from` on a task that is not in `depends_on`.
- A directory with `scripts/__pycache__/x.pyc` and `out/` in `.sluiceignore`: `push --verbose` lists both as skipped and the version does not hold them.
- A directory with `.env`: `push` exits non-zero, names `.env`, and creates no version. With `.env` in `.sluiceignore`, `push` succeeds without it.
- `printf s3cr3t | sluice secrets set API_KEY --stdin`, then `sluice secrets list` shows `API_KEY` and `sluice secrets check API_KEY` succeeds. `sluice secrets delete API_KEY` removes it.
- `sluice run ns/flow --input space=1` starts an execution for a `select` input with values `["all", "1"]`.
- `sluice run ns/flow --input space=zzz` prints `inputs.space: must be one of [all 1]`. With `--output json`, stdout holds the envelope with `details`.
