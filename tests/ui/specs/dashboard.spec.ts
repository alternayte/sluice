import { expect, test, type APIRequestContext } from "@playwright/test";
import { adminAPI, adminEmail, adminPassword, loginUI, seedNamespace, triggerFlowAPI } from "../helpers/app";

type DashboardData = {
  kpis: {
    executions: number;
    failed: number;
    timed_out: number;
    running: number;
    success_rate: number | null;
    median_duration_ms: number | null;
  };
  buckets: { success: number; failed: number; timed_out: number; cancelled: number; skipped: number }[];
};

/** formatMs and formatRate repeat the UI formats of ui/src/lib/charts.ts. */
function formatMs(ms: number | null): string {
  if (ms === null) return "—";
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${Math.floor(ms / 60_000)} min ${Math.round((ms % 60_000) / 1000)} s`;
}

function formatRate(rate: number | null): string {
  return rate === null ? "—" : `${(rate * 100).toFixed(1)}%`;
}

/** runAll triggers a flow n times and waits until all executions ended. */
async function runAll(api: APIRequestContext, ns: string, flowId: string, n: number) {
  const ids = await Promise.all(Array.from({ length: n }, () => triggerFlowAPI(api, ns, flowId).then((e) => e.id)));
  await expect
    .poll(
      async () => {
        const res = await api.get(`/api/v1/executions?flow=${encodeURIComponent(`${ns}/${flowId}`)}&limit=200`);
        const items = ((await res.json()) as { items: { id: string; state: string }[] }).items;
        return items.filter((i) => ids.includes(i.id) && ["SUCCESS", "FAILED"].includes(i.state)).length;
      },
      { timeout: 120_000, message: `executions of ${ns}/${flowId}` },
    )
    .toBe(n);
}

test("SCN-UI-001 KPI values equal the API aggregates, the bucket totals match and a range change changes the query", async ({
  page,
}) => {
  test.setTimeout(240_000);
  const api = await adminAPI();
  const ns = await seedNamespace(api, "ui001", {
    "ok.flow.yaml": 'id: ok\ntasks:\n  - {id: t, type: command, command: ["true"]}\n',
    "bad.flow.yaml": 'id: bad\ntasks:\n  - {id: t, type: command, command: ["false"]}\n',
  });
  await runAll(api, ns, "ok", 3);
  await runAll(api, ns, "bad", 2);

  await loginUI(page, adminEmail, adminPassword);
  await page.getByLabel("Namespace").fill(ns);
  const res = await api.get(`/api/v1/stats/dashboard?range=24h&namespace=${ns}`);
  const d = (await res.json()) as DashboardData;
  expect(d.kpis.executions).toBe(5);
  await expect(page.getByRole("group", { name: "Executions" })).toContainText(String(d.kpis.executions));
  await expect(page.getByRole("group", { name: "Success rate" })).toContainText(formatRate(d.kpis.success_rate));
  await expect(page.getByRole("group", { name: "Failed" })).toContainText(String(d.kpis.failed + d.kpis.timed_out));
  await expect(page.getByRole("group", { name: "Median duration" })).toContainText(formatMs(d.kpis.median_duration_ms));
  await expect(page.getByRole("group", { name: "Running now" })).toContainText(String(d.kpis.running));

  const table = page.getByRole("table", { name: "Executions by end state data" });
  await expect(table.getByRole("row")).toHaveCount(d.buckets.length + 1);
  const cells = await table.locator("tbody td").allTextContents();
  const shown = cells.reduce((sum, c) => sum + (Number(c) || 0), 0);
  const apiTotal = d.buckets.reduce((sum, b) => sum + b.success + b.failed + b.timed_out + b.cancelled + b.skipped, 0);
  expect(shown).toBe(apiTotal);
  expect(apiTotal).toBe(d.kpis.executions);

  const request = page.waitForRequest(
    (r) => r.url().includes("/api/v1/stats/dashboard") && r.url().includes("range=7d"),
  );
  await page.getByLabel("Range").selectOption("7d");
  await request;
  await expect(table.getByRole("row")).toHaveCount(8);
});

test("SCN-UI-004 the state strip shows 50 states, the duration chart renders and a metric grouped by table shows one series per table", async ({
  page,
}) => {
  test.setTimeout(240_000);
  const api = await adminAPI();
  const emit =
    'for t in orders customers; do echo "{\\"type\\":\\"metric\\",\\"name\\":\\"rows_loaded\\",\\"value\\":$RANDOM,\\"tags\\":{\\"table\\":\\"$t\\"}}" >> "$SLUICE_OUTPUTS"; done\n';
  const ns = await seedNamespace(api, "ui004", {
    "emit.sh": emit,
    "m.flow.yaml": "id: m\ntasks:\n  - {id: t, type: script, file: emit.sh}\n",
  });
  await runAll(api, ns, "m", 55);

  await loginUI(page, adminEmail, adminPassword);
  await page.goto(`/flows/${ns}/m`);
  await expect(page.getByRole("list", { name: "Execution states" }).getByRole("listitem")).toHaveCount(50);
  const duration = page.locator("figure", { hasText: "Duration of the last executions" });
  // The legend icons are also recharts surfaces. The chart surface is the wrapper child.
  await expect(duration.locator(".recharts-wrapper > svg.recharts-surface")).toBeVisible();
  await expect(page.getByRole("table", { name: "Duration of the last executions data" }).getByRole("row")).toHaveCount(
    51,
  );

  // The label wraps the select, so the accessible name also contains the selected option.
  await expect(page.getByRole("combobox", { name: /^Metric/ })).toHaveValue("rows_loaded");
  await page.getByLabel("Group by tag").fill("table");
  const metric = page.getByRole("table", { name: "Metric rows_loaded data" });
  await expect(metric.getByRole("columnheader")).toHaveText(["Label", "customers", "orders"]);
  await expect(page.locator("figure", { hasText: "Metric rows_loaded" }).locator(".recharts-line")).toHaveCount(2);
});
