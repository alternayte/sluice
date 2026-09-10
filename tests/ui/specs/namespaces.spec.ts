import { expect, test, type Page } from "@playwright/test";
import { adminAPI, executionHeading, saveFilesAPI, seedNamespace, signInAs } from "../helpers/app";

/** versionRow returns the row of a version in the versions table, for example "v2". */
function versionRow(page: Page, version: string) {
  return page.getByRole("row").filter({ has: page.getByRole("cell", { name: new RegExp(`^${version}(?!\\d)`) }) });
}

/** createFileUI creates an empty file with the "New file" dialog and waits until the list shows it. */
async function createFileUI(page: Page, path: string) {
  await page.getByRole("button", { name: "New file" }).click();
  const dialog = page.getByRole("dialog", { name: "New file" });
  await dialog.getByLabel("Path").fill(path);
  await dialog.getByRole("button", { name: "Create" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByRole("button", { name: path, exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: path, exact: true })).toBeVisible();
}

/** typeAndSave types content into the open file and saves it with a commit message. */
async function typeAndSave(page: Page, path: string, content: string, message: string) {
  const editor = page.getByRole("textbox", { name: `Content of ${path}` });
  await editor.click();
  // A paste puts the text in as it is. Typed new lines get automatic indentation from the editor.
  await editor.evaluate((el, text) => {
    const data = new DataTransfer();
    data.setData("text/plain", text);
    el.dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true }));
  }, content);
  await expect(editor).toContainText(content.split("\n")[0]!);
  await expect(page.getByText("Unsaved changes")).toBeVisible();
  await page.getByRole("button", { name: "Save", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Save file" });
  await dialog.getByLabel("Commit message").fill(message);
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByText("Unsaved changes")).toBeHidden();
  // The file list shows the new size after it loads the new version.
  const bytes = Buffer.byteLength(content);
  await expect(page.getByRole("row").filter({ hasText: path })).toContainText(`${bytes} B`);
}

test("SCN-NS-002 an editor creates a script and a flow and saves them with a message", async ({ browser }) => {
  const api = await adminAPI();
  const ns = await seedNamespace(api, "ns002", {});
  const { context, page, email } = await signInAs(browser, api, "editor");

  await page.goto(`/namespaces/${ns}`);
  await expect(page.getByText("This namespace has no files.")).toBeVisible();

  await createFileUI(page, "pipelines/load.py");
  await typeAndSave(page, "pipelines/load.py", 'print("load")\n', "Add load script");
  await createFileUI(page, "sync.flow.yaml");
  await typeAndSave(
    page,
    "sync.flow.yaml",
    "id: sync\ntasks:\n  - id: load\n    type: script\n    file: pipelines/load.py\n",
    "Add sync flow",
  );

  await page.getByRole("tab", { name: "Versions" }).click();
  const v2 = versionRow(page, "v2");
  await expect(v2).toContainText(email);
  await expect(v2).toContainText("Add load script");
  const v4 = versionRow(page, "v4");
  await expect(v4).toContainText(email);
  await expect(v4).toContainText("Add sync flow");
  await expect(v4).toContainText("Head");

  await page.getByRole("tab", { name: "Files" }).click();
  await expect(page.getByRole("button", { name: "pipelines/load.py", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "sync.flow.yaml", exact: true })).toBeVisible();
  await context.close();
  await api.dispose();
});

test("SCN-NS-003 diff between versions 1 and 3, and revert to version 1 creates version 4", async ({ browser }) => {
  const api = await adminAPI();
  const ns = await seedNamespace(api, "ns003", { "a.txt": "alpha\n" });
  await saveFilesAPI(api, ns, { "a.txt": "beta\n" }, "Second");
  await saveFilesAPI(api, ns, { "a.txt": "gamma\n", "b.txt": "new\n" }, "Third");
  const { context, page } = await signInAs(browser, api, "editor");

  await page.goto(`/namespaces/${ns}?tab=versions`);
  await expect(versionRow(page, "v3")).toContainText("Head");
  await page.getByLabel("From", { exact: true }).selectOption({ label: "v1" });
  await page.getByLabel("To", { exact: true }).selectOption({ label: "v3" });
  const diffA = page.getByLabel("Diff of a.txt");
  await expect(diffA).toContainText("-alpha");
  await expect(diffA).toContainText("+gamma");
  await expect(diffA).not.toContainText("beta");
  await expect(page.getByLabel("Diff of b.txt")).toContainText("+new");

  await versionRow(page, "v1").getByRole("button", { name: "Revert to this version" }).click();
  const dialog = page.getByRole("dialog", { name: "Revert namespace" });
  await dialog.getByRole("button", { name: "Revert" }).click();
  await expect(dialog).toBeHidden();
  await expect(versionRow(page, "v4")).toContainText("Head");

  await page.getByLabel("From", { exact: true }).selectOption({ label: "v1" });
  await page.getByLabel("To", { exact: true }).selectOption({ label: "v4" });
  await expect(page.getByText("The versions have the same files.")).toBeVisible();

  await page.getByRole("tab", { name: "Files" }).click();
  await page.getByRole("button", { name: "a.txt", exact: true }).click();
  await expect(page.getByRole("textbox", { name: "Content of a.txt" })).toHaveText("alpha");
  await expect(page.getByRole("button", { name: "b.txt", exact: true })).toHaveCount(0);
  await context.close();
  await api.dispose();
});

test("SCN-NS-007 an operator runs hello.py with arguments and the logs show them", async ({ browser }) => {
  test.setTimeout(120_000);
  const api = await adminAPI();
  const ns = await seedNamespace(api, "ns007", {
    "hello.py": 'import sys\nprint("args: " + " ".join(sys.argv[1:]))\n',
  });
  const { context, page } = await signInAs(browser, api, "operator");

  await page.goto(`/namespaces/${ns}?file=hello.py`);
  await page.getByRole("button", { name: "Run", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Run hello.py" });
  await dialog.getByLabel("Arguments").fill("--name\nworld");
  await dialog.getByRole("button", { name: "Run", exact: true }).click();

  await expect(page).toHaveURL(/\/executions\/[0-9a-f-]{36}$/);
  await expect(executionHeading(page, ns)).toContainText("Success", { timeout: 90_000 });
  await expect(page.getByLabel("Log lines")).toContainText("args: --name world");
  await context.close();
  await api.dispose();
});
