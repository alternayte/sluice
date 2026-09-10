import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { expect, request, type APIRequestContext, type Browser, type Page } from "@playwright/test";
import { adminEmail, adminPassword, stateFile } from "./constants";

export { adminEmail, adminPassword };

export function baseURL(): string {
  const u = process.env.SLUICE_UI_BASE_URL;
  if (!u) throw new Error("SLUICE_UI_BASE_URL is not set");
  return u;
}

/** uniq returns a unique suffix for test data. */
export function uniq(): string {
  return `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
}

/** loginUI signs in through the login page. */
export async function loginUI(page: Page, email: string, password: string) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).not.toHaveURL(/\/login/);
}

/** adminAPI returns an API context with an admin token. */
export async function adminAPI(): Promise<APIRequestContext> {
  const cookieCtx = await request.newContext({ baseURL: baseURL() });
  const login = await cookieCtx.post("/api/v1/auth/login", { data: { email: adminEmail, password: adminPassword } });
  expect(login.status()).toBe(200);
  const tok = await cookieCtx.post("/api/v1/tokens", {
    data: { name: `ui-${uniq()}`, role: "admin" },
    headers: { Origin: baseURL() },
  });
  expect(tok.status()).toBe(201);
  const secret = ((await tok.json()) as { secret: string }).secret;
  await cookieCtx.dispose();
  return request.newContext({ baseURL: baseURL(), extraHTTPHeaders: { Authorization: `Bearer ${secret}` } });
}

/** createUserAPI creates a user with a final password (no forced change). */
export async function createUserAPI(api: APIRequestContext, role: string, password = "user-password-1") {
  const email = `${role}-${uniq()}@example.com`;
  const res = await api.post("/api/v1/users", { data: { email, role, password: "temporary-pass-1" } });
  expect(res.status()).toBe(201);
  const ctx = await request.newContext({ baseURL: baseURL() });
  expect((await ctx.post("/api/v1/auth/login", { data: { email, password: "temporary-pass-1" } })).status()).toBe(200);
  const ch = await ctx.post("/api/v1/auth/password", {
    data: { current_password: "temporary-pass-1", new_password: password },
    headers: { Origin: baseURL() },
  });
  expect(ch.status()).toBe(204);
  await ctx.dispose();
  return { email, password };
}

/** signInAs creates a user with the role and signs in with it in a new browser context. */
export async function signInAs(browser: Browser, api: APIRequestContext, role: string) {
  const user = await createUserAPI(api, role);
  const context = await browser.newContext({ baseURL: baseURL() });
  const page = await context.newPage();
  await loginUI(page, user.email, user.password);
  return { context, page, email: user.email };
}

/** saveFilesAPI saves files in one new version. Shell scripts get the executable bit. */
export async function saveFilesAPI(api: APIRequestContext, ns: string, files: Record<string, string>, message: string) {
  const changes = Object.entries(files).map(([path, content]) => ({
    op: "put",
    path,
    content,
    ...(path.endsWith(".sh") ? { executable: true } : {}),
  }));
  const res = await api.post(`/api/v1/namespaces/${ns}/changes`, { data: { message, changes } });
  expect(res.status(), await res.text()).toBe(201);
}

/** seedNamespace creates a managed namespace with a unique name and saves the files as version 1. */
export async function seedNamespace(api: APIRequestContext, prefix: string, files: Record<string, string>) {
  const ns = `${prefix}-${uniq()}`;
  const res = await api.post("/api/v1/namespaces", { data: { name: ns } });
  expect(res.status(), await res.text()).toBe(201);
  if (Object.keys(files).length > 0) await saveFilesAPI(api, ns, files, "Seed files");
  return ns;
}

export type TaskRun = {
  id: string;
  task_key: string;
  attempt: number;
  state: string;
  reused_from_id?: string | null;
};

export type Execution = {
  id: string;
  state: string;
  snapshot_version?: number | null;
  inputs?: Record<string, unknown>;
  labels?: Record<string, string>;
  task_runs: TaskRun[];
};

/** triggerFlowAPI starts a flow through the API and returns the execution. */
export async function triggerFlowAPI(
  api: APIRequestContext,
  ns: string,
  flowId: string,
  body: { inputs?: Record<string, unknown>; labels?: Record<string, string> } = {},
): Promise<Execution> {
  const res = await api.post(`/api/v1/flows/${ns}/${flowId}/executions`, { data: body });
  expect(res.status(), await res.text()).toBe(201);
  return (await res.json()) as Execution;
}

/** runFileAPI runs a namespace file through the API and returns the execution. */
export async function runFileAPI(api: APIRequestContext, ns: string, path: string, args: string[] = []) {
  const res = await api.post(`/api/v1/namespaces/${ns}/run`, { data: { path, args } });
  expect(res.status(), await res.text()).toBe(201);
  return (await res.json()) as Execution;
}

export async function getExecutionAPI(api: APIRequestContext, id: string): Promise<Execution> {
  const res = await api.get(`/api/v1/executions/${id}`);
  expect(res.status()).toBe(200);
  return (await res.json()) as Execution;
}

/** waitExecutionState polls the API until the execution has one of the states. */
export async function waitExecutionState(api: APIRequestContext, id: string, states: string[], timeout = 60_000) {
  await expect
    .poll(async () => (await getExecutionAPI(api, id)).state, { timeout, message: `execution ${id} state` })
    .toMatch(new RegExp(`^(${states.join("|")})$`));
  return getExecutionAPI(api, id);
}

/** cancelExecutionAPI asks the server to cancel an execution. It ignores the result, for cleanup. */
export async function cancelExecutionAPI(api: APIRequestContext, id: string) {
  await api.post(`/api/v1/executions/${id}/cancel`);
}

/** executionHeading returns the heading of the execution detail page, which holds the state badge. */
export function executionHeading(page: Page, ns: string) {
  return page.getByRole("heading", { level: 1, name: new RegExp(`^${ns}/`) });
}

/** ganttRow returns the timeline row of a task attempt, for example "extract #1". */
export function ganttRow(page: Page, label: string) {
  return page.getByTestId("gantt").getByRole("listitem").filter({ hasText: label });
}

/** dbQuery runs SQL in the Postgres container of the global setup and returns the text output. */
export function dbQuery(sql: string): string {
  const { container } = JSON.parse(readFileSync(stateFile, "utf8")) as { container: string };
  return execFileSync("docker", ["exec", container, "psql", "-U", "sluice", "-d", "sluice", "-tAc", sql])
    .toString()
    .trim();
}
