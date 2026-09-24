set shell := ["bash", "-euo", "pipefail", "-c"]
set dotenv-load

version := `git describe --tags --always --dirty 2>/dev/null || echo dev`
commit := `git rev-parse HEAD 2>/dev/null || echo unknown`
build_date := `date -u +%Y-%m-%dT%H:%M:%SZ`
pkg := "github.com/alternayte/sluice/internal/app"
ldflags := "-s -w -X " + pkg + ".Version=" + version + " -X " + pkg + ".Commit=" + commit + " -X " + pkg + ".BuildDate=" + build_date
junit := "build/reports/junit"
gotestsum := "go tool gotestsum --format pkgname-and-test-fails"

default:
    @just --list

# Check the tools, create .env and install the UI packages.
setup:
    go run ./tools/buildtool setup
    test -f .env || cp .env.example .env
    cd ui && bun install --frozen-lockfile
    cd tests/ui && bun install --frozen-lockfile

# Start Postgres for development.
up:
    docker compose -f deploy/compose/dev.yml up -d --wait

# Stop the development services.
down:
    docker compose -f deploy/compose/dev.yml down

# Run the Go server with live reload and the Vite dev server.
dev:
    #!/usr/bin/env bash
    set -euo pipefail
    trap 'kill 0' EXIT
    go tool air &
    (cd ui && bun run dev) &
    wait

# Apply the database migrations.
migrate:
    go run ./cmd/sluice migrate

# Drop the development database, create it again and apply the migrations.
db-reset:
    docker compose -f deploy/compose/dev.yml exec -T postgres psql -U sluice -d postgres -c 'DROP DATABASE IF EXISTS sluice WITH (FORCE)' -c 'CREATE DATABASE sluice'
    go run ./cmd/sluice migrate

# Compare tests/e2e with the refactor baseline.
e2e-compare:
    ./scripts/e2e-compare.sh

# Generate code, schemas and reference docs.
gen:
    go tool sqlc generate
    go run ./cmd/sluice openapi > api/openapi.yaml
    if [ -f ui/package.json ]; then cd ui && bun install --frozen-lockfile >/dev/null && bun run gen:api; fi
    go run ./tools/buildtool gen

# Fail when generated files differ from the committed files.
gen-check: gen
    git diff --exit-code
    test -z "$(git status --porcelain --untracked-files=all -- api/openapi.yaml internal/*/*db ui/src/api schemas site/src/content/docs/reference)"

lint: forbid
    golangci-lint run ./...
    if [ -f ui/package.json ]; then cd ui && bun run format:check && bun run lint && bunx tsc --noEmit; fi
    if [ -d deploy/helm/sluice ]; then helm lint deploy/helm/sluice; fi

forbid:
    go run ./tools/buildtool forbid

# Go tests use the integration tag and testcontainers, so Docker must run.
test:
    mkdir -p {{junit}}
    {{gotestsum}} --junitfile {{junit}}/go.xml -- -race -count=1 -tags integration ./...
    if [ -f ui/package.json ]; then cd ui && bun run test --reporter=default --reporter=junit --outputFile.junit=../{{junit}}/vitest.xml; fi

# Build the UI, check its size, build the binary and both images.
build: build-ui build-go build-images

build-ui:
    if [ -f ui/package.json ]; then cd ui && bun install --frozen-lockfile && bun run build; fi
    touch ui/dist/.keep
    if [ -f ui/package.json ]; then go run ./tools/buildtool size-check; fi

build-go:
    CGO_ENABLED=0 go build -trimpath -ldflags '{{ldflags}}' -o bin/sluice ./cmd/sluice

build-images:
    docker build -f deploy/docker/Dockerfile --target sluice --build-arg VERSION={{version}} --build-arg COMMIT={{commit}} --build-arg BUILD_DATE={{build_date}} -t sluice:dev .
    docker build -f deploy/docker/Dockerfile --target sluice-uv --build-arg VERSION={{version}} --build-arg COMMIT={{commit}} --build-arg BUILD_DATE={{build_date}} -t sluice-uv:dev .

e2e:
    mkdir -p {{junit}}
    SLUICE_E2E_BINARY=$PWD/bin/sluice {{gotestsum}} --junitfile {{junit}}/e2e.xml -- -tags e2e -count=1 -timeout 60m ./tests/e2e/...
    if [ -f tests/ui/package.json ]; then cd tests/ui && bun install --frozen-lockfile >/dev/null && bunx playwright install chromium >/dev/null && SLUICE_E2E_BINARY=$PWD/../../bin/sluice bunx playwright test; fi

e2e-k8s:
    mkdir -p {{junit}}
    if [ -d tests/k8s ]; then ./tests/k8s/run.sh; fi

perf:
    mkdir -p {{junit}}
    if [ -d tests/perf ]; then SLUICE_E2E_BINARY=$PWD/bin/sluice {{gotestsum}} --junitfile {{junit}}/perf.xml -- -tags perf -count=1 -timeout 60m ./tests/perf/...; fi

trace:
    go run ./tools/buildtool trace

# Fail when a generated docs page is stale, a YAML sample is invalid, a sample runs a sluice
# command that does not exist, the env page misses a SLUICE_ variable, or a handwritten page
# breaks the Sluice Vale style.
docs-ref-check:
    go run ./tools/buildtool docs-ref-check
    go tool vale --config site/.vale.ini --glob='!**/reference/{cli,env,exit-codes,flow,github-action,mcp-tools}.md' site/src/content/docs

# Build the docs site into site/dist.
docs:
    cd site && bun install --frozen-lockfile && bun run build

# Run the docs site with live reload on http://localhost:4321.
docs-dev:
    cd site && bun install --frozen-lockfile && bun run dev

# Deploy site/dist to the Cloudflare Pages project sluice-docs with the logged-in wrangler.
# main is the production branch: https://sluice-docs.pages.dev.
docs-deploy: docs
    npx --yes wrangler@4 pages deploy site/dist --project-name=sluice-docs --branch=main

# Capture the screenshots and the quickstart GIF of the docs site from the real UI into
# site/src/assets/shots. Needs Docker, agent-browser and ffmpeg.
docs-shots: build-ui build-go
    go run ./tools/buildtool docs-shots

# Fast loop.
check: gen-check lint test docs-ref-check
