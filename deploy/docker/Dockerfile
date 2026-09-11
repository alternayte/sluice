# syntax=docker/dockerfile:1
FROM oven/bun:1 AS ui
WORKDIR /src/ui
COPY ui/package.json ui/bun.lock ./
RUN bun install --frozen-lockfile
COPY ui/ ./
RUN bun run build

FROM golang:1.26 AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/ui/dist ./ui/dist
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/alternayte/sluice/internal/app.Version=${VERSION} -X github.com/alternayte/sluice/internal/app.Commit=${COMMIT}" \
    -o /out/sluice ./cmd/sluice

FROM gcr.io/distroless/static-debian12:nonroot AS sluice
COPY --from=go /out/sluice /usr/local/bin/sluice
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/sluice"]
CMD ["server"]
