import { spawn, type ChildProcess } from "node:child_process";
import path from "node:path";
import readline from "node:readline";
import { expect, test } from "@playwright/test";
import { adminAPI, adminEmail, adminPassword, loginUI, uniq } from "../helpers/app";
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

test("SCN-UI-006 instances show pools and executors, storage shows driver and health, git source create and provider create and check work", async ({
  page,
}) => {
  test.setTimeout(120_000);
  await loginUI(page, adminEmail, adminPassword);

  // Instances: the Playwright server runs the default pool with the process executor.
  await page.goto("/settings/instances");
  const instance = page.getByRole("row").filter({ hasText: "Online" }).first();
  await expect(instance.getByRole("cell").nth(2)).toHaveText(/\bdefault\b/);
  await expect(instance.getByRole("cell").nth(3)).toHaveText(/\bprocess\b/);

  // Storage: the default driver is Postgres, and the health round trip passes.
  await page.goto("/settings/storage");
  const status = page.locator("dl");
  await expect(status).toContainText("Postgres");
  await expect(status).toContainText("Healthy");

  // Git source: create through the form with a token from a global secret, then wait for the first sync.
  const repo = `set-${uniq()}`;
  const { http_url } = await control<{ http_url: string }>("POST", `/repos/${repo}`);
  await control("POST", `/repos/${repo}/commits`, { branch: "main", message: "first", files: [{ Path: "a.py", Content: "print(1)\n" }] });
  const api = await adminAPI();
  const key = `GIT_SET_${uniq()}`;
  expect((await api.put(`/api/v1/secrets/${key}`, { data: { value: info.token } })).status()).toBe(200);
  const name = `set-${uniq()}`;
  const ns = `gitset-${uniq()}`;
  await page.goto("/settings/git");
  await page.getByRole("button", { name: "Add git source" }).click();
  const gitDialog = page.getByRole("dialog", { name: "Add git source" });
  await gitDialog.getByLabel("Name", { exact: true }).fill(name);
  await gitDialog.getByLabel("Repository URL").fill(http_url);
  await gitDialog.getByLabel("Credential secret key").fill(key);
  await gitDialog.getByLabel("Namespace").fill(ns);
  await gitDialog.getByRole("button", { name: "Add git source" }).click();
  await expect(gitDialog).toBeHidden();
  const source = page.getByRole("row").filter({ hasText: name });
  await expect(source).toContainText(ns);
  await expect(source).toContainText("Synced", { timeout: 30_000 });

  // Providers: create a Vault provider, then check the env provider with a set and an unset reference.
  const provider = `vault-${uniq()}`;
  await page.goto("/settings/secret-providers");
  await page.getByRole("button", { name: "Add provider" }).click();
  const addDialog = page.getByRole("dialog", { name: "Add provider" });
  await addDialog.getByLabel("Name", { exact: true }).fill(provider);
  await addDialog.getByLabel("KV v2 mount").fill("kv");
  await addDialog.getByRole("button", { name: "Add provider" }).click();
  await expect(addDialog).toBeHidden();
  const created = page.getByRole("row").filter({ hasText: provider });
  await expect(created).toContainText("HashiCorp Vault");
  await expect(created).toContainText("mount=kv");

  await page.getByRole("button", { name: "Check env" }).click();
  const checkDialog = page.getByRole("dialog", { name: "Check provider" });
  await checkDialog.getByLabel("Reference").fill("UI_CHECK");
  await checkDialog.getByRole("button", { name: "Check", exact: true }).click();
  await expect(checkDialog.getByRole("status")).toContainText("The reference resolves.");
  await expect(checkDialog).not.toContainText("ui-check-value");
  await checkDialog.getByLabel("Reference").fill(`MISSING_${uniq()}`);
  await checkDialog.getByRole("button", { name: "Check", exact: true }).click();
  await expect(checkDialog.getByRole("status")).toContainText("Check result:");
});
