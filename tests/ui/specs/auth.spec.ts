import { expect, request, test } from "@playwright/test";
import { adminEmail, adminPassword, baseURL, loginUI, uniq } from "../helpers/app";

test("SCN-AUTH-001 login errors, reload, logout and old cookie", async ({ page, context }) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill(adminEmail);
  await page.getByLabel("Password", { exact: true }).fill("wrong-password-x");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByText(/email or password is wrong/i)).toBeVisible();

  await page.getByLabel("Password", { exact: true }).fill(adminPassword);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();

  const cookie = (await context.cookies()).find((c) => c.name === "sluice_session");
  expect(cookie?.httpOnly).toBe(true);
  expect(cookie?.sameSite).toBe("Lax");

  await page.getByRole("button", { name: "Sign out" }).first().click();
  await expect(page).toHaveURL(/\/login/);
  await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();

  const old = await request.newContext({ baseURL: baseURL(), extraHTTPHeaders: { Cookie: `sluice_session=${cookie!.value}` } });
  expect((await old.get("/api/v1/auth/me")).status()).toBe(401);
  await old.dispose();
});

test("SCN-AUTH-003 admin creates an editor who must set a new password", async ({ page, browser }) => {
  const email = `editor-${uniq()}@example.com`;
  await loginUI(page, adminEmail, adminPassword);
  await page.goto("/settings/users");
  await page.getByRole("button", { name: "Create user" }).click();
  const dialog = page.getByRole("dialog", { name: "Create user" });
  await dialog.getByLabel("Email").fill(email);
  await dialog.getByLabel("Name").fill("Eve Editor");
  await dialog.getByLabel("Role").selectOption("editor");
  await dialog.getByLabel("Temporary password").fill("temporary-pass-1");
  await dialog.getByRole("button", { name: /create/i }).click();
  await expect(page.getByRole("cell", { name: email })).toBeVisible();

  const ctx = await browser.newContext({ baseURL: baseURL() });
  const editor = await ctx.newPage();
  await editor.goto("/login");
  await editor.getByLabel("Email").fill(email);
  await editor.getByLabel("Password", { exact: true }).fill("temporary-pass-1");
  await editor.getByRole("button", { name: "Sign in" }).click();
  await expect(editor.getByRole("heading", { name: "Set a new password" })).toBeVisible();
  await editor.getByLabel("Current password").fill("temporary-pass-1");
  await editor.getByLabel("New password", { exact: true }).fill("final-password-1");
  await editor.getByLabel("Confirm new password").fill("final-password-1");
  await editor.getByRole("button", { name: "Set password" }).click();
  await expect(editor.getByRole("heading", { name: "Dashboard" })).toBeVisible();

  const me = await ctx.request.get("/api/v1/auth/me");
  expect(((await me.json()) as { role: string; must_change_password: boolean })).toMatchObject({ role: "editor", must_change_password: false });
  await ctx.close();
});
