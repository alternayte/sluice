import { spawn, type ChildProcess } from "node:child_process";
import path from "node:path";
import readline from "node:readline";
import { expect, test } from "@playwright/test";
import { adminAPI, signInAs, uniq } from "../helpers/app";
import { uiTestsDir } from "../helpers/constants";

type FixtureInfo = { control: string; http_base: string; token: string };

let fixture: ChildProcess | undefined;
let info: FixtureInfo;

/** Starts the Go git fixture program (SDD §10.4) and reads the JSON line with its URLs. */
test.beforeAll(async ({}, testInfo) => {
  testInfo.setTimeout(180_000);
  const repoRoot = path.resolve(uiTestsDir, "../..");
  const child = spawn("go", ["run", "./tests/fixtures/gitserver"], { cwd: repoRoot, stdio: ["ignore", "pipe", "inherit"] });
  fixture = child;
  info = await new Promise<FixtureInfo>((resolve, reject) => {
    const rl = readline.createInterface({ input: child.stdout! });
    rl.once("line", (line) => resolve(JSON.parse(line) as FixtureInfo));
    child.once("exit", (code) => reject(new Error(`the git fixture exited with ${code}`)));
  });
});

test.afterAll(() => {
  fixture?.kill("SIGTERM");
});

async function control<T>(method: string, p: string, body?: unknown): Promise<T> {
  const res = await fetch(info.control + p, {
    method,
    body: body === undefined ? undefined : JSON.stringify(body),
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
  });
  expect(res.ok, `${method} ${p}`).toBe(true);
  return (await res.json()) as T;
}

type CommitInfo = { sha: string; author_name: string; author_email: string; message: string };

test("SCN-GIT-005 an editor pushes an edit of a git namespace to a new branch and Sync now adds a sync run", async ({ browser }) => {
  const repo = `ui-${uniq()}`;
  const { http_url } = await control<{ http_url: string }>("POST", `/repos/${repo}`);
  const base = (await control<{ sha: string }>("POST", `/repos/${repo}/commits`, {
    branch: "main",
    message: "first",
    files: [{ Path: "load.py", Content: "print('v1')\n" }],
  })).sha;

  const api = await adminAPI();
  const key = `GIT_UI_${uniq()}`;
  const secret = await api.put(`/api/v1/secrets/${key}`, { data: { value: info.token } });
  expect(secret.status(), await secret.text()).toBe(200);
  const ns = `gitui-${uniq()}`;
  const created = await api.post("/api/v1/git-sources", {
    data: {
      name: `ui-${uniq()}`,
      repo_url: http_url,
      branch: "main",
      auth_type: "https_token",
      credential_secret_key: key,
      mappings: [{ repo_path: "", namespace: ns }],
    },
  });
  expect(created.status(), await created.text()).toBe(201);
  const sourceId = ((await created.json()) as { id: string }).id;
  await expect
    .poll(async () => ((await (await api.get(`/api/v1/git-sources/${sourceId}/runs`)).json()) as { items: { status: string }[] }).items[0]?.status, {
      timeout: 30_000,
    })
    .toBe("success");

  const { page, email } = await signInAs(browser, api, "editor");
  await page.goto(`/namespaces/${ns}`);
  await page.getByRole("button", { name: "load.py", exact: true }).click();
  await page.getByRole("textbox", { name: "Content of load.py" }).click();
  await page.keyboard.press("ControlOrMeta+End");
  await page.keyboard.type("print('v2')");
  await page.getByRole("button", { name: "Push to branch" }).click();
  const dialog = page.getByRole("dialog", { name: "Push to branch" });
  await dialog.getByLabel("Commit message").fill("Edit from the UI");
  await dialog.getByRole("button", { name: "Push", exact: true }).click();
  const status = dialog.getByRole("status");
  await expect(status).toContainText("Pushed to branch sluice/");
  const branch = (await status.locator(".font-mono").textContent())!.trim();

  const branches = await control<string[]>("GET", `/repos/${repo}/branches`);
  expect(branches).toContain(branch);
  const pushed = await control<CommitInfo>("GET", `/repos/${repo}/commits/${branch}`);
  expect(pushed.author_email).toBe(email);
  const content = await (await fetch(`${info.control}/repos/${repo}/file?branch=${encodeURIComponent(branch)}&path=load.py`)).text();
  expect(content).toContain("print('v2')");
  expect((await control<CommitInfo>("GET", `/repos/${repo}/commits/main`)).sha).toBe(base);
  await dialog.getByRole("button", { name: "Done" }).click();

  const runs = page.getByRole("table", { name: "Sync runs" }).getByRole("row");
  await expect(runs).toHaveCount(2);
  await page.getByRole("button", { name: "Sync now" }).click();
  await expect(runs).toHaveCount(3, { timeout: 15_000 });
});
