import { expect, test, type Page } from "@playwright/test";
import { adminAPI, adminEmail, adminPassword, loginUI, seedNamespace, triggerFlowAPI, waitExecutionState } from "../helpers/app";

test.use({ viewport: { width: 390, height: 844 } });

async function pageOverflow(page: Page): Promise<number> {
  return page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
}

test("SCN-UI-009 at 390 px the dashboard, executions and execution detail have no page-level horizontal scroll and logs scroll inside their panel", async ({
  page,
}) => {
  const api = await adminAPI();
  const script = 'for i in $(seq 1 200); do echo "line $i $(printf \'x%.0s\' $(seq 1 300))"; done\n';
  const ns = await seedNamespace(api, "ui009", {
    "long.sh": script,
    "long.flow.yaml": "id: long\ntasks:\n  - {id: t, type: script, file: long.sh}\n",
  });
  const exec = await triggerFlowAPI(api, ns, "long");
  await waitExecutionState(api, exec.id, ["SUCCESS"]);

  await loginUI(page, adminEmail, adminPassword);
  for (const path of ["/", "/executions", `/executions/${exec.id}`]) {
    await page.goto(path);
    await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible();
    await page.waitForLoadState("networkidle");
    expect(await pageOverflow(page), `page-level horizontal scroll on ${path}`).toBeLessThanOrEqual(0);
  }

  const panel = page.getByTestId("log-panel");
  await expect(panel).toContainText("line 200");
  const scroll = await panel.evaluate((el) => {
    const scroller = [el, ...Array.from(el.querySelectorAll<HTMLElement>("*"))].find((n) => {
      const s = getComputedStyle(n);
      return (s.overflowY === "auto" || s.overflowY === "scroll") && n.scrollHeight > n.clientHeight;
    });
    return scroller ? { inner: true, width: scroller.clientWidth } : { inner: false, width: 0 };
  });
  expect(scroll.inner, "the log lines scroll inside their panel").toBe(true);
  expect(scroll.width).toBeLessThanOrEqual(390);
  expect(await pageOverflow(page)).toBeLessThanOrEqual(0);
});
