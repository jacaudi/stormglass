# syntax=docker/dockerfile:1

# --- UI stage: build the React app with Vite -------------------------------
FROM node:22-alpine@sha256:c610fcdfb1d5b4740dd70c284ed3cb16bb857e0f7166196e36a5501df7a3aa32 AS ui

WORKDIR /app/web

# Copy manifest + lockfile first for better layer caching.
COPY web/package.json web/package-lock.json ./
RUN npm ci --ignore-scripts

# Copy the rest of the UI source and build.
COPY web/ ./
RUN npm run build

# --- Builder stage: compile the Go binary, embedding the built UI ----------
FROM golang:1.26-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS builder

ARG VERSION=dev

WORKDIR /app

# Copy go mod files first for better layer caching.
COPY go.mod go.sum ./
RUN go mod download

# Copy source code.
COPY . .

# Overlay the freshly-built UI so web/embed.go's `//go:embed all:dist`
# embeds real assets instead of the repo's tracked web/dist/.gitkeep
# placeholder (see .dockerignore, which excludes any local web/dist so this
# copy is always the authoritative one).
COPY --from=ui /app/web/dist ./web/dist

RUN CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /tempestwx-utilities .

# --- Final stage: non-root static image -------------------------------------
FROM cgr.dev/chainguard/static:latest@sha256:60582b2ae6074f641094af0f370d4ab241aab271858a66223dcde7eee9f51638

COPY --from=builder /tempestwx-utilities /tempestwx-utilities

USER 65532:65532

EXPOSE 8080

# The final image has no shell/curl/wget, so the binary probes itself via
# its `healthcheck` subcommand (see runHealthcheck in main.go), which GETs
# its own /healthz and exits 0/1 accordingly.
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/tempestwx-utilities", "healthcheck"]

ENTRYPOINT ["/tempestwx-utilities"]
