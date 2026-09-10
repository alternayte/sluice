import { expect, test, type Page } from "@playwright/test";
import {
  adminAPI,
  cancelExecutionAPI,
  executionHeading,
  seedNamespace,
  signInAs,
  triggerFlowAPI,
  waitExecutionState,
} from "../helpers/app";

const button = (page: Page, name: string) => page.getByRole("button", { name, exact: true });

test("SCN-UI-005 a viewer sees no run, edit or cancel actions, an operator sees run and cancel, an editor sees edit", async ({
  browser,
}) => {
  test.setTimeout(120_000);
  const api = await adminAPI();
  const ns = await seedNamespace(api, "ui005", {
    "hello.sh": "echo hello\n",
    "slow.flow.yaml": 'id: slow\ntasks:\n  - id: wait\n    type: command\n    command: ["sleep", "60"]\n',
  });
  const exec = await triggerFlowAPI(api, ns, "slow");
  try {
    await waitExecutionState(api, exec.id, ["RUNNING"]);

    const viewer = await signInAs(browser, api, "viewer");
    const operator = await signInAs(browser, api, "operator");
    const editor = await signInAs(browser, api, "editor");

    // Namespace list and file page.
    for (const { page } of [viewer, operator, editor]) {
      await page.goto("/namespaces");
      await expect(page.getByRole("link", { name: ns, exact: true })).toBeVisible();
      await page.goto(`/namespaces/${ns}?file=hello.sh`);
      await expect(page.getByRole("heading", { name: "hello.sh", exact: true })).toBeVisible();
      await expect(page.getByRole("textbox", { name: "Content of hello.sh" })).toContainText("echo hello");
    }
    for (const name of ["Run", "Save", "Rename", "Delete", "New file"]) {
      await expect(button(viewer.page, name)).toHaveCount(0);
    }
    await expect(button(operator.page, "Run")).toBeVisible();
    for (const name of ["Save", "Rename", "Delete", "New file"]) {
      await expect(button(operator.page, name)).toHaveCount(0);
    }
    for (const name of ["Run", "Save", "Rename", "Delete", "New file"]) {
      await expect(button(editor.page, name)).toBeVisible();
    }

    // Flow page.
    for (const { page } of [viewer, operator, editor]) {
      await page.goto(`/flows/${ns}/slow`);
      await expect(page.getByRole("heading", { name: "slow", exact: true })).toBeVisible();
    }
    await expect(button(viewer.page, "Run")).toHaveCount(0);
    await expect(viewer.page.getByRole("switch")).toHaveCount(0);
    await expect(button(operator.page, "Run")).toBeVisible();
    await expect(operator.page.getByRole("switch")).toHaveCount(0);
    await expect(button(editor.page, "Run")).toBeVisible();
    await expect(editor.page.getByRole("switch", { name: "Enabled" })).toBeVisible();

    // Execution page.
    for (const { page } of [viewer, operator, editor]) {
      await page.goto(`/executions/${exec.id}`);
      await expect(executionHeading(page, ns)).toContainText("Running");
    }
    await expect(button(viewer.page, "Cancel")).toHaveCount(0);
    await expect(button(viewer.page, "Rerun")).toHaveCount(0);
    await expect(button(operator.page, "Cancel")).toBeVisible();
    await expect(button(editor.page, "Cancel")).toBeVisible();

    // Only an editor can create a namespace.
    for (const { page } of [viewer, operator, editor]) await page.goto("/namespaces");
    await expect(editor.page.getByRole("button", { name: "Create namespace" })).toBeVisible();
    await expect(viewer.page.getByRole("button", { name: "Create namespace" })).toHaveCount(0);
    await expect(operator.page.getByRole("button", { name: "Create namespace" })).toHaveCount(0);

    for (const s of [viewer, operator, editor]) await s.context.close();
  } finally {
    await cancelExecutionAPI(api, exec.id);
    await api.dispose();
  }
});
