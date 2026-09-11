import { expect, test } from "@playwright/test";
import { adminAPI, executionHeading, getExecutionAPI, saveFilesAPI, seedNamespace, signInAs } from "../helpers/app";

test("SCN-FLOW-004 two saves of a flow show two revisions and a diff", async ({ browser }) => {
  const api = await adminAPI();
  const flow = (description: string) =>
    `id: rev\ndescription: ${description}\ntasks:\n  - id: say\n    type: command\n    command: ["echo", "hi"]\n`;
  const ns = await seedNamespace(api, "flow004", { "rev.flow.yaml": flow("first") });
  await saveFilesAPI(api, ns, { "rev.flow.yaml": flow("second") }, "Change description");
  const { context, page } = await signInAs(browser, api, "editor");

  await page.goto(`/flows/${ns}/rev?tab=revisions`);
  const table = page.getByRole("table");
  const rows = table.getByRole("row").filter({ has: page.getByRole("cell") });
  await expect(rows).toHaveCount(2);
  await expect(rows.filter({ has: page.getByRole("cell", { name: "v1", exact: true }) })).toHaveCount(1);
  await expect(rows.filter({ has: page.getByRole("cell", { name: "v2", exact: true }) })).toHaveCount(1);

  const from = page.getByLabel("From", { exact: true });
  const to = page.getByLabel("To", { exact: true });
  await from.selectOption((await from.getByRole("option", { name: /^v1,/ }).getAttribute("value"))!);
  await to.selectOption((await to.getByRole("option", { name: /^v2,/ }).getAttribute("value"))!);
  const diff = page.getByLabel("Revision diff");
  await expect(diff).toContainText("-description: first");
  await expect(diff).toContainText("+description: second");
  await context.close();
  await api.dispose();
});

test("SCN-FLOW-008 the run form renders typed inputs, shows a missing input and starts an execution", async ({
  browser,
}) => {
  const api = await adminAPI();
  const ns = await seedNamespace(api, "flow008", {
    "form.flow.yaml": [
      "id: form",
      "inputs:",
      "  - { id: name, type: string, required: true }",
      "  - { id: count, type: int, default: 1 }",
      "  - { id: flag, type: boolean }",
      "  - { id: mode, type: select, values: [fast, slow], default: fast }",
      "  - { id: extra, type: json }",
      "tasks:",
      "  - id: show",
      "    type: command",
      '    command: ["echo", "${{ inputs.name }}"]',
      "",
    ].join("\n"),
  });
  const { context, page } = await signInAs(browser, api, "operator");

  await page.goto(`/flows/${ns}/form`);
  await page.getByRole("button", { name: "Run", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Run form" });
  const name = dialog.getByRole("textbox", { name: "name *" });
  const count = dialog.getByRole("spinbutton", { name: "count" });
  const flag = dialog.getByRole("checkbox", { name: "flag" });
  const mode = dialog.getByRole("combobox", { name: "mode" });
  const extra = dialog.getByRole("textbox", { name: "extra" });
  await expect(name).toBeVisible();
  await expect(count).toHaveValue("1");
  await expect(flag).not.toBeChecked();
  await expect(mode).toHaveValue("fast");
  await expect(extra).toBeVisible();

  await dialog.getByRole("button", { name: "Run", exact: true }).click();
  await expect(dialog.getByText("This input is required.")).toBeVisible();
  await expect(name).toHaveAttribute("aria-invalid", "true");
  await expect(page).toHaveURL(new RegExp(`/flows/${ns}/form$`));

  await name.fill("Ada");
  await count.fill("3");
  await flag.check();
  await mode.selectOption("slow");
  await extra.fill('{"k": [1, 2]}');
  await dialog.getByRole("button", { name: "Run", exact: true }).click();

  await expect(page).toHaveURL(/\/executions\/[0-9a-f-]{36}$/);
  await expect(executionHeading(page, ns)).toBeVisible();
  for (const part of ['"name":"Ada"', '"count":3', '"flag":true', '"mode":"slow"', '"extra":{"k":[1,2]}']) {
    await expect(page.getByText(part)).toBeVisible();
  }
  const id = page.url().split("/").pop()!;
  const detail = await getExecutionAPI(api, id);
  expect(detail.inputs).toEqual({ name: "Ada", count: 3, flag: true, mode: "slow", extra: { k: [1, 2] } });
  await context.close();
  await api.dispose();
});

test("an editor rotates a webhook key and the new URL starts an execution", async ({ browser }) => {
  const api = await adminAPI();
  const ns = await seedNamespace(api, "hookrot", {
    "hook.flow.yaml": [
      "id: hook",
      "triggers:",
      "  - id: push",
      "    type: webhook",
      "tasks:",
      "  - id: say",
      "    type: command",
      '    command: ["echo", "hi"]',
      "",
    ].join("\n"),
  });
  const { context, page } = await signInAs(browser, api, "editor");

  await page.goto(`/flows/${ns}/hook?tab=triggers`);
  await page.getByRole("button", { name: "Rotate key of push" }).click();
  const confirm = page.getByRole("dialog", { name: "Rotate webhook key" });
  await expect(confirm).toContainText("The old key stops working");
  await confirm.getByRole("button", { name: "Rotate key", exact: true }).click();

  const result = page.getByRole("dialog", { name: "New webhook URL" });
  await expect(result).toContainText("It is not shown again.");
  await expect(result.getByRole("button", { name: "Copy" })).toBeVisible();
  const url = await result.getByRole("textbox", { name: "Webhook URL" }).inputValue();
  expect(url).toMatch(/\/hooks\/[^/]+$/);

  const res = await fetch(url, { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" });
  expect(res.status, await res.text()).toBe(202);

  await result.getByRole("button", { name: "Done" }).click();
  await expect(result).toBeHidden();
  await context.close();
  await api.dispose();
});
