import { readFileSync } from "node:fs";
import { expect, test } from "@playwright/test";
import {
  adminAPI,
  adminEmail,
  adminPassword,
  dbQuery,
  executionHeading,
  loginUI,
  seedNamespace,
  triggerFlowAPI,
} from "../helpers/app";

type LogEntry = { task_key: string; attempt: number; stream: string; text: string };

test("SCN-RUN-008 live tail, archive after completion, search, task filter and download", async ({ page }) => {
  test.setTimeout(120_000);
  const api = await adminAPI();
  const ns = await seedNamespace(api, "run008", {
    "tick.py": [
      "import time",
      "for i in range(1, 11):",
      '    print(f"tick {i} at {int(time.time() * 1000)}", flush=True)',
      "    time.sleep(1)",
      "",
    ].join("\n"),
    "logs.flow.yaml": [
      "id: logs",
      "tasks:",
      "  - id: ticker",
      "    type: command",
      '    command: ["python3", "-u", "tick.py"]',
      "  - id: other",
      "    type: command",
      '    command: ["echo", "other-line"]',
      "",
    ].join("\n"),
  });

  await loginUI(page, adminEmail, adminPassword);
  const exec = await triggerFlowAPI(api, ns, "logs");
  await page.goto(`/executions/${exec.id}`);
  const status = page.getByRole("status").filter({ hasText: /lines/ });
  const panel = page.getByLabel("Log lines");
  await expect(status).toContainText("Live");

  // Each live line must show within 2 s after the script prints it.
  for (const n of [6, 7, 8]) {
    const handle = await page.waitForFunction(
      (tick) => {
        const text = document.querySelector('[data-testid="log-panel"]')?.textContent ?? "";
        const m = new RegExp(`tick ${tick} at (\\d+)`).exec(text);
        return m ? { printed: Number(m[1]), shown: Date.now() } : null;
      },
      n,
      { polling: "raf", timeout: 30_000 },
    );
    const { printed, shown } = (await handle.jsonValue())!;
    expect(shown - printed, `tick ${n} latency`).toBeLessThan(2_000);
  }

  await expect(executionHeading(page, ns)).toContainText("Success", { timeout: 30_000 });
  await expect(status).toContainText("Complete");

  // After completion the server archives the chunks and removes them from Postgres.
  await expect
    .poll(() => dbQuery(`SELECT count(*) FROM log_chunks WHERE execution_id = '${exec.id}'`), { timeout: 15_000 })
    .toBe("0");
  await page.reload();
  await expect(status).toContainText("Complete");
  await expect(panel).toContainText("tick 10 at");
  await expect(panel).toContainText("other-line");

  // Search and task filter.
  const search = page.getByLabel("Search");
  await search.fill("tick 3 ");
  await expect(panel).toContainText("tick 3 at");
  await expect(panel).not.toContainText("tick 4 at");
  await expect(panel).not.toContainText("other-line");
  await search.fill("");
  await page.getByLabel("Task").selectOption("other");
  await expect(panel).toContainText("other-line");
  await expect(panel).not.toContainText("tick");
  await page.getByLabel("Task").selectOption("");

  // The download holds the stored lines in the same order.
  const res = await api.get(`/api/v1/executions/${exec.id}/logs?limit=5000`);
  const stored = ((await res.json()) as { lines: LogEntry[] }).lines;
  expect(stored.length).toBeGreaterThanOrEqual(11);
  const downloadEvent = page.waitForEvent("download");
  await page.getByRole("link", { name: "Download" }).click();
  const download = await downloadEvent;
  const lines = readFileSync(await download.path(), "utf8").trimEnd().split("\n");
  expect(lines).toHaveLength(stored.length);
  lines.forEach((line, i) => {
    const l = stored[i]!;
    expect(line.endsWith(` [${l.task_key}#${l.attempt}] ${l.stream}: ${l.text}`), line).toBe(true);
  });
  await api.dispose();
});
