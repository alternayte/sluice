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
  await expect(page.getByRole("treeitem", { name: path, exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: path, exact: true })).toBeVisible();
}

/** typeContent pastes content into the open file. The file stays staged until a save. */
async function typeContent(page: Page, path: string, content: string) {
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
}

test("SCN-NS-002 an editor creates pipelines/load.py and sync.flow.yaml and saves them with one message as version 2", async ({
  browser,
}) => {
  const api = await adminAPI();
  const ns = await seedNamespace(api, "ns002", { "README.md": "# ns002\n" });
  const { context, page, email } = await signInAs(browser, api, "editor");
  await page.goto(`/namespaces/${ns}`);

  const load = 'print("load")\n';
  const flow = "id: sync\ntasks:\n  - id: load\n    type: script\n    file: pipelines/load.py\n";
  await createFileUI(page, "pipelines/load.py");
  await typeContent(page, "pipelines/load.py", load);
  await createFileUI(page, "sync.flow.yaml");
  await typeContent(page, "sync.flow.yaml", flow);
  // The first file keeps its staged content while the second file is open.
  await expect(page.getByRole("status").filter({ hasText: "2 unsaved files" })).toBeVisible();

  await page.getByRole("button", { name: "Save changes" }).click();
  const dialog = page.getByRole("dialog", { name: "Save changes" });
  await expect(dialog.getByRole("list", { name: "Files to save" })).toContainText("pipelines/load.py");
  await expect(dialog.getByRole("list", { name: "Files to save" })).toContainText("sync.flow.yaml");
  await dialog.getByLabel("Commit message").fill("Add the load script and the sync flow");
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByText("unsaved file")).toHaveCount(0);
  await expect(page.getByRole("treeitem", { name: "pipelines/load.py", exact: true })).toContainText(`${Buffer.byteLength(load)} B`);
  await expect(page.getByRole("treeitem", { name: "sync.flow.yaml", exact: true })).toContainText(`${Buffer.byteLength(flow)} B`);

  await page.getByRole("tab", { name: "Versions" }).click();
  const v2 = versionRow(page, "v2");
  await expect(v2).toContainText(email);
  await expect(v2).toContainText("Add the load script and the sync flow");
  await expect(v2).toContainText("Head");
  await expect(versionRow(page, "v3")).toHaveCount(0);
  const files = await api.get(`/api/v1/namespaces/${ns}/files`);
  expect(((await files.json()) as { items: { path: string }[] }).items.map((f) => f.path).sort()).toEqual(["README.md", "pipelines/load.py", "sync.flow.yaml"]);
  expect((await api.get(`/api/v1/flows/${ns}/sync`)).status()).toBe(200);
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
  await page.getByRole("treeitem", { name: "a.txt", exact: true }).click();
  await expect(page.getByRole("textbox", { name: "Content of a.txt" })).toHaveText("alpha");
  await expect(page.getByRole("treeitem", { name: "b.txt", exact: true })).toHaveCount(0);
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
