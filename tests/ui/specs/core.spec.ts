import { expect, test } from "@playwright/test";
import {
  adminAPI,
  adminEmail,
  adminPassword,
  executionHeading,
  loginUI,
  runFileAPI,
  seedNamespace,
  waitExecutionState,
} from "../helpers/app";

test("SCN-CORE-008 a deep link loads, cache headers match and API responses carry a request ID", async ({ page }) => {
  const api = await adminAPI();
  const ns = await seedNamespace(api, "core008", { "hello.sh": "echo hello\n" });
  const exec = await runFileAPI(api, ns, "hello.sh");
  await waitExecutionState(api, exec.id, ["SUCCESS"]);
  await loginUI(page, adminEmail, adminPassword);

  const detail = page.waitForResponse((r) => r.url().endsWith(`/api/v1/executions/${exec.id}`));
  const nav = await page.goto(`/executions/${exec.id}`);
  expect(nav?.status()).toBe(200);
  expect(nav?.headers()["cache-control"]).toBe("no-cache");
  await expect(executionHeading(page, ns)).toBeVisible();
  expect((await detail).headers()["x-request-id"]).toBeTruthy();

  const html = await (await page.request.get(`/executions/${exec.id}`)).text();
  const asset = html.match(/\/assets\/[^"']+\.js/)?.[0];
  expect(asset, "index.html references a hashed script").toBeTruthy();
  const res = await page.request.get(asset!);
  expect(res.status()).toBe(200);
  expect(res.headers()["cache-control"]).toBe("public, max-age=31536000, immutable");

  const missing = await page.request.get("/api/v1/no-such-route");
  expect(missing.status()).toBe(404);
  expect(missing.headers()["x-request-id"]).toBeTruthy();
});
