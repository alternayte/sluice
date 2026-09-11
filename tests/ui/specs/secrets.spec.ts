import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import {
  adminAPI,
  saveFilesAPI,
  seedNamespace,
  signInAs,
  triggerFlowAPI,
  uniq,
  waitExecutionState,
} from "../helpers/app";

/** childNamespace creates `<parent>.child` next to a seeded parent namespace. */
async function childNamespace(api: APIRequestContext, parent: string) {
  const child = `${parent}.child`;
  const res = await api.post("/api/v1/namespaces", { data: { name: child } });
  expect(res.status(), await res.text()).toBe(201);
  return child;
}

async function addSecret(page: Page, key: string, value: string) {
  await page.getByRole("button", { name: "Add secret" }).click();
  const dialog = page.getByRole("dialog", { name: "Add secret" });
  await dialog.getByLabel("Key").fill(key);
  await dialog.getByLabel("Value").fill(value);
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog).toBeHidden();
}

async function addVariable(page: Page, key: string, value: string) {
  await page.getByRole("button", { name: "Add variable" }).click();
  const dialog = page.getByRole("dialog", { name: "Add variable" });
  await dialog.getByLabel("Key").fill(key);
  await dialog.getByLabel("Value").fill(value);
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog).toBeHidden();
}

function row(page: Page, key: string) {
  return page.getByRole("row").filter({ has: page.getByRole("cell", { name: key, exact: true }) });
}

type AuditEvent = { action: string; actor_label: string; target_id: string };

test("SCN-SEC-002 an editor creates and updates a builtin namespace secret and never sees the value", async ({ browser }) => {
  const api = await adminAPI();
  const ns = await seedNamespace(api, "sec002", {});
  const { page, email } = await signInAs(browser, api, "editor");
  await page.goto(`/namespaces/${ns}?tab=secrets`);

  await page.getByRole("button", { name: "Add secret" }).click();
  const dialog = page.getByRole("dialog", { name: "Add secret" });
  await dialog.getByLabel("Value").fill("abc");
  await expect(dialog.getByRole("alert")).toContainText("not masked");
  await dialog.getByRole("button", { name: "Cancel" }).click();

  await addSecret(page, "DB_PASSWORD", "first-secret-value");
  await expect(row(page, "DB_PASSWORD")).toContainText("builtin");
  await expect(page.locator("body")).not.toContainText("first-secret-value");

  const listed = await api.get(`/api/v1/namespaces/${ns}/secrets`);
  const text = await listed.text();
  expect(text).not.toContain("first-secret-value");
  const items = (JSON.parse(text) as { items: Record<string, unknown>[] }).items;
  expect(items).toHaveLength(1);
  expect(items[0]).not.toHaveProperty("value");

  await row(page, "DB_PASSWORD").getByRole("button", { name: "Edit DB_PASSWORD" }).click();
  const edit = page.getByRole("dialog", { name: "Edit secret" });
  await expect(edit.getByLabel("Value")).toHaveValue("");
  await edit.getByLabel("Value").fill("second-secret-value");
  await edit.getByLabel("Description").fill("rotated");
  await edit.getByRole("button", { name: "Save" }).click();
  await expect(edit).toBeHidden();
  await expect(page.locator("body")).not.toContainText("second-secret-value");

  const audit = await api.get(`/api/v1/audit?target=${encodeURIComponent(`secret:${ns}/DB_PASSWORD`)}`);
  expect(audit.status()).toBe(200);
  const events = ((await audit.json()) as { items: AuditEvent[] }).items;
  const actions = events.filter((e) => e.actor_label === email).map((e) => e.action);
  expect(actions).toContain("secret.create");
  expect(actions).toContain("secret.update");
  expect(JSON.stringify(events)).not.toContain("secret-value");
});

test("SCN-SEC-007 variables at global and namespace scope show inherited values and a flow logs them", async ({ browser }) => {
  const api = await adminAPI();
  const parent = await seedNamespace(api, "sec007", {});
  const child = await childNamespace(api, parent);
  const greeting = `GREETING_${uniq()}`;
  const target = `TARGET_${uniq()}`;
  await saveFilesAPI(
    api,
    child,
    { "v.flow.yaml": `id: v\ntasks:\n  - {id: t, type: command, command: ["echo", "vars \${{ vars.${greeting} }} \${{ vars.${target} }}"]}\n` },
    "Flow",
  );
  const { page } = await signInAs(browser, api, "admin");

  await page.goto("/variables");
  await addVariable(page, greeting, "hello-global");
  await page.goto(`/namespaces/${parent}?tab=variables`);
  await addVariable(page, target, "parent-target");

  await page.goto(`/namespaces/${child}?tab=variables`);
  await expect(row(page, greeting)).toContainText("Inherited from global");
  await expect(row(page, greeting)).toContainText("hello-global");
  await expect(row(page, target)).toContainText(`Inherited from ${parent}`);
  await expect(row(page, target)).toContainText("parent-target");

  const exec = await triggerFlowAPI(api, child, "v");
  await waitExecutionState(api, exec.id, ["SUCCESS"]);
  await page.goto(`/executions/${exec.id}`);
  await expect(page.getByLabel("Log lines")).toContainText("vars hello-global parent-target");
});

test("SCN-UI-010 an invalid flow shows an error marker within 1 s and the secrets page shows an inherited key with its scope", async ({
  browser,
}) => {
  const api = await adminAPI();
  const parent = await seedNamespace(api, "ui010", {});
  const child = await childNamespace(api, parent);
  await saveFilesAPI(api, child, { "f.flow.yaml": "id: f\ntasks:\n  - {id: t, type: command, command: [\"true\"]}\n" }, "Flow");
  const put = await api.put(`/api/v1/namespaces/${parent}/secrets/SHARED_TOKEN`, { data: { value: "shared-token-value" } });
  expect(put.status(), await put.text()).toBe(200);
  const { page } = await signInAs(browser, api, "editor");

  await page.goto(`/namespaces/${child}`);
  await page.getByRole("button", { name: "f.flow.yaml", exact: true }).click();
  const editor = page.getByRole("textbox", { name: "Content of f.flow.yaml" });
  await editor.click();
  await page.keyboard.press("ControlOrMeta+End");
  await page.keyboard.type("\n  - {id: u, type: command, depends_on: [missing], command: [\"true\"]}");
  const typed = Date.now();
  await expect(page.locator(".cm-lint-marker-error").first()).toBeVisible({ timeout: 1000 });
  expect(Date.now() - typed).toBeLessThan(1000);

  await page.goto(`/namespaces/${child}?tab=secrets`);
  await expect(row(page, "SHARED_TOKEN")).toContainText(`Inherited from ${parent}`);
  await expect(page.locator("body")).not.toContainText("shared-token-value");
});

test("SCN-AUTH-010 the audit page shows a token creation and a secret update with the actor and filters them", async ({ browser }) => {
  const api = await adminAPI();
  const { page, email } = await signInAs(browser, api, "admin");
  const key = `AUDIT_${uniq()}`;

  await page.goto("/settings/tokens");
  await page.getByRole("button", { name: "Create token" }).click();
  const tokenDialog = page.getByRole("dialog", { name: "Create token" });
  await tokenDialog.getByLabel("Name").fill(`audit-${uniq()}`);
  await tokenDialog.getByRole("button", { name: "Create token" }).click();
  await tokenDialog.getByRole("button", { name: "Done" }).click();

  await page.goto("/secrets");
  await addSecret(page, key, "audit-value-1");
  await row(page, key).getByRole("button", { name: `Edit ${key}` }).click();
  const edit = page.getByRole("dialog", { name: "Edit secret" });
  await edit.getByLabel("Value").fill("audit-value-2");
  await edit.getByRole("button", { name: "Save" }).click();
  await expect(edit).toBeHidden();

  await page.goto("/settings/audit");
  await page.getByLabel("Action").fill("token.create");
  await page.getByLabel("Actor").fill(email);
  await page.getByRole("button", { name: "Apply filters" }).click();
  const tokenRows = page.getByRole("row").filter({ hasText: "token.create" });
  await expect(tokenRows.first()).toContainText(email);
  await expect(page.getByRole("row").filter({ hasText: "secret.update" })).toHaveCount(0);

  await page.getByLabel("Action").fill("secret.update");
  await page.getByRole("button", { name: "Apply filters" }).click();
  const secretRow = page.getByRole("row").filter({ hasText: `global/${key}` });
  await expect(secretRow).toContainText("secret.update");
  await expect(secretRow).toContainText(email);
  await expect(page.getByRole("row").filter({ hasText: "token.create" })).toHaveCount(0);
  await expect(page.locator("body")).not.toContainText("audit-value");
});
