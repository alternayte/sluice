# v1 compatibility

## What it does
Sluice keeps four promises that a v1 needs. A restarted execution owns its artifacts. A database upgrades from each release to each later release, also during a rolling update. A runner that is too old for a task fails that task with a clear reason. Each pull request runs every e2e test on the process, docker and kubernetes executors.

## Decisions

### Artifacts of a restarted execution
- Restart copies the artifact rows of each reused task run to the new execution. The rows name the same storage key. This spec replaces the outputs-only copy of REQ-EXE-009 in `docs/sluice-sdd.md` — the new execution then lists and serves its artifacts.
- Retention and the storage cleanup delete a stored artifact only when no artifact row names its key — the rule that file objects of snapshots already have.
- The lookup through `reused_from_id` for artifact inputs goes away. An artifact input reads the rows of its own execution — one rule for the first run and for a restart.
- Artifacts get no content hash — the server masks secrets in the upload stream, so the hash is known only after the upload, and the storage interface has no rename.

### Database upgrades
- `db/migrations/00001_init.sql` never changes again. Each schema change is a new file. This spec replaces DI-4 in `docs/build/decisions.md` — releases exist, and the file is identical in all of them.
- A check in `checks/` fails when a migration file differs from its content at the latest release tag — the rule must not depend on memory.
- Migrations go forward only. The way back is a database restore — a down migration that nobody runs is not safe.
- Each migration keeps the previous release working. A release adds tables and columns. A later release removes what the previous one used — a rolling update runs old instances on the new schema.
- A required CI job starts the image of the latest release with data and a running execution, applies the new migrations, checks that the old binary serves and ends that execution, then starts the new binary on the same database.

### Runner protocol level
- The runner sends its protocol level, an integer, with the `spec` request. A runner that sends none has level 1 — the released runners send none.
- Each feature of the spec has the level that introduced it. Artifact inputs are level 2. Fan-out items are level 3.
- The server computes the level that the task needs. For a lower runner it answers 409 and fails the task run with reason `runner_too_old`. The error names both levels and the image — today an old runner ignores `artifacts` and starts the command without the files.
- A task that needs no new feature runs on an old runner — a server upgrade must not break every image with `inject_runner: false`.
- Changes inside `/api/runner/v1` only add. A removal needs `/api/runner/v2`.

### Tests that gate a merge
- The CI e2e job builds both images, then runs the full Go e2e suite. Every test must pass.
- `docs/build/e2e-baseline.txt` and `scripts/e2e-compare.sh` go away — the baseline accepts tests that fail.
- Each test that fails in the baseline today is fixed, or deleted when it cannot pass in CI — a test that never passes gives no evidence.
- A required CI job runs `just e2e-k8s` on kind for each pull request — a broken kubernetes executor must not reach `main`.

## Out
- Deduplication of equal artifacts.
- Down migrations.
- An upgrade that skips the rule "previous release keeps working", for example straight from 0.2.x across a later removal.
- A freeze of the flow YAML and of `/api/v1`. They freeze at the 1.0.0 tag, after `task-fan-out.md` and `wait-task.md` are built.

## How I know it works
- `extract` succeeds with an artifact and `render` fails. After a restart, the Artifacts tab of the new execution shows the artifact of `extract`. After retention deletes the old execution, the download still works and `render` still gets the file on a second restart.
- After both executions are deleted, the storage cleanup removes the stored object.
- A change to `00001_init.sql` fails `just check` with the name of the file.
- The upgrade job is green on a pull request with a new migration, and red when the migration drops a column that the last release reads.
- An image with a 0.2.3 runner and `inject_runner: false` runs a plain command task. The same image fails a task with `artifacts` with reason `runner_too_old`, and the command does not start.
- A pull request that breaks a docker e2e test cannot merge. The same holds for a kind test.
- The repository has no e2e baseline file.
