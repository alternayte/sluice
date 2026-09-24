# Docs site

## What it does
The Sluice docs move to an Astro Starlight site in `site/`, hosted on Cloudflare Pages at `sluice-docs.pages.dev`. The site has a splash landing page and Diátaxis sections: Tutorials, How-to guides, Concepts, Reference and Operations. It uses the visual system of `ui-polish.md`. Agents can read it through llms.txt, a Markdown copy of each page and a "Copy page" menu. CI checks the samples, the generated pages and the prose, and deploys a preview for each pull request.

## Decisions
- This spec comes after `ui-polish.md` and `agent-readiness.md` — its screenshots and agent pages describe the result of both.
- Stack: Astro Starlight with `starlight-llms-txt` and `starlight-openapi`, as in speccy and deedbox — one pattern across the three projects.
- Host: the Cloudflare Pages project `sluice-docs`. `DOCS_SITE` sets the site URL — a custom domain is a one-line change.
- This spec replaces the paths of REQ-DOC-001 and REQ-DOC-002: the generated env and flow pages move from `docs/reference/` to `site/src/content/docs/reference/` — the site is their home.
- The site is the only home of the user docs. `docs/` keeps only `sluice-sdd.md`, `specs/`, `build/` and `superpowers/` — one source, so no doc drifts from another.
- The guides get a rewrite into the Diátaxis sections, not a file-by-file copy — each page answers one kind of question.
- README shrinks to the pitch, the quickstart and a link to the site.
- `buildtool gen` writes the generated reference pages into the site: flow (from the schema), env, CLI (from the command table), MCP tools (from the registry) and exit codes. `gen-check` checks them — generated pages never go stale.
- `starlight-openapi` renders the HTTP API from `api/openapi.yaml`.
- The site serves `schemas/flow.schema.json` and `schemas/validate-result.schema.json` at `/schemas/`, and `api/openapi.yaml` at `/openapi.yaml` — these are the stable URLs of the `$id`s.
- The theme uses the tokens of `ui-polish.md`: Inter Variable, JetBrains Mono, Apple system blue and the same greys — the docs and the app look like one product.
- The landing page uses the Starlight splash template with a hero, the quickstart, a product shot and a card grid.
- The site publishes llms.txt, llms-full.txt, llms-small.txt and a `.md` copy of every page. Each page has a "Copy page" menu: copy as Markdown, view as Markdown, open in Claude, open in ChatGPT — every competitor except Airflow publishes llms.txt.
- Two how-to guides cover the agents: "Use Sluice with coding agents" and "Run flows from GitHub Actions".
- `just docs-shots` starts Sluice and Postgres with the ELT demo data, drives the real UI with agent-browser, and writes light and dark screenshots and the quickstart GIF to `site/src/assets/shots/` — a restyle gets fresh shots with one command.
- `just docs-ref-check` fails when a generated page is stale, a flow YAML sample fails `sluice validate`, a sample runs a `sluice` command that does not exist, the env page misses a `SLUICE_` variable, or a handwritten page breaks the Sluice Vale style. The Vale style is a copy of the speccy style — CI finds drift before a reader does.
- `just docs`, `just docs-dev` and `just docs-deploy` build, serve and deploy the site. `just check` includes `docs-ref-check`.
- `.github/workflows/docs.yml` is a copy of the speccy workflow. Main deploys to production. A pull request deploys to its own alias and gets a comment with the preview URL. Cloudflare Web Analytics is on only when the beacon secret is set.
- Setup creates the `sluice-docs` Pages project with the logged-in wrangler. The user adds the `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID` repo secrets — only the user can create an API token.

## Out
- A custom domain.
- Versioned docs.
- A docs search service other than the built-in Starlight search.
- A docs MCP server.

## How I know it works
- `https://sluice-docs.pages.dev` shows the landing page in light and dark themes.
- `/llms.txt` lists every page. `/reference/flow.md` returns Markdown.
- "Copy page" puts the page Markdown on the clipboard.
- `curl https://sluice-docs.pages.dev/schemas/flow.schema.json | jq '."$id"'` matches the URL.
- The HTTP API reference lists every operation of `api/openapi.yaml`.
- A pull request gets a comment with a preview URL that loads.
- A broken flow sample in a page fails `just docs-ref-check`.
- `just docs-shots` regenerates every screenshot, and `git diff --stat` shows only image files.
- `docs/` has no user guide left, and every README link resolves.
