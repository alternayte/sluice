import type { APIRoute } from "astro";
import { readFile } from "node:fs/promises";
import path from "node:path";

// The OpenAPI document of the HTTP API, as `sluice openapi` prints it.
export const GET: APIRoute = async () =>
  new Response(await readFile(path.resolve(process.cwd(), "../api/openapi.yaml"), "utf8"), {
    headers: { "Content-Type": "application/yaml" },
  });
