# UI polish

## What it does
Every screen of the web UI gets a new visual system with a macOS app look and spring motion. A command palette and keyboard shortcuts reach every page and the main actions. The execution page shows a live waterfall with a resizable inspector for the selected task. The log viewer, the executions list, the feedback states and the empty states get new behaviour. The UI uses only the current API.

## Decisions
- This spec replaces the visual system of REQ-UI-010 in `docs/sluice-sdd.md` — the old rules forbid shadows and motion on state change.
- The UI font is Inter Variable with tabular numbers. JetBrains Mono stays for code, logs and the editor — Inter is the closest open font to SF Pro.
- The sidebar has a translucent, blurred background. Panels have hairline borders and two soft shadow levels. The radius is 8 px on controls and 12 px on panels — this gives the premium look without a change to the layout.
- Segmented controls replace native `<select>` in filters and range pickers — native selects break the visual system.
- The palette is a neutral grey scale with Apple system blue (`#007AFF` light, `#0A84FF` dark). The state colours move to the same palette. Each state keeps its icon and text label — colour is never the only signal (REQ-UI-011).
- Light, dark and system themes stay.
- Motion uses springs on press, hover, dialogs, popovers, tabs and route changes. A running task pulses. A state change transitions. Motion also shows live state changes, not only user actions — this shows the product is live.
- `prefers-reduced-motion` turns off all motion — accessibility.
- Table rows stay 36 px — this is an operations tool.
- Motion writes styles through the CSSOM or CSS classes only. It never injects `<style>` elements — the CSP is `default-src 'self'`.
- Cmd+K opens a palette. It searches flows, namespaces, recent executions, namespace files and pages. It runs actions: run flow, rerun, restart from failed, switch theme and new file — one entry point for every action.
- `?` opens a shortcut sheet. `g` chords open pages, for example `g e` for executions. `j`/`k` and Enter work in every list. Cmd+S saves and Cmd+Enter runs in the editor — keyboard users never need the mouse.
- The execution page shows a waterfall on the left and a resizable inspector on the right. The inspector shows the attempts, outputs, metrics and logs of the selected task — this follows Trigger.dev and Inngest.
- The execution page takes task states from the event stream. A control jumps to the first failed task. A control downloads the execution as JSON, built in the client from the loaded data — no new endpoint.
- The log viewer virtualizes rows. It reconnects in follow mode after a lost stream. It makes URLs clickable, gives stderr a separate colour and highlights search matches — the viewer must stay fast on large runs.
- The executions list shows a chart of the loaded executions above the table: duration against time, with dots coloured by state. A click on a dot opens that execution.
- Actions show a toast. Loading states show skeletons in place of "Loading…". An empty state shows an icon, one line and the next action.
- The layout at 390 px keeps REQ-UI-013.

## Out
- Engine features: replay from any task with edited inputs, bulk actions, share links.
- The editor context panel for the task at the cursor.
- A flow DAG graph.
- Assistant changes. `agent-readiness.md` holds them.

## How I know it works
- `just check` passes, with the existing UI specs and axe checks in light and dark themes.
- Cmd+K on any page opens the palette. "Run" on a flow in the palette starts an execution, and a toast links to it.
- `?` shows the shortcut sheet. `g e` opens the executions list. `j`/`k` move the selection, and Enter opens the row.
- A running execution shows each task change from queued to running to success in the waterfall without a reload. The running bar pulses.
- A click on a waterfall row fills the inspector with the logs of that task only. A drag on the divider resizes the inspector.
- "Jump to first failure" on a failed execution selects the failed task.
- "Download JSON" saves a file with the execution and its task runs.
- The log viewer of an execution with 50 000 lines scrolls without lag. A URL in a log line opens in a new tab.
- With macOS "Reduce motion" on, no element animates.
- At 390 px the dashboard, the executions list and the execution page have no page-level horizontal scroll.
