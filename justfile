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
    test -z "$(git status --porcelain --untracked-files=all -- api/openapi.yaml internal/*/*db ui/src/api schemas docs/reference)"

lint: forbid
    golangci-lint run ./...
    if [ -f ui/package.json ]; then cd ui && bun run lint && bunx tsc --noEmit; fi
    if [ -d deploy/helm/sluice ]; then helm lint deploy/helm/sluice; fi

forbid:
    go run ./tools/buildtool forbid

test:
    mkdir -p {{junit}}
    {{gotestsum}} --junitfile {{junit}}/unit.xml -- -race -count=1 ./...
    if [ -f ui/package.json ]; then cd ui && bun run test --reporter=default --reporter=junit --outputFile.junit=../{{junit}}/vitest.xml; fi

test-int:
    mkdir -p {{junit}}
    {{gotestsum}} --junitfile {{junit}}/integration.xml -- -tags integration -count=1 -timeout 30m ./...

# Build the UI, check its size, build the binary and both images.
build: build-ui build-go build-images

build-ui:
    if [ -f ui/package.json ]; then cd ui && bun install --frozen-lockfile && bun run build; fi
    touch ui/dist/.keep
    if [ -f ui/package.json ]; then go run ./tools/buildtool size-check; fi

build-go:
    CGO_ENABLED=0 go build -trimpath -ldflags '{{ldflags}}' -o bin/sluice ./cmd/sluice

build-images:
    if [ -f deploy/docker/Dockerfile ]; then docker build -f deploy/docker/Dockerfile --target sluice --build-arg VERSION={{version}} --build-arg COMMIT={{commit}} -t sluice:dev . ; fi

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

ledger-check:
    go run ./tools/buildtool ledger-check

# Fast loop.
check: gen-check lint test

verify:
    go run ./tools/buildtool verify

evidence:
    go run ./tools/buildtool evidence

evidence-check:
    go run ./tools/buildtool evidence-check
