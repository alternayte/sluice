# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM oven/bun:1 AS ui
WORKDIR /src/ui
COPY ui/package.json ui/bun.lock ./
RUN bun install --frozen-lockfile
COPY ui/ ./
RUN bun run build

FROM --platform=$BUILDPLATFORM golang:1.26 AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/ui/dist ./ui/dist
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags "-s -w -X github.com/alternayte/sluice/internal/app.Version=${VERSION} -X github.com/alternayte/sluice/internal/app.Commit=${COMMIT} -X github.com/alternayte/sluice/internal/app.BuildDate=${BUILD_DATE}" \
    -o /out/sluice ./cmd/sluice

FROM ghcr.io/astral-sh/uv:0.11.7 AS uv

FROM oven/bun:1.4.2 AS bun

# sluice: distroless and non-root (REQ-DEP-001).
FROM gcr.io/distroless/static-debian12:nonroot AS sluice
COPY --from=go /out/sluice /usr/local/bin/sluice
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/sluice"]
CMD ["server"]

# sluice-uv: Debian slim with bash, uv, a uv-managed Python and bun, non-root (REQ-DEP-001).
FROM debian:bookworm-slim AS sluice-uv
RUN apt-get update \
    && apt-get install -y --no-install-recommends bash ca-certificates git \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 65532 sluice \
    && useradd --uid 65532 --gid 65532 --home-dir /home/sluice --create-home --shell /bin/bash sluice
COPY --from=uv /uv /uvx /usr/local/bin/
COPY --from=bun /usr/local/bin/bun /usr/local/bin/bun
# Python lives outside the home directory, so that it also works with a read-only root
# file system. Caches go to /tmp, which is an emptyDir in Kubernetes.
ENV UV_PYTHON_INSTALL_DIR=/opt/uv/python \
    UV_PYTHON_PREFERENCE=only-managed \
    UV_CACHE_DIR=/tmp/uv-cache \
    UV_LINK_MODE=copy \
    BUN_INSTALL_CACHE_DIR=/tmp/bun-cache
# The install uses its own cache, so that no root-owned /tmp/uv-cache stays in the image.
RUN UV_CACHE_DIR=/tmp/uv-build-cache uv python install 3.12 \
    && rm -rf /tmp/uv-build-cache \
    && chmod -R a+rX /opt/uv
COPY --from=go /out/sluice /usr/local/bin/sluice
USER 65532:65532
WORKDIR /home/sluice
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/sluice"]
CMD ["server"]
