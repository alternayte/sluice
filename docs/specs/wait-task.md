# Wait task

## What it does
A `wait` task pauses its branch of an execution until a person, a script or an agent answers it. An answer resumes the task with values, or rejects it. The same task covers a human approval, an outside event and an agent in the loop.

## Decisions
- `wait` is a new task type. It takes `inputs:` in the shape of flow inputs (`id`, `type`, `required`, `default`, `values`, `description`) and an optional `message` template — Sluice validates an answer with the code of the Run dialog, and the UI shows the same form.
- A waiting task run has the new state WAITING. This spec adds the state to the task run states of §6.6 in `docs/sluice-sdd.md`, with the transitions PENDING to WAITING, and WAITING to SUCCESS, FAILED, TIMED_OUT or CANCELLED — a reason on RUNNING needs a special case in each place that reads RUNNING.
- No instance claims a WAITING task run, no heartbeat applies, and it uses no pool slot — the wait must survive a restart of every instance.
- The execution stays RUNNING. The eight execution states and the CLI exit codes do not change — clients of the execution API keep working.
- Resume ends the task run SUCCESS. The answer values become the outputs of the task — later tasks read them with `tasks.<id>.outputs.<key>`, with no new template syntax.
- Reject ends the task run FAILED with reason `rejected` and the message as its error — a flow has no branch other than `run_if`, so an approval must map to SUCCESS or FAILED.
- Resume and reject need the operator role — the role that starts and cancels executions also answers them.
- The API has `POST /api/v1/executions/{id}/tasks/{task}/resume` and `.../reject`. The CLI has `sluice executions resume <id> --task <task> --input k=v` and `sluice executions reject <id> --task <task> --message text`. MCP has `resume_execution` and `reject_execution`. The UI shows the form on the task — each kind of user answers through its own surface.
- `--input` on resume converts each value by the declared type of the `wait` input — the same rule as `sluice run --input`.
- An answer to a task run that is not WAITING gives 409 `not_waiting` — two answers to one wait must not both succeed.
- The task `timeout` applies to the wait and ends it TIMED_OUT. The retry policy does not apply to `rejected` or to a timeout of a `wait` task — a retry would ask the same question again.
- `GET /api/v1/executions` and `sluice executions list` take `waiting=true` — a person or an agent finds what needs an answer.
- The audit log records who resumed or rejected, with the values — an approval needs a record.
- `executor`, `each:`, `files` and `artifacts` are not allowed on a `wait` task — it runs no process.

## Out
- Notifications. A task before the `wait` sends the message, for example an `http` task to a chat webhook.
- An answer through a public link without a token.
- A wait for a time (`sleep`) or for another execution. `timeout` and `subflow` cover them.
- A new execution state.

## How I know it works
- A flow `build → approve (wait) → deploy` shows `approve` as WAITING and the execution as RUNNING. `sluice executions list --waiting` lists it.
- All instances stop and start again. `approve` is still WAITING.
- `sluice executions resume <id> --task approve --input reason=ok` ends `approve` SUCCESS, and `deploy` reads `${{ tasks.approve.outputs.reason }}`.
- `sluice executions reject <id> --task approve --message "not today"` ends `approve` FAILED with reason `rejected`. `deploy` is SKIPPED, and a task with `run_if: failure` runs.
- A resume without a required input gives `validation_failed` with `inputs.<id>` in the details, and the task stays WAITING.
- A second resume gives 409 `not_waiting`.
- With `timeout: 1m` and no answer, `approve` ends TIMED_OUT.
- A viewer token gets 403 on resume.
- The audit log shows the resume with the user and the values.
