import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";
import { adminAPI, adminEmail, adminPassword, loginUI, seedNamespace, triggerFlowAPI, waitExecutionState } from "../helpers/app";

/** seriousViolations returns the serious and critical axe violations of the page. */
async function seriousViolations(page: Page) {
  const result = await new AxeBuilder({ page }).analyze();
  return result.violations
    .filter((v) => v.impact === "serious" || v.impact === "critical")
    .map((v) => `${v.id}: ${v.help} (${v.nodes.map((n) => n.target.join(" ")).slice(0, 3).join(", ")})`);
}

async function setTheme(page: Page, theme: "Light" | "Dark") {
  await page.getByRole("radio", { name: theme }).first().click();
  await expect(page.getByRole("radio", { name: theme }).first()).toBeChecked();
}

test("SCN-UI-007 main routes have no serious or critical axe violations in both themes and the theme persists after reload", async ({
  page,
}) => {
  test.setTimeout(180_000);
  const api = await adminAPI();
  const ns = await seedNamespace(api, "ui007", {
    "a.flow.yaml": 'id: a\ntasks:\n  - {id: t, type: command, command: ["echo", "hello"]}\n',
  });
  const exec = await triggerFlowAPI(api, ns, "a");
  await waitExecutionState(api, exec.id, ["SUCCESS"]);

  await loginUI(page, adminEmail, adminPassword);
  const pages: [string, string][] = [
    ["dashboard", "/"],
    ["executions", "/executions"],
    ["execution detail", `/executions/${exec.id}`],
    ["flows", "/flows"],
    ["flow detail", `/flows/${ns}/a`],
    ["namespace files", `/namespaces/${ns}`],
    ["secrets", "/secrets"],
    ["settings", "/settings/profile"],
  ];
  const problems: string[] = [];
  for (const theme of ["Light", "Dark"] as const) {
    await page.goto("/");
    await setTheme(page, theme);
    for (const [name, path] of pages) {
      await page.goto(path);
      await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible();
      await page.waitForLoadState("networkidle");
      for (const v of await seriousViolations(page)) problems.push(`${theme} ${name}: ${v}`);
    }
  }
  expect(problems).toEqual([]);

  await setTheme(page, "Dark");
  await page.reload();
  await expect(page.getByRole("radio", { name: "Dark" }).first()).toBeChecked();
  await expect(page.locator("html")).toHaveClass(/dark/);
});
