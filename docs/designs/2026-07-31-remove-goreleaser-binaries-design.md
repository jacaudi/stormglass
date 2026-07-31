# Design — Remove GoReleaser; the container image is the release artifact

**Date:** 2026-07-31
**Status:** Approved (design phase)
**Branch:** `worktree-release-binaries`

## Problem

Since v3.0.0, the `release-binaries` job in `.github/workflows/on-release.yml` has failed on
every tagged release:

```
422 Cannot upload assets to an immutable release
```

GoReleaser builds all eight platform archives successfully; only the upload fails. Releases
v3.0.0 and v3.1.0 therefore carry **0 assets**, where v1.0.0, v1.1.0 and v2.0.0 each carried 9.

## Root cause (verified)

Three facts, each verified against a primary source rather than inferred from the error string:

1. **GoReleaser is already immutable-safe when it owns the release.** It creates the release as
   a draft, uploads artifacts, then un-drafts it — which is exactly the flow GitHub documents
   for immutable releases. From `internal/client/github.go:538`:

   ```go
   // Always start with a draft release while uploading artifacts.
   // PublishRelease will undraft it.
   Draft: new(true),
   ```

   and `internal/pipe/release/release.go` runs `CreateRelease` (:144) → `Upload` (:190) →
   `PublishRelease` (:200).

2. **It never reaches that path here.** `findRelease` (`github.go:653`) calls
   `GetReleaseByTag` first. release-please had already created *and published* the release for
   the tag — release ID 362923849, `immutable: true`, published 08:00:59Z by `wall-e-one[bot]`,
   before `on-release.yml` even started at 08:02. GoReleaser found it and switched to
   update-and-upload, which immutability forbids.

3. **A draft release does not materialise the git tag until it is published.** So the obvious
   alternative — "have release-please create a draft and let GoReleaser publish it" — would
   silently break the App-token → tag → `on-release.yml` trigger chain that
   `release-please.yml` documents as load-bearing.

The regression is pinned to the release-please migration in v3.0.0. Before it, GoReleaser
created the release *and* uploaded in one operation, so immutability was never contended.

## Decision

**Delete GoReleaser. The container image is the release artifact.**

The pipeline stops producing binary release assets entirely. Users obtain the binary from the
published image, which is already tagged per release
(`ghcr.io/jacaudi/tempestwx-utilities:v3.1.0`, pushed 08:15:15Z in the same run whose binaries
job failed).

### Why this over restructuring the pipeline

The alternative — making release-please stop publishing the release so GoReleaser can own it —
is achievable, but it requires `skip-github-release: true` plus a new explicit tag-push step
(release-please's own schema states the option "should only be used if you have existing
infrastructure to tag these releases"), plus a changelog-extraction step to keep the release
notes. That is three new moving parts to keep publishing artifacts that, as established below,
were defective.

**The binaries GoReleaser produced were wrong in two independent ways.** Both verified:

- **Empty embedded UI.** `web/embed.go` does `//go:embed all:dist`, and the only tracked file
  under `web/dist/` is `.gitkeep`. The Dockerfile has a dedicated `ui` stage
  (`npm ci` → `npm run build`) that overlays real Vite output into the builder before
  `go build`. `.goreleaser.yaml`'s only hook is `go mod tidy`. Every released binary therefore
  embedded an empty `dist/` and served nothing at `/`.
- **Wrong version string.** `main.go:44` is `var version = "dev"`, injected via
  `-ldflags -X main.version=${VERSION}`. The Dockerfile passes it; `.goreleaser.yaml` passes
  only `-s -w`. Every released binary reported its version as `dev`. It also omitted
  `-trimpath`, which the image build uses.

So v1.0.0–v2.0.0 shipped 9 assets that were quietly incorrect. Removing GoReleaser eliminates a
source of bad artifacts, not merely a failing job.

**The documented install story is unaffected.** `README.md`'s only install instruction is
`ghcr.io/jacaudi/tempestwx-utilities`. It has never advertised a binary download.

### Accepted trade-off

`docker-bake.hcl`'s `image-automated` target builds `linux/amd64` and `linux/arm64` only.
Extracting the binary from the image therefore covers 2 of the 8 platform archives v2.0.0
carried. `darwin/amd64`, `darwin/arm64`, `windows/386`, `windows/amd64`, `windows/arm64` and
`linux/386` lose their download.

This is accepted deliberately: those downloads were broken in the two ways above, and the README
never pointed at them. Restoring cross-platform binaries later is an additive change — a new
workflow job — not a rewrite of anything this design touches.

## Scope

### Removed

| Path | Change |
|---|---|
| `.goreleaser.yaml` | delete |
| `.github/actions/go-release/` | delete the whole composite action |
| `.github/workflows/on-release.yml` | delete the `release-binaries` job |

### Also removed — dead inputs in the docker action

`.github/actions/docker/action.yml` declares four inputs but its body reads only two
(`inputs.token` at :50, `inputs.push` at :62). `latest` and `tag-strategy` are never
referenced; the version step branches on `github.ref_type`/`github.ref_name` instead.

| caller | passes | what actually happens |
|---|---|---|
| `on-release.yml` | `latest: false`, `tag-strategy: "semver"` | tags `:v3.1.0` — because `ref_type == tag` |
| `on-push-main.yml` | `latest: true`, `tag-strategy: "latest"` | tags `:latest` — because `ref_name == main` |

Behaviour is correct today; `image-automated` tags `${REGISTRY_IMAGE}:${VERSION}` and nothing
else, so a tag build never applies `:latest` regardless of what `latest:` says. The inputs are
pure noise that make the workflows read as though they configure tagging. Delete the two input
declarations and the four caller lines. **No behaviour change.**

### Explicitly NOT changed

- **`release-please.yml`** — untouched. It keeps minting the App installation token, keeps
  creating both the tag and the immutable GitHub Release. The trigger chain is preserved
  because nothing about tag creation moves.
- **Immutable releases stays enabled.** It is no longer contended, because nothing attaches
  assets after publish.
- **`release-image`** — untouched, in both `on-release.yml` and `on-push-main.yml`.
- **The hardened CI gate from #112** (`gofmt` · `go mod tidy` check · `CGO_ENABLED=0 go build` ·
  `go vet` · `golangci-lint` · `go test -race`) — untouched. `on-release.yml` keeps
  `release-image: needs: tests`.
- **`README.md`** — unchanged. No "extract the binary from the image" snippet; the image *is*
  the documented install path.
- **`${{ github.ref_name }}` interpolated into a `run:` script** in the docker action, flagged
  as an injection antipattern in `docs/review/2026-07-17-code-review-summary.md:154`.
  Deliberately out of scope — it is a behaviour-adjacent edit to the one job that is currently
  healthy, and belongs in its own change.

## Resulting pipeline

```
push to main
  └─ release-please.yml  (App token)
       └─ maintains release PR → merge cuts tag v3.1.0 + immutable GitHub Release (notes = CHANGELOG.md)

tag push v*
  └─ on-release.yml
       └─ tests  ──►  release-image  →  ghcr.io/jacaudi/tempestwx-utilities:v3.1.0
```

Releases carry no assets. That is now the intended steady state, not a defect.

## Consequences

- **The v3.0.0 / v3.1.0 asset-backfill question is moot.** There are no assets to backfill in
  either direction, so immutability's prohibition on adding them to a published release never
  needs testing.
- **Renovate** will stop seeing `goreleaser/goreleaser-action` once the action file is deleted;
  no config change is needed (`renovate.json` extends shared presets and pins nothing locally).
- `CHANGELOG.md` and the 2026-07-17 design/plan/review documents retain historical references to
  GoReleaser. Those are records of what happened and are left alone.

## Verification

1. `actionlint` passes on the three edited/deleted workflow files.
2. No workflow or action still references `go-release`, `goreleaser`, or `release-binaries`
   (grep, excluding `CHANGELOG.md` and `docs/`).
3. No workflow still passes `latest:` or `tag-strategy:` to `./.github/actions/docker`, and the
   action declares only `token` and `push`.
4. The repo builds and tests green under the existing gate — the change is CI-only and touches
   no Go source, so `go test ./...` should be unaffected; run it to confirm that.
5. **Acceptance is the next tagged release:** `on-release.yml` completes with zero failing jobs,
   and `ghcr.io/jacaudi/tempestwx-utilities:vX.Y.Z` exists. The release will show 0 assets by
   design.

## Could not verify

- **Where immutability is enabled.** `GET /repos/jacaudi/tempestwx-utilities` returns
  `immutable_releases: null` and `GET .../rulesets` returns `[]`. GitHub's own announcement
  discussion states the repository-level API for this setting "is planned but not yet
  published," so the setting's location could not be read programmatically. The per-release
  evidence is unambiguous regardless: v1.0.0/v1.1.0/v2.0.0 are `immutable: false` with 9 assets
  each; v3.0.0/v3.1.0 are `immutable: true` with 0. This design does not depend on knowing where
  the toggle lives — it stops contending with immutability rather than configuring it.

## References

- Failing run: <https://github.com/jacaudi/tempestwx-utilities/actions/runs/30614807926>
- GoReleaser draft-then-publish: `internal/client/github.go:538`, `internal/pipe/release/release.go:144,190,200`
- GoReleaser existing-release lookup: `internal/client/github.go:653`
- GitHub immutable releases announcement: <https://github.com/orgs/community/discussions/171210>
- release-please `skip-github-release` semantics: <https://raw.githubusercontent.com/googleapis/release-please/main/schemas/config.json>
