# Git sources and linking

## What it does
An admin turns a git source on or off from the source list and from the source form. A new source starts off. An admin edits a source in the UI and deletes a git namespace that no source maps. A source can take over a managed namespace when the admin confirms it. The execution page and the flow page link to the namespace, the flow, the flow revision, the trigger and the git source.

## Decisions
- `git_sources` gets `enabled boolean NOT NULL DEFAULT false`. The migration sets `enabled = true` on each existing row. Reason: new sources start off, and an upgrade does not stop sync that already runs.
- Off stops sync only. `DueSources` skips the source. The webhook returns 202 with `queued: false`. "Sync now" returns 409 `git_source_disabled`. The mapped namespaces stay read-only, and their flows and triggers run from the last synced snapshot. Reason: the toggle controls where the files come from, not whether flows run.
- `PATCH /api/v1/git-sources/{sourceId}` takes `{enabled}`, needs admin and writes an audit event `git_source.enable` or `git_source.disable`. Reason: `updateFlow` uses PATCH for the same kind of change, and a list toggle cannot send the full PUT body.
- When a source turns on, Sluice requests a sync immediately. Reason: the user sees the files without a wait for the poll interval.
- `SourceOut` gets `enabled`. The source list shows a switch on each row. The source form shows the same switch. Reason: the user asked for the toggle in both places.
- A new source with off still creates its empty git namespaces on save. Reason: the mappings are valid at save time, and `setMappings` already does this.
- The UI gets an edit form for a source on top of the existing `PUT /api/v1/git-sources/{sourceId}`. The form shows the name read-only. Reason: the CLI, the audit `target_id` and the flow page refer to a source by name.
- `namespace.Delete` deletes a git namespace that no source maps, with the same rules as a managed namespace. If a source maps the namespace, the delete returns 409 `namespace_mapped` with the source name in `details`. The delete dialog links to the edit form of that source. Reason: a soft delete leaves the `git_mappings` row, and the next sync would then write to a deleted namespace.
- `MappingIO` gets `adopt boolean`. With `adopt: true`, a mapping to a managed namespace changes its `source_type` to `git` and sets its source. The namespace keeps its snapshots, flows and executions. The next sync writes a new snapshot from the repo. Files that the repo does not have leave the head and stay in the old snapshots. The audit log records `namespace.adopt`. Reason: a user builds a namespace in the UI and then moves it to git without a loss of history.
- Without `adopt`, a mapping to a managed namespace still returns 409 `namespace_managed`. The message tells the user to confirm the conversion. The form shows the conflict on the mapping row with a "Convert to git namespace" checkbox. Reason: a conversion makes the namespace read-only, so the user must confirm it.
- Execution breadcrumb: `Executions › <namespace> › <flow> › <id>`. The namespace links to `/namespaces/$namespace`. The flow links to `/flows/$namespace/$flowId`. Reason: Kestra and Prefect put the flow in the run breadcrumb.
- The flow route gets a `revision` search parameter. The Source tab shows that flow revision. Reason: an execution must link to the definition that ran, as the GitHub Actions "workflow file for this run" link does.
- The execution "Version" field links to the Source tab at `flow_revision_id`. For a git namespace, the field also shows the SHA. When `repo_url` is on github.com, gitlab.com or bitbucket.org, the SHA links to the commit page on that host, from https or ssh URLs. On other hosts, the SHA has a copy button. Reason: the user goes from a run to its code in one click.
- The execution "Trigger" field links to the Triggers tab of the flow. Each task run card gets a "Source" link to the same revision. Reason: Dagster links the schedule or sensor that launched a run.
- Flow page breadcrumb: the namespace links to the namespace page, not to the filtered flow list. A git flow shows a chip with the source name and the SHA, and the chip links to the source edit form. Reason: the flow page is the hop from a run to its source.
- The git panel of the namespace page links to the source edit form. Reason: today the panel names the source and gives no link to it.

## Out
- CLI and MCP commands for git sources.
- A rename of a git source.
- A delete of a mapped git namespace that also removes its mapping.
- A conversion of a git namespace back to managed.
- Commit links for git hosts other than github.com, gitlab.com and bitbucket.org.

## How I know it works
- After the upgrade, each existing source shows its switch on and keeps its sync. A new source shows its switch off and has no sync runs.
- With a source off, a push to the tracked branch does not add a sync run. Flows of its namespaces still run from their schedules. When the source turns on, a sync run starts.
- The edit form changes the branch of a source. The next sync run shows a SHA from the new branch.
- Deleting a source and then its namespace succeeds. Deleting a namespace that a source still maps returns 409 and names the source.
- A mapping to a managed namespace without the checkbox shows the conflict. With the checkbox, the namespace becomes read-only, keeps its old versions and executions, and gets a new version from the repo.
- From an execution of a git flow, one click opens the namespace, the flow, the revision that ran, the trigger, or the commit on GitHub.
