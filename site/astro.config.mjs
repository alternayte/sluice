// @ts-check
import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";
import starlightLlmsTxt from "starlight-llms-txt";
import starlightOpenAPI, { createOpenAPISidebarGroup } from "starlight-openapi";
import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";

// Cloudflare Web Analytics is cookie-free; it is on only when the deploy passes a beacon token.
const beacon = process.env.PUBLIC_CF_BEACON_TOKEN;
const api = createOpenAPISidebarGroup();
const site = process.env.DOCS_SITE ?? "https://sluice-docs.pages.dev";

// llms.txt lists every page with the URL of its Markdown copy, section by section.
function pageIndex() {
  const root = "src/content/docs";
  const sections = [
    ["tutorials", "Tutorials"],
    ["how-to", "How-to guides"],
    ["concepts", "Concepts"],
    ["reference", "Reference"],
    ["operations", "Operations"],
  ];
  const field = (src, key) => src.match(new RegExp(`^${key}:\\s*(.+)$`, "m"))?.[1].trim().replace(/^["']|["']$/g, "") ?? "";
  const out = ["## Pages", "", "Each page is also Markdown at its URL plus `.md`."];
  for (const [dir, label] of sections) {
    const files = readdirSync(path.join(root, dir)).filter((f) => /\.mdx?$/.test(f)).sort();
    out.push("", `### ${label}`, "");
    for (const f of files) {
      const src = readFileSync(path.join(root, dir, f), "utf8");
      const slug = `${dir}/${f.replace(/\.mdx?$/, "")}`;
      out.push(`- [${field(src, "title")}](${site}/${slug}.md): ${field(src, "description")}`);
    }
  }
  out.push("", `The HTTP API reference is at ${site}/reference/api/ and the OpenAPI document at ${site}/openapi.yaml.`);
  return out.join("\n");
}

export default defineConfig({
  site,
  integrations: [
    starlight({
      title: "Sluice",
      description:
        "Sluice runs flows of scripts, commands, HTTP calls and subflows on process, docker or kubernetes executors. One Go binary with Postgres.",
      logo: { src: "./src/assets/logo.svg", replacesTitle: false },
      favicon: "/favicon.svg",
      social: [{ icon: "github", label: "GitHub", href: "https://github.com/alternayte/sluice" }],
      editLink: { baseUrl: "https://github.com/alternayte/sluice/edit/main/site/" },
      lastUpdated: false,
      customCss: [
        "@fontsource-variable/inter/opsz.css",
        "@fontsource-variable/jetbrains-mono",
        "./src/styles/theme.css",
      ],
      components: {
        PageTitle: "./src/components/PageTitle.astro",
        Hero: "./src/components/Hero.astro",
      },
      head: beacon
        ? [
            {
              tag: "script",
              attrs: {
                defer: true,
                src: "https://static.cloudflareinsights.com/beacon.min.js",
                "data-cf-beacon": JSON.stringify({ token: beacon }),
              },
            },
          ]
        : [],
      sidebar: [
        { label: "Tutorials", items: [{ autogenerate: { directory: "tutorials" } }] },
        { label: "How-to guides", items: [{ autogenerate: { directory: "how-to" } }] },
        { label: "Concepts", items: [{ autogenerate: { directory: "concepts" } }] },
        { label: "Reference", items: [{ autogenerate: { directory: "reference" } }, api] },
        { label: "Operations", items: [{ autogenerate: { directory: "operations" } }] },
      ],
      plugins: [
        starlightOpenAPI([
          {
            base: "reference/api",
            schema: "../api/openapi.yaml",
            sidebar: { label: "HTTP API", group: api, operations: { badges: true } },
          },
        ]),
        starlightLlmsTxt({
          projectName: "Sluice",
          details: pageIndex(),
          description:
            "Sluice is a self-hosted flow orchestrator: one Go binary with Postgres, a web UI, a CLI and an MCP server. Flows are YAML files in namespaces.",
        }),
      ],
    }),
  ],
});
