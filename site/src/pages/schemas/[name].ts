import type { APIRoute, GetStaticPaths } from "astro";
import { readdir, readFile } from "node:fs/promises";
import path from "node:path";

// The JSON Schemas of schemas/ at the URLs of their $id (https://sluice-docs.pages.dev/schemas/...).
const dir = path.resolve(process.cwd(), "../schemas");

export const getStaticPaths: GetStaticPaths = async () =>
  (await readdir(dir)).filter((f) => f.endsWith(".json")).map((name) => ({ params: { name } }));

export const GET: APIRoute = async ({ params }) =>
  new Response(await readFile(path.join(dir, params.name!), "utf8"), {
    headers: { "Content-Type": "application/schema+json" },
  });
