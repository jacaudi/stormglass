# syntax=docker/dockerfile:1

# --- UI stage: build the React app with Vite -------------------------------
FROM node:24-alpine@sha256:e67514e5d0f6c46656005e1b693b2ec9d52e80b641307de684d4a015ba7a4eaf AS ui

WORKDIR /app/web

# Copy manifest + lockfile first for better layer caching.
COPY web/package.json web/package-lock.json ./
RUN npm ci --ignore-scripts

# Copy the rest of the UI source and build.
COPY web/ ./
RUN npm run build

# --- Builder stage: compile the Go binary, embedding the built UI ----------
FROM golang:1.27-alpine@sha256:cf6fca6641884b8433441b2b0652976f975e1d0fdd26d177eaaf8596087f3125 AS builder

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
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /stormglass ./cmd/stormglass

# An empty /data to carry into the final stage. Docker seeds a freshly created
# named volume from the image's directory -- ownership included -- so shipping
# /data owned by 65532 is what makes `docker run -v vol:/data` work for the
# unprivileged user below. Without it the volume mounts root-owned, SQLite
# cannot open its database, and the process exits at startup (#226).
RUN mkdir -p /seed/data

# --- Final stage: non-root static image -------------------------------------
FROM cgr.dev/chainguard/static:latest@sha256:96d02f455d5a73b817c0602910748609cf8471b1cc9522f78c75cedb1f67d072

COPY --from=builder /stormglass /stormglass
COPY --from=builder --chown=65532:65532 /seed/data /data

USER 65532:65532

EXPOSE 8080

# The final image has no shell/curl/wget, so the binary probes itself via
# its `healthcheck` subcommand (see runHealthcheck in main.go), which GETs
# its own /healthz and exits 0/1 accordingly.
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/stormglass", "healthcheck"]

ENTRYPOINT ["/stormglass"]
