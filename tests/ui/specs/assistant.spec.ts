import { spawn, type ChildProcess } from "node:child_process";
import path from "node:path";
import readline from "node:readline";
import { expect, test, type APIRequestContext, type Locator, type Page } from "@playwright/test";
import {
  adminAPI,
  adminEmail,
  adminPassword,
  loginUI,
  seedNamespace,
  triggerFlowAPI,
  uniq,
  waitExecutionState,
} from "../helpers/app";
import { uiTestsDir } from "../helpers/constants";

type LLMInfo = { control: string; anthropic_url: string };
type GitInfo = { control: string; http_base: string; token: string };
type Reply = {
  status?: number;
  text?: string;
  tool_calls?: { id: string; name: string; input: string }[];
  json?: string;
};

const children: ChildProcess[] = [];
let llm: LLMInfo;
let git: GitInfo;
let api: APIRequestContext;

/** startFixture runs a Go fixture program (SDD §10.4) and reads the JSON line with its URLs. */
function startFixture<T>(pkg: string): Promise<T> {
  const child = spawn("go", ["run", pkg], {
    cwd: path.resolve(uiTestsDir, "../.."),
    stdio: ["ignore", "pipe", "inherit"],
  });
  children.push(child);
  return new Promise<T>((resolve, reject) => {
    readline.createInterface({ input: child.stdout! }).once("line", (line) => resolve(JSON.parse(line) as T));
    child.once("exit", (code) => reject(new Error(`the fixture ${pkg} exited with ${code}`)));
  });
}

async function control(base: string, method: string, p: string, body?: unknown): Promise<unknown> {
  const res = await fetch(base + p, {
    method,
    body: body === undefined ? undefined : JSON.stringify(body),
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
  });
  expect(res.ok, `${method} ${p}`).toBe(true);
  return res.status === 204 ? undefined : res.json();
}

/** script replaces the replies of the scripted model. */
async function script(replies: Reply[]) {
  await control(llm.control, "POST", "/reset");
  await control(llm.control, "POST", "/script", replies);
}

async function recorded(): Promise<string[]> {
  const reqs = (await control(llm.control, "GET", "/requests")) as { body: unknown }[];
  return reqs.map((r) => JSON.stringify(r.body));
}

const call = (id: string, name: string, input: unknown): Reply => ({
  tool_calls: [{ id, name, input: JSON.stringify(input) }],
});

async function openAssistant(page: Page): Promise<Locator> {
  await page.getByRole("button", { name: "Assistant" }).click();
  return page.getByRole("complementary", { name: "Assistant" });
}

async function newConversation(panel: Locator) {
  await panel.getByRole("button", { name: "New conversation" }).click();
  await expect(panel.getByRole("list", { name: "Conversation messages" }).getByRole("listitem")).toHaveCount(0);
}

async function ask(panel: Locator, text: string) {
  // exact: the message list is labelled "Conversation messages".
  await panel.getByLabel("Message", { exact: true }).fill(text);
  await panel.getByRole("button", { name: "Send" }).click();
}

async function executionCount(ns: string, flowId: string): Promise<number> {
  const res = await api.get(`/api/v1/executions?flow=${encodeURIComponent(`${ns}/${flowId}`)}`);
  return ((await res.json()) as { items: unknown[] }).items.length;
}

const okFlow = 'id: job\ntasks:\n  - {id: t, type: command, command: ["true"]}\n';
const genValid = 'id: gen\ntasks:\n  - {id: t, type: command, command: ["true"]}\n';
const genInvalid = "id: gen\ntasks: []\n";

test.describe.configure({ mode: "serial" });

test.beforeAll(async ({}, testInfo) => {
  testInfo.setTimeout(240_000);
  [llm, git] = await Promise.all([
    startFixture<LLMInfo>("./tests/fixtures/llmserver"),
    startFixture<GitInfo>("./tests/fixtures/gitserver"),
  ]);
  api = await adminAPI();
  const put = await api.put("/api/v1/secrets/LLM_UI_KEY", { data: { value: "llm-ui-key-value" } });
  expect(put.status(), await put.text()).toBe(200);
});

test.afterAll(async () => {
  // Later spec files expect no AI provider.
  await api?.delete("/api/v1/ai/provider");
  for (const c of children) c.kill("SIGTERM");
});

test("SCN-AI-001 without a provider the UI hides AI actions", async ({ page }) => {
  expect((await api.delete("/api/v1/ai/provider")).status()).toBe(204);
  const ns = await seedNamespace(api, "noai", {
    "bad.flow.yaml": 'id: bad\ntasks:\n  - {id: t, type: command, command: ["false"]}\n',
  });
  const ex = await waitExecutionState(api, (await triggerFlowAPI(api, ns, "bad")).id, ["FAILED"]);

  await loginUI(page, adminEmail, adminPassword);
  await page.goto(`/executions/${ex.id}`);
  await expect(page.getByRole("heading", { level: 1, name: new RegExp(`^${ns}/`) })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Failure triage" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Assistant" })).toHaveCount(0);
  await page.goto("/settings/ai");
  await expect(page.getByRole("status").filter({ hasText: "No provider is configured" })).toBeVisible();
});

test("SCN-UI-006 the AI provider test works", async ({ page }) => {
  await loginUI(page, adminEmail, adminPassword);
  await page.goto("/settings/ai");
  await page.getByLabel("Type").selectOption("anthropic");
  await page.getByLabel("Base URL").fill(llm.anthropic_url);
  await page.getByLabel("Model", { exact: true }).fill("claude-ui");
  await page.getByLabel("API key secret key").fill("LLM_UI_KEY");
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByRole("status").filter({ hasText: "A provider is configured." })).toBeVisible();

  await script([{ text: "OK" }]);
  await page.getByRole("button", { name: "Test provider" }).click();
  await expect(page.getByRole("status").filter({ hasText: "The provider answered." })).toBeVisible();
  expect((await recorded())[0]).toContain("Reply with the word OK.");
  await expect(page.getByRole("button", { name: "Assistant" })).toBeVisible();
});

test("SCN-AI-004 the panel shows the get_execution and get_logs calls and the answer, and keeps the conversation after a reload", async ({
  page,
}) => {
  const ns = await seedNamespace(api, "ai004", {
    "bad.flow.yaml":
      'id: bad\ntasks:\n  - {id: t, type: command, command: ["sh", "-c", "echo \'ERROR: disk full\'; exit 1"]}\n',
  });
  const ex = await waitExecutionState(api, (await triggerFlowAPI(api, ns, "bad")).id, ["FAILED"]);
  await script([
    call("c1", "get_execution", { execution_id: ex.id }),
    call("c2", "get_logs", { execution_id: ex.id }),
    { text: "The execution failed because the disk is full." },
  ]);

  await loginUI(page, adminEmail, adminPassword);
  const panel = await openAssistant(page);
  await ask(panel, "Why did the last execution fail?");
  await expect(panel.getByText("Tool call get_execution")).toBeVisible();
  await expect(panel.getByText("Tool call get_logs")).toBeVisible();
  await expect(panel.getByText("The execution failed because the disk is full.")).toBeVisible();
  const reqs = await recorded();
  expect(reqs).toHaveLength(3);
  expect(reqs[1]).toContain(ex.id);
  expect(reqs[2]).toContain("ERROR: disk full");

  await page.reload();
  const again = await openAssistant(page);
  await expect(again.getByText("The execution failed because the disk is full.")).toBeVisible();
  await expect(again.getByText("Tool call get_logs")).toBeVisible();
});

test("SCN-AI-005 a trigger_execution call waits for confirm or reject, reject does not run it, confirm runs it with an audit event", async ({
  page,
}) => {
  test.setTimeout(120_000);
  const ns = await seedNamespace(api, "ai005", { "job.flow.yaml": okFlow });
  await script([
    call("t1", "trigger_execution", { namespace: ns, flow_id: "job" }),
    { text: "I did not run the job." },
  ]);

  await loginUI(page, adminEmail, adminPassword);
  const panel = await openAssistant(page);
  await newConversation(panel);
  await ask(panel, "Run the job.");
  let card = panel.getByRole("listitem", { name: "Action trigger_execution" });
  await expect(card.getByRole("button", { name: "Confirm" })).toBeVisible();
  await expect(card.getByRole("button", { name: "Reject" })).toBeVisible();
  expect(await executionCount(ns, "job")).toBe(0);

  await card.getByRole("button", { name: "Reject" }).click();
  await expect(panel.getByText("I did not run the job.")).toBeVisible();
  expect(await executionCount(ns, "job")).toBe(0);
  expect((await recorded())[1]).toContain("the user rejected this action");

  await script([call("t2", "trigger_execution", { namespace: ns, flow_id: "job" }), { text: "The job started." }]);
  await ask(panel, "Run it now.");
  card = panel.getByRole("listitem", { name: "Action trigger_execution" });
  await card.getByRole("button", { name: "Confirm" }).click();
  await expect(panel.getByText("The job started.")).toBeVisible();
  await expect.poll(() => executionCount(ns, "job")).toBe(1);

  const audit = await api.get("/api/v1/audit?action=ai.action.confirmed");
  const events = ((await audit.json()) as { items: { action: string; actor_label: string; target_id: string }[] })
    .items;
  expect(events[0]).toMatchObject({ action: "ai.action.confirmed", target_id: "trigger_execution" });
  expect(events[0]!.actor_label).toContain(adminEmail);
});

test("SCN-AI-006 an invalid proposal returns errors, a valid proposal shows the diff, and apply creates a version or pushes a branch", async ({
  page,
}) => {
  test.setTimeout(180_000);
  const ns = await seedNamespace(api, "ai006", { "job.flow.yaml": okFlow });
  const change = (content: string, namespace = ns) => ({
    namespace,
    message: "Add the gen flow",
    files: [{ path: "gen.flow.yaml", content }],
  });
  await script([
    call("p1", "propose_change", change(genInvalid)),
    call("p2", "propose_change", change(genValid)),
    call("a1", "apply_change", change(genValid)),
    { text: "The flow is applied." },
  ]);

  await loginUI(page, adminEmail, adminPassword);
  const panel = await openAssistant(page);
  await newConversation(panel);
  await ask(panel, "Write a flow gen that runs true.");
  await expect(panel.getByRole("list", { name: "Issues of gen.flow.yaml" })).toBeVisible();
  await expect(panel.getByLabel("Diff of gen.flow.yaml")).toHaveCount(2);
  await expect(panel.getByLabel("Diff of gen.flow.yaml").last()).toContainText("+id: gen");
  const reqs = await recorded();
  expect(reqs[1]).toContain('\\"valid\\":false');
  expect(reqs[2]).toContain('\\"valid\\":true');
  expect((await api.get(`/api/v1/flows/${ns}/gen`)).status()).toBe(404);

  await panel.getByRole("listitem", { name: "Action apply_change" }).getByRole("button", { name: "Confirm" }).click();
  await expect(panel.getByText("The flow is applied.")).toBeVisible();
  expect((await api.get(`/api/v1/flows/${ns}/gen`)).status()).toBe(200);

  // Apply on a git namespace pushes a branch.
  const repo = `ai-${uniq()}`;
  const { http_url } = (await control(git.control, "POST", `/repos/${repo}`)) as { http_url: string };
  await control(git.control, "POST", `/repos/${repo}/commits`, {
    branch: "main",
    message: "first",
    files: [{ Path: "job.flow.yaml", Content: okFlow }],
  });
  const key = `GIT_AI_${uniq()}`;
  expect((await api.put(`/api/v1/secrets/${key}`, { data: { value: git.token } })).status()).toBe(200);
  const gitNs = `gitai-${uniq()}`;
  const created = await api.post("/api/v1/git-sources", {
    data: {
      name: `ai-${uniq()}`,
      repo_url: http_url,
      branch: "main",
      auth_type: "https_token",
      credential_secret_key: key,
      mappings: [{ repo_path: "", namespace: gitNs }],
    },
  });
  expect(created.status(), await created.text()).toBe(201);
  const sourceId = ((await created.json()) as { id: string }).id;
  await expect
    .poll(
      async () =>
        ((await (await api.get(`/api/v1/git-sources/${sourceId}/runs`)).json()) as { items: { status: string }[] })
          .items[0]?.status,
      {
        timeout: 30_000,
      },
    )
    .toBe("success");

  await script([call("g1", "apply_change", change(genValid, gitNs)), { text: "The branch is pushed." }]);
  await newConversation(panel);
  await ask(panel, "Add the gen flow to the git namespace.");
  await panel.getByRole("listitem", { name: "Action apply_change" }).getByRole("button", { name: "Confirm" }).click();
  await expect(panel.getByText("The branch is pushed.")).toBeVisible();
  const branches = (await control(git.control, "GET", `/repos/${repo}/branches`)) as string[];
  expect(branches.some((b) => b.startsWith("sluice/"))).toBe(true);
});
