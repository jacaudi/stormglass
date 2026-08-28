# Development

## The CI contract

`task ci` is the whole *static* gate: the test workflow invokes `task ci` and
nothing else. Every check it runs must be runnable locally with that one
command — a check that cannot be is not allowed to live only in a workflow.

```bash
task ci          # lint, format, tidy, vuln, tests, node, python
task lint        # static checks only
task test        # tests only
task --list      # every available target
```

`task ci` needs `golangci-lint`, `actionlint`, `hadolint` and `govulncheck` on
`PATH`. It fails loudly naming the missing tool rather than skipping the check.

## Image-shaped stages

Container builds are the deliberate exception — a build is not something to put
in front of every local run. Each CI stage still has a named local equivalent:

| CI stage | Local equivalent |
|---|---|
| application image | `task build-local` |
| boot the built image | `IMAGE=… task smoke` |
| radar sidecar image | `task radar-build` |
| boot the radar image | `IMAGE=… task radar-smoke` |

`task smoke` boots the image the way the README's quickstart does — a named
volume at `/data`, SQLite at its default path — and fails if the container
cannot serve `/healthz` and the embedded UI.

## Building

```bash
task build-local   # build the container image
docker buildx bake image-local
```

**The UI must be built before the Go binary.** `web/embed.go` carries
`//go:embed all:dist`, so building Go first embeds whatever `web/dist` currently
holds — an empty `web/dist` still compiles, which is the one failure `go build`
cannot catch. `task build` sequences them correctly.

Go 1.25+; the pinned toolchain is go1.26.6.

## Tests

```bash
go test ./...
go test -json ./...    # CI format
go mod tidy
```

Test files sit alongside their implementation. Postgres tests that need a live
database skip unless `POSTGRES_URL` is set:

```bash
docker run --rm -d --name pg-test -e POSTGRES_PASSWORD=x -e POSTGRES_DB=weather \
  -p 55432:5432 postgres:16
POSTGRES_URL='postgres://postgres:x@localhost:55432/weather?sslmode=disable' \
  go test ./internal/postgres/ -count=1
docker rm -f pg-test
```

## Running locally

The station broadcasts to UDP :50222 as link-local traffic, which Docker's
default bridge network does not deliver to a container. Host networking is
required to receive anything:

```bash
docker run -it --rm --net=host -v stormglass-data:/data stormglass:latest
```

See [Configuration](configuration.md) for the full variable surface.
