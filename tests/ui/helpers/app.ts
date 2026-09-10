import { expect, request, type APIRequestContext, type Page } from "@playwright/test";
import { adminEmail, adminPassword } from "./constants";

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
