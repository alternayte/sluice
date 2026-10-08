# Task fan-out

## What it does
A task with `each:` runs one time for each item of a list. Each item has its own task run, retry, log and timeline bar in the same execution. A later task reads the outputs of all items as a list, and reads their artifacts as one file per item.

## Decisions
- `each:` is a literal YAML list or one template that gives a JSON list. It is allowed on `script`, `command` and `subflow` tasks — `http` and `wait` have no workload to split.
- A task run gets an item index. Its key becomes (execution, task, item, attempt). This spec replaces the task run key of §5 in `docs/sluice-sdd.md` — each item needs its own attempts and restart state.
- A task without `each:` has item index 0 and no item value — existing rows, API clients and queries keep their meaning.
- Sluice renders the list one time, when the dependencies of the task end, and stores each item value on its task run — a retry and a restart then use the same items.
- Inside the task, `${{ item }}` is the item value and `${{ item_index }}` is its position. They are allowed where `inputs.<id>` is allowed — one rule for the author to learn.
- `${{ tasks.<id>.outputs.<key> }}` of a task with `each:` gives a JSON list in item order. An item that did not emit the key gives `null` — a later task can match the list with the list that it gave to `each:`.
- The task counts as SUCCESS for `run_if` only when every item ended SUCCESS — a partial result must not start the default path.
- An empty list ends the task SUCCESS with no task runs, and each output key gives `[]` — SKIPPED would also skip each later task with `run_if: success`.
- A list of more than 1000 items, or a value that is not a list, fails the task with reason `template_error` before any item starts — the limit protects the queue and the UI.
- `max_parallel` on the task limits the items that run at the same time. The default is no limit other than the pool slots — a flow can protect a rate-limited API.
- A task that declares an artifact of a task with `each:` gets one file per item, with the item index before the file name: `data/0/part.parquet` for `path: data/part.parquet` — the file keeps its name, and one glob reads all items.
- When one item did not emit the artifact, the reading task fails with `artifact_not_found` and the error names the item — the rule for a missing artifact does not change.
- Restart reuses each item that ended SUCCESS and runs the other items again — the same rule as for tasks, applied per item.
- A task with `each:` needs the next runner protocol level (see `v1-compatibility.md`) — an old runner does not know `item`.
- The API and the CLI show the item index and the item value of each task run. The timeline groups the items of one task under one row that expands.

## Out
- `If`, `Switch` and a loop task type. `run_if` and the script cover them.
- Nested fan-out: `each:` on a task that reads `item` of another task.
- A fan-out that continues when items fail, other than through `run_if: always` on a later task.
- Lists above 1000 items. Use a `subflow` per batch.

## How I know it works
- A flow with `each: ["a", "b", "c"]` on a command task `echo ${{ item }}` shows three task runs with item 0, 1 and 2, and each log holds its own letter.
- A later task with `${{ tasks.load.outputs.rows }}` gets `[3, null, 7]` when item 1 emitted no `rows`.
- Item 1 fails. The task is FAILED, a later task with `run_if: success` is SKIPPED, and after `sluice executions restart` only item 1 runs again.
- `each: ${{ inputs.tables }}` with the input `[]` ends the task SUCCESS, and a later task runs.
- A list of 1001 items fails the task with `template_error`, and no item starts.
- With `max_parallel: 2` and five items of `sleep 5`, the timeline never shows more than two items RUNNING.
- A later task with `artifacts: [{from: load, name: part.parquet, path: data/part.parquet}]` finds `data/0/part.parquet`, `data/1/part.parquet` and `data/2/part.parquet`.
- `sluice validate` rejects `each:` on an `http` task, and `${{ item }}` in a task without `each:`.
