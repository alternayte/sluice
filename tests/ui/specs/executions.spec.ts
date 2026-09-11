import { readFileSync, rmSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { expect, test, type Page } from "@playwright/test";
import {
  adminAPI,
  cancelExecutionAPI,
  executionHeading,
  ganttRow,
  loginUI,
  adminEmail,
  adminPassword,
  runFileAPI,
  seedNamespace,
  signInAs,
  triggerFlowAPI,
  uniq,
  waitExecutionState,
} from "../helpers/app";

const durationText = /\d+ ms|\d+\.\d s/;

/** bodyRows returns the data rows of the only table on the page. */
function bodyRows(page: Page) {
  return page.getByRole("table").getByRole("row").filter({ has: page.getByRole("cell") });
}

/** localInput formats a time for a datetime-local field in the browser time zone. */
function localInput(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

test("SCN-EXE-008 cancel from the UI cancels the running task, the pending task and the execution", async ({
  browser,
}) => {
  const api = await adminAPI();
  const ns = await seedNamespace(api, "exe008", {
    "cancel.flow.yaml": [
      "id: cancel",
      "tasks:",
      "  - id: slow",
      "    type: command",
      '    command: ["sleep", "60"]',
      "  - id: after",
      "    type: command",
      "    depends_on: [slow]",
      '    command: ["echo", "late"]',
      "",
    ].join("\n"),
  });
  const exec = await triggerFlowAPI(api, ns, "cancel");
  try {
    const { context, page } = await signInAs(browser, api, "operator");
    await page.goto(`/executions/${exec.id}`);
    await expect(ganttRow(page, "slow #1")).toContainText("Running");

    await page.getByRole("button", { name: "Cancel", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Cancel execution" });
    await dialog.getByRole("button", { name: "Cancel execution" }).click();

    await expect(ganttRow(page, "slow #1")).toContainText("Cancelled", { timeout: 10_000 });
    await expect(ganttRow(page, "after #1")).toContainText("Cancelled", { timeout: 10_000 });
    await expect(executionHeading(page, ns)).toContainText("Cancelled", { timeout: 10_000 });
    await context.close();
  } finally {
    await cancelExecutionAPI(api, exec.id);
    await api.dispose();
  }
});

test("SCN-EXE-009 restart from failed reuses successful tasks, and rerun runs all tasks with the same inputs and snapshot", async ({
  browser,
}) => {
  test.setTimeout(120_000);
  const api = await adminAPI();
  const marker = path.join(os.tmpdir(), `sluice-ui-exe009-${uniq()}`);
  const ns = await seedNamespace(api, "exe009", {
    "flaky.sh": `if [ -f "${marker}" ]; then echo recovered; exit 0; fi\ntouch "${marker}"\necho first failure\nexit 1\n`,
    "restart.flow.yaml": [
      "id: restart",
      "inputs:",
      "  - { id: n, type: int, default: 1 }",
      "tasks:",
      "  - id: first",
      "    type: command",
      '    command: ["echo", "first"]',
      "  - id: flaky",
      "    type: script",
      "    file: flaky.sh",
      "    depends_on: [first]",
      "  - id: last",
      "    type: command",
      "    depends_on: [flaky]",
      '    command: ["echo", "last"]',
      "",
    ].join("\n"),
  });
  try {
    const failed = await triggerFlowAPI(api, ns, "restart", { inputs: { n: 7 } });
    await waitExecutionState(api, failed.id, ["FAILED"]);
    const { context, page } = await signInAs(browser, api, "operator");

    await page.goto(`/executions/${failed.id}`);
    await expect(executionHeading(page, ns)).toContainText("Failed");
    await expect(ganttRow(page, "last #1")).toContainText("Skipped");
    await page.getByRole("button", { name: "Restart from failed" }).click();
    await expect(page).not.toHaveURL(new RegExp(failed.id));
    await expect(page).toHaveURL(/\/executions\/[0-9a-f-]{36}$/);
    const restartId = page.url().split("/").pop()!;
    await expect(executionHeading(page, ns)).toContainText("Success", { timeout: 60_000 });
    await expect(ganttRow(page, "first #1")).toContainText("reused");
    await expect(ganttRow(page, "flaky #1")).not.toContainText("reused");
    await expect(ganttRow(page, "flaky #1")).toContainText("Success");
    await expect(ganttRow(page, "last #1")).not.toContainText("reused");
    await expect(ganttRow(page, "last #1")).toContainText("Success");
    await expect(page.getByRole("link", { name: failed.id.slice(0, 8) })).toBeVisible();

    await page.getByRole("button", { name: "Rerun", exact: true }).click();
    await expect(page).not.toHaveURL(new RegExp(restartId));
    await expect(page).toHaveURL(/\/executions\/[0-9a-f-]{36}$/);
    await expect(executionHeading(page, ns)).toContainText("Success", { timeout: 60_000 });
    const gantt = page.getByTestId("gantt");
    await expect(gantt.getByRole("listitem")).toHaveCount(3);
    await expect(gantt.getByText("reused")).toHaveCount(0);
    for (const task of ["first #1", "flaky #1", "last #1"]) await expect(ganttRow(page, task)).toContainText("Success");
    await expect(page.getByText('{"n":7}')).toBeVisible();
    await expect(page.getByRole("definition").filter({ hasText: /^v1$/ })).toBeVisible();
    await context.close();
  } finally {
    rmSync(marker, { force: true });
    await api.dispose();
  }
});

test("SCN-UI-002 executions list filters, sort, pagination and filters from the URL after reload", async ({ page }) => {
  test.setTimeout(180_000);
  const api = await adminAPI();
  const ns = await seedNamespace(api, "ui002", {
    "busy.flow.yaml":
      'id: busy\nconcurrency: { limit: 1, behavior: skip }\ntasks:\n  - id: wait\n    type: command\n    command: ["sleep", "60"]\n',
    "quick.flow.yaml": 'id: quick\ntasks:\n  - id: nap\n    type: command\n    command: ["sleep", "2"]\n',
    "fast.flow.yaml": 'id: fast\ntasks:\n  - id: done\n    type: command\n    command: ["true"]\n',
    "hello.sh": "echo hello\n",
  });
  const start = new Date();
  const busy = await triggerFlowAPI(api, ns, "busy", { labels: { team: "a" } });
  try {
    await waitExecutionState(api, busy.id, ["RUNNING"]);
    for (let i = 0; i < 55; i++) await triggerFlowAPI(api, ns, "busy");
    await expect
      .poll(async () => {
        const res = await api.get(`/api/v1/executions?namespace=${ns}&state=SKIPPED&limit=200`);
        return ((await res.json()) as { items: unknown[] }).items.length;
      })
      .toBe(55);
    const quick = await triggerFlowAPI(api, ns, "quick");
    await waitExecutionState(api, quick.id, ["SUCCESS"]);
    const fast = await triggerFlowAPI(api, ns, "fast");
    await waitExecutionState(api, fast.id, ["SUCCESS"]);
    const file = await runFileAPI(api, ns, "hello.sh");
    await waitExecutionState(api, file.id, ["SUCCESS"]);

    await loginUI(page, adminEmail, adminPassword);
    await page.goto("/executions");
    const filters = page.getByRole("form", { name: "Filters" });
    const apply = filters.getByRole("button", { name: "Apply" });
    const rows = bodyRows(page);

    // State and namespace filters with server pagination.
    await filters.getByLabel("Namespace").fill(ns);
    await filters.getByRole("checkbox", { name: "Skipped" }).check();
    await apply.click();
    await expect(page).toHaveURL(new RegExp(`namespace=${ns}`));
    await expect(page).toHaveURL(/state=SKIPPED/);
    await expect(rows).toHaveCount(50);
    await expect(rows.filter({ hasNotText: "Skipped" })).toHaveCount(0);
    await page.getByRole("button", { name: "Load more" }).click();
    await expect(rows).toHaveCount(55);
    await expect(page.getByRole("button", { name: "Load more" })).toHaveCount(0);

    // Reload keeps the filters from the URL.
    await page.reload();
    await expect(filters.getByLabel("Namespace")).toHaveValue(ns);
    await expect(filters.getByRole("checkbox", { name: "Skipped" })).toBeChecked();
    await expect(rows).toHaveCount(50);
    await expect(rows.filter({ hasNotText: "Skipped" })).toHaveCount(0);

    // Label filter.
    await filters.getByRole("checkbox", { name: "Skipped" }).uncheck();
    await filters.getByLabel("Labels").fill("team=a");
    await apply.click();
    await expect(page).toHaveURL(/label=team/);
    await expect(rows).toHaveCount(1);
    await expect(rows.first()).toContainText("team=a");
    await expect(rows.first()).toContainText("Running");

    // Trigger type filter.
    await filters.getByLabel("Labels").fill("");
    await filters.getByLabel("Trigger type").selectOption({ label: "File" });
    await apply.click();
    await expect(page).toHaveURL(/trigger_type=file/);
    await expect(rows).toHaveCount(1);
    await expect(rows.first()).toContainText("File run");

    // Flow filter.
    await filters.getByLabel("Trigger type").selectOption({ label: "All" });
    await filters.getByLabel("Flow", { exact: true }).fill(`${ns}/quick`);
    await apply.click();
    await expect(rows).toHaveCount(1);
    await expect(rows.first()).toContainText("quick");

    // Sort by created time and by duration.
    await filters.getByLabel("Flow", { exact: true }).fill("");
    await filters.getByRole("checkbox", { name: "Success" }).check();
    await apply.click();
    await expect(rows).toHaveCount(3);
    await expect(rows.first()).toContainText("File run");
    await expect(rows.nth(1)).toContainText("fast");
    await expect(rows.nth(2)).toContainText("quick");
    await filters.getByLabel("Sort").selectOption({ label: "Duration" });
    await expect(page).toHaveURL(/sort=duration/);
    await expect(rows.first()).toContainText("quick");

    // Time range.
    await filters.getByLabel("From").fill(localInput(new Date(Date.now() + 24 * 3600_000)));
    await apply.click();
    await expect(page.getByText("No executions match the filters.")).toBeVisible();
    await filters.getByLabel("From").fill(localInput(new Date(start.getTime() - 5 * 60_000)));
    await filters.getByLabel("To").fill(localInput(new Date(Date.now() + 24 * 3600_000)));
    await apply.click();
    await expect(rows).toHaveCount(3);
    await filters.getByLabel("To").fill(localInput(new Date(start.getTime() - 5 * 60_000)));
    await apply.click();
    await expect(page.getByText("No executions match the filters.")).toBeVisible();
  } finally {
    await cancelExecutionAPI(api, busy.id);
    await api.dispose();
  }
});

test("SCN-UI-003 the Gantt shows attempts with durations, and outputs, metrics and artifact download work", async ({
  page,
}) => {
  const api = await adminAPI();
  const ns = await seedNamespace(api, "ui003", {
    "emit.sh": [
      "printf 'hello report\\n' > report.txt",
      `echo '{"type":"output","key":"rows","value":42}' >> "$SLUICE_OUTPUTS"`,
      `echo '{"type":"metric","name":"rows_loaded","value":42,"unit":"rows","tags":{"table":"orders"}}' >> "$SLUICE_OUTPUTS"`,
      `echo '{"type":"artifact","path":"report.txt","name":"report","content_type":"text/plain"}' >> "$SLUICE_OUTPUTS"`,
      "",
    ].join("\n"),
    "retry.sh": 'if [ "$SLUICE_ATTEMPT" = "1" ]; then echo attempt one fails; exit 1; fi\necho attempt two\n',
    "detail.flow.yaml": [
      "id: detail",
      "tasks:",
      "  - id: emit",
      "    type: script",
      "    file: emit.sh",
      "  - id: retry",
      "    type: script",
      "    file: retry.sh",
      "    retry: { max_attempts: 2, backoff: fixed, initial: 1s }",
      "",
    ].join("\n"),
  });
  const exec = await triggerFlowAPI(api, ns, "detail");
  await waitExecutionState(api, exec.id, ["SUCCESS", "FAILED"]);

  await loginUI(page, adminEmail, adminPassword);
  await page.goto(`/executions/${exec.id}`);
  await expect(executionHeading(page, ns)).toContainText("Success");
  await expect(page.getByTestId("gantt-bar")).toHaveCount(3);
  await expect(ganttRow(page, "retry #1")).toContainText("Failed");
  await expect(ganttRow(page, "retry #2")).toContainText("Success");
  for (const label of ["emit #1", "retry #1", "retry #2"]) {
    await expect(ganttRow(page, label)).toContainText(durationText);
  }

  const sections = page.getByRole("tablist", { name: "Execution sections" });
  await sections.getByRole("tab", { name: "Outputs" }).click();
  await expect(page.getByRole("heading", { name: "emit #1" })).toBeVisible();
  await expect(page.getByText('"rows": 42')).toBeVisible();

  await sections.getByRole("tab", { name: "Metrics" }).click();
  const metric = page.getByRole("row").filter({ hasText: "rows_loaded" });
  await expect(metric).toContainText("42");
  await expect(metric).toContainText("rows");
  await expect(metric).toContainText("table=orders");

  await sections.getByRole("tab", { name: "Artifacts" }).click();
  const artifact = page.getByRole("row").filter({ hasText: "report" });
  await expect(artifact).toContainText("text/plain");
  const downloadEvent = page.waitForEvent("download");
  await artifact.getByRole("link", { name: "Download" }).click();
  const download = await downloadEvent;
  expect(readFileSync(await download.path(), "utf8")).toBe("hello report\n");
  await api.dispose();
});

test("SCN-UI-008 a state change shows on the list and on the detail page within 2 s", async ({ browser }) => {
  const api = await adminAPI();
  const ns = await seedNamespace(api, "ui008", {
    "slow.flow.yaml": 'id: slow\ntasks:\n  - id: wait\n    type: command\n    command: ["sleep", "60"]\n',
  });
  const exec = await triggerFlowAPI(api, ns, "slow");
  try {
    await waitExecutionState(api, exec.id, ["RUNNING"]);
    const context = await browser.newContext();
    const list = await context.newPage();
    await loginUI(list, adminEmail, adminPassword);
    const detail = await context.newPage();

    await list.goto(`/executions?namespace=${ns}`);
    await detail.goto(`/executions/${exec.id}`);
    const row = bodyRows(list);
    await expect(row).toHaveCount(1);
    await expect(row).toContainText("Running");
    await expect(executionHeading(detail, ns)).toContainText("Running");

    await cancelExecutionAPI(api, exec.id);
    await Promise.all([
      expect(row).toContainText(/Cancell(ing|ed)/, { timeout: 2_000 }),
      expect(executionHeading(detail, ns)).toContainText(/Cancell(ing|ed)/, { timeout: 2_000 }),
    ]);
    await context.close();
  } finally {
    await cancelExecutionAPI(api, exec.id);
    await api.dispose();
  }
});
