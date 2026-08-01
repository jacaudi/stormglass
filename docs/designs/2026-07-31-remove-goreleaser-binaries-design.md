# Design — Remove GoReleaser; the container image is the release artifact

**Date:** 2026-07-31
**Status:** Approved (design phase); revised after Gate 1 review
**Branch:** `worktree-release-binaries`

## Problem

The `release-binaries` job in `.github/workflows/on-release.yml` has failed on both tagged
releases since the release-please migration, for **two different reasons**:

| tag | run | failure |
|---|---|---|
| v3.0.0 | [30410541653](https://github.com/jacaudi/tempestwx-utilities/actions/runs/30410541653) | `hook failed: go.mod requires go >= 1.25.0 (running go 1.24.13; GOTOOLCHAIN=local)` — died in the `before` hook, built nothing |
| v3.1.0 | [30614807926](https://github.com/jacaudi/tempestwx-utilities/actions/runs/30614807926) | `422 Cannot upload assets to an immutable release` — built all eight archives, failed only on upload |

The v3.0.0 toolchain failure was fixed by `6cb7a00`/`8990b88` (see the comment now at
`.github/actions/go-release/action.yml:21-26`). Fixing it is what allowed v3.1.0 to reach the
upload and expose the 422. **The 422 has occurred exactly once.**

Releases v3.0.0 and v3.1.0 carry **0 assets**, where v1.0.0, v1.1.0 and v2.0.0 each carried 9.

## Root cause (verified)

Three facts about the 422. Two are verified against primary sources; the third is explicitly
not, and is labelled as such.

1. **GoReleaser is already immutable-safe when it owns the release.** It creates the release as
   a draft, uploads artifacts, then un-drafts it — which is exactly what GitHub's own
   documentation recommends for immutable releases. From `internal/client/github.go:538-540`
   at v2.17.1:

   ```go
   // Always start with a draft release while uploading artifacts.
   // PublishRelease will undraft it.
   Draft: new(true),
   ```

   Flow confirmed at `internal/pipe/release/release.go:144` (`CreateRelease`) → `:190`
   (`Upload`) → `:200` (`PublishRelease`).

2. **It never reaches that path here.** `createOrUpdateRelease` (`github.go:606`) calls
   `findRelease` first (`:608`), which calls `GetReleaseByTag` (`:652-655`). release-please had
   already created *and published* the release for the tag — ID **362923849**, `immutable: true`,
   published 08:00:59Z by `wall-e-one[bot]`. The `on-release.yml` run started at 08:01:00Z, one
   second later, and the `release-binaries` job at 08:02:11Z. GoReleaser found the existing
   release and took the update branch at `:647-649`, where `data.Draft = new(release.Draft)`
   inherits the release's *published* state — so its immutability-safe path is structurally
   unreachable once a published release exists for the tag. The run log's target
   (`releases/362923849/assets`) is that exact release ID.

3. **A draft release is believed not to materialise the git tag until it is published** — which
   would mean "have release-please create a draft and let GoReleaser publish it" silently
   breaks the App-token → tag → `on-release.yml` trigger chain that `release-please.yml`
   documents as load-bearing. **This claim is NOT verified** — see *Could not verify*. It bears
   only on an alternative this design rejects, so nothing here depends on it.

### The 422 requires a conjunction, not a single cause

Two conditions both changed between v2.0.0 (2025-12-18, `immutable: false`) and v3.0.0
(2026-07-29, `immutable: true`):

1. release-please creates **and publishes** the release before `on-release.yml` runs — introduced
   in `fe8cba7`, first tagged v3.0.0.
2. Immutable releases is enabled on the repository.

Remove either and GoReleaser works. Attributing the regression solely to the release-please
migration overstates the evidence; condition 2 may not have been a deliberate act at all (see
*Could not verify*).

## Decision

**Delete GoReleaser. The container image is the release artifact.**

The pipeline stops producing binary release assets. Users obtain the binary from the published
image, which is tagged per release (`ghcr.io/jacaudi/tempestwx-utilities:v3.1.0`, pushed
08:15:15Z in the same run whose binaries job failed).

### Why this over restructuring the pipeline

**1. Nobody used the binaries.** Download counts across all three releases that shipped them:

| tag | per-asset downloads |
|---|---|
| v1.0.0 | 3 on every one of the 8 archives |
| v1.1.0 | 1 on every one of the 8 archives |
| v2.0.0 | 1–3, mostly 1 |

Uniformity across `windows_386` and `darwin_arm64` alike is the signature of automated scanners,
not users — a real user population does not download every platform in equal measure.

**2. Nothing in the repository consumes them.** `git grep -liE "releases/download|\.tar\.gz|curl
.*releases"` over tracked non-doc files returns no hits. `deploy/docker-compose.yml` builds from
source. There is no install script, no Homebrew tap, no package manifest.

**3. The documented install story never mentioned them.** `README.md`'s only install instruction
is `ghcr.io/jacaudi/tempestwx-utilities`. It has never advertised a binary download. (But see
*Consequences* — that line has its own gap.)

**4. Restructuring costs three new moving parts.** Making release-please stop publishing so
GoReleaser can own the release requires `skip-github-release: true` (whose own schema warns it
"should only be used if you have existing infrastructure to tag these releases"), **plus** a new
explicit tag-push step to replace the tag release-please no longer creates, **plus** a
changelog-extraction step to keep the release notes. Three additions to keep publishing
artifacts that points 1–3 show nobody wants.

### The current GoReleaser config is also broken — but this is not a claim about shipped assets

Two defects exist in the **current tree**, and would have to be fixed before GoReleaser could be
trusted again. Both were introduced with `.goreleaser.yaml` in `e2f62ba`, first tagged v3.0.0:

- **Empty embedded UI.** `web/embed.go` does `//go:embed all:dist`; the only tracked file under
  `web/dist/` is `.gitkeep`. The Dockerfile has a dedicated `ui` stage (`npm ci` →
  `npm run build`) that overlays real Vite output before `go build`; `.goreleaser.yaml`'s only
  hook is `go mod tidy`. A GoReleaser build therefore embeds an empty `dist/` — verified by
  building with GoReleaser-equivalent flags and enumerating `web.DistFS()`: one non-directory
  entry, `.gitkeep`; `index.html` absent.
- **Version injection disabled.** `main.go:44` is `var version = "dev"`, set via
  `-X main.version`. GoReleaser's *defaults* would inject it — `internal/builders/golang/build.go:120-122`
  sets `-s -w -X main.version={{.Version}} ...` when `Ldflags` is empty. `.goreleaser.yaml`
  specifies `-s -w` only, which **overrides** that default and disables the injection. It also
  drops `-trimpath`, which the image build uses.

**These defects never reached a published asset.** At v2.0.0 there is no `web/` directory, no
`//go:embed` anywhere, no `version` variable, and no `.goreleaser.yaml` at all — GoReleaser ran
config-less with its defaults. The v1.x/v2.0.0 assets were not defective. The only binaries
carrying these defects are the ones built during the v3.0.0/v3.1.0 runs, which were never
uploaded. **Zero defective assets were ever published.**

Noted because it is instructive: `.goreleaser.yaml` was added to close
`docs/review/2026-07-17-code-review-summary.md:154` ("GoReleaser runs with no `.goreleaser.yml`
in the repo"). The fix for that finding is what introduced the version regression.

### Accepted trade-off

`docker-bake.hcl:56-59` builds `linux/amd64` and `linux/arm64` only. Extraction from the image
therefore covers 2 of the 8 platform archives v2.0.0 carried; `darwin/amd64`, `darwin/arm64`,
`windows/386`, `windows/amd64`, `windows/arm64` and `linux/386` lose their download.

Accepted deliberately, on the download evidence above. Restoring cross-platform binaries later is
an additive change — a new workflow job — not a rewrite of anything this design touches.

Extraction mechanics, since "the binaries can be extracted" is load-bearing in this rationale:
the final image is `cgr.dev/chainguard/static` with no shell, so it needs
`docker create` + `docker cp` rather than `docker run`; the binary is at `/tempestwx-utilities`
(`Dockerfile:42`) and is static (`CGO_ENABLED=0`); on the multi-arch manifest, `--platform`
selects the arch.

## Scope — consolidated change list

Seven edits across six files. Three of them are workflow files
(`on-release.yml`, `on-push-main.yml`, `release-please.yml`).

| # | Path | Change |
|---|---|---|
| 1 | `.goreleaser.yaml` | **delete file** |
| 2 | `.github/actions/go-release/action.yml` | **delete file** (and the now-empty `go-release/` directory) |
| 3 | `.github/workflows/on-release.yml` | delete the `release-binaries` job (`:40-52`); delete `latest: false` (`:36`) and `tag-strategy: "semver"` (`:38`) from the `release-image` step |
| 4 | `.github/workflows/on-push-main.yml` | delete `latest: true` (`:44`) and `tag-strategy: "latest"` (`:46`) |
| 5 | `.github/actions/docker/action.yml` | delete the `latest` (`:13-16`) and `tag-strategy` (`:17-20`) input declarations |
| 6 | `.github/workflows/release-please.yml` | comment-only, **two** sites: `:30` ("no image or binaries would be published") and `:44-45` ("tests -> tagged image -> binaries") |

Line numbers are as of `a1b07f1`; the implementation plan must re-anchor them.

### Why the dead inputs go too

`.github/actions/docker/action.yml` declares four inputs but its body reads only two —
`inputs.token` (`:50`) and `inputs.push` (`:62`). `latest` and `tag-strategy` are never
referenced; the version step branches on `github.ref_type`/`github.ref_name` instead.

| caller | passes | what actually happens |
|---|---|---|
| `on-release.yml` | `latest: false`, `tag-strategy: "semver"` | tags `:v3.1.0` — because `ref_type == tag` |
| `on-push-main.yml` | `latest: true`, `tag-strategy: "latest"` | tags `:latest` — because `ref_name == main` |

Behaviour is correct today: `docker-bake.hcl:61` tags `${REGISTRY_IMAGE}:${VERSION}` and nothing
else, so a tag build never applies `:latest` regardless of what `latest:` says. The inputs are
noise that make the workflows read as though they configure tagging. **No behaviour change.**

### Explicitly NOT changed

- **release-please's behaviour** — it keeps minting the App installation token and keeps creating
  both the tag and the immutable GitHub Release. Only comments change (item 6). Note the Gate 1
  review flagged one stale site; writing the plan surfaced a second at `:30`, inside the
  App-token rationale, which after this change would claim binaries publish when only the image
  does.
- **Immutable releases stays enabled.** It is no longer contended, because nothing attaches
  assets after publish.
- **`release-image`'s behaviour** — unchanged in both workflows. Items 3 and 4 do delete two lines
  from its `with:` block, but those inputs were never read, so the job's inputs, steps, and the
  image tag it pushes are all identical before and after.
- **The hardened CI gate from #112** — untouched. `on-release.yml` keeps `release-image: needs: tests`.
- **`README.md`** — unchanged, by explicit decision.
- **`${{ github.ref_name }}` interpolated into a `run:` script** in the docker action, flagged in
  `docs/review/2026-07-17-code-review-summary.md:154`. Out of scope by explicit decision — it is a
  behaviour-adjacent edit to the one job that is currently healthy.
- **The Go-floor lesson** recorded in `.github/actions/go-release/action.yml:21-26` disappears with
  that file. Accepted: the same trap is already guarded in `.github/actions/tests/action.yml`,
  which pins the Go version for the surviving jobs.

## Resulting pipeline

```
push to main
  └─ release-please.yml  (App token)
       └─ maintains release PR → merge cuts tag v3.1.0 + immutable Release (notes = CHANGELOG.md)

tag push v*
  └─ on-release.yml
       └─ tests  ──►  release-image  →  ghcr.io/jacaudi/tempestwx-utilities:v3.1.0
```

Releases carry no assets. That is the intended steady state, not a defect.

## Consequences

- **The v3.0.0 / v3.1.0 asset-backfill question is moot.** There are no assets to backfill.
- **The README's install line does not resolve to a release.** `README.md:17` is
  `ghcr.io/jacaudi/tempestwx-utilities` — untagged, so Docker pulls `:latest`. But
  `docker-bake.hcl:61` writes one tag, and `VERSION` is `latest` only on main pushes, so a tagged
  release never writes `:latest`. Confirmed on ghcr: `v3.1.0` pushed 2026-07-31T08:15:15Z,
  `latest` pushed 07:28:39Z — a main build 47 minutes earlier. So "the image is the release
  artifact" holds for `:v3.1.0` but not for what the README tells people to pull. Recorded as a
  known gap; the README is unchanged by explicit decision. A follow-up could either tag releases
  as `:latest` too, or point the README at a version.
- **Renovate needs no change.** `.github/renovate.json` extends three shared
  `github>jacaudi/renovate-config:*` presets and pins nothing locally. Deleting the action removes
  the only `goreleaser/goreleaser-action` reference.
- `CHANGELOG.md` and the 2026-07-17 design/plan/review documents retain historical GoReleaser
  references. Those are records of what happened; left alone.

## Verification

**Pre-merge — the strongest available argument.** In the failing run `30614807926`, both surviving
jobs already passed: `tests` ✓ 08:02:03Z and `release-image` ✓ 08:15:27Z. This change deletes only
the third job and removes unreferenced inputs. Nothing that must keep working is being modified.

1. `actionlint` passes. **Its actual scope, verified empirically:** it *collects* workflow files
   only (`-verbose` → `Collected 4 YAML files`, all under `.github/workflows/`), so
   `.github/actions/docker/action.yml` is never independently linted against its rule set — **but
   it does resolve local composite actions and validate the caller→declaration contract.** Deleting
   the two declarations while leaving the callers passing them yields four
   `input "latest" is not defined in action "Docker Image"` errors and exit 1. So actionlint covers
   item 5 after all, contrary to this design's earlier claim. Note the `actionlint` job exists only
   in `on-pull-request.yml`, so this change must go through a PR for the check to run in CI; it can
   be run locally with `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12`.
2. `git grep -iE "goreleaser|go-release|release-binaries"` over tracked files returns hits only in
   `CHANGELOG.md` and `docs/` (historical records).
3. **Second, independent cover for item 5:** no workflow passes `latest:` or `tag-strategy:` to
   `./.github/actions/docker`, and the action declares exactly `token` and `push`. Both sides of
   the deletion must be grepped — deleting a declaration while a caller still passes it is the
   failure mode, and it is caught both here and by actionlint.
4. `go test ./...` green. The change is CI-only and touches no Go source, so this confirms no
   accidental collateral rather than testing the change.
5. **Post-merge acceptance:** the next tagged release completes with zero failing jobs and
   `ghcr.io/jacaudi/tempestwx-utilities:vX.Y.Z` exists. The release will show 0 assets by design.

Two things confirmed as non-blockers: `GET /repos/.../branches/main/protection` returns
**404 Branch not protected** and `GET /repos/.../rulesets` returns `[]`, so no required status
check can be left orphaned by deleting the job. After deletion no job is over-scoped —
`release-binaries` held the only `contents: write`; top-level is `contents: read` and
`release-image` is `contents: read` + `packages: write`.

## Could not verify

1. **That a draft release does not materialise the git tag until published** (fact 3 above). No
   authoritative GitHub source states it: the REST "Create a release" docs describe
   `target_commitish` and `draft` without addressing the timing, and `gh release create`'s manual
   arguably cuts the other way ("Draft releases can be modified or deleted, and the associated git
   tags can be modified or deleted as well"). The only direct support is a 2021 non-staff comment
   in community discussion #24690. Not tested empirically, as that would mean creating a release on
   the live repository. Bears only on a rejected alternative.
2. **Who enabled immutable releases, and when.** The setting itself *is* readable —
   `GET /repos/jacaudi/tempestwx-utilities/immutable-releases` returns
   `{"enabled": true, "enforced_by_owner": false}`, so it is set on the repository rather than
   inherited from the owner. But GitHub's 2025-08-26 changelog says immutable releases were being
   "gradually rolled out to all repositories," so an automatic rollout is as plausible as a
   deliberate act. No audit-log access. This design does not depend on the answer — it stops
   contending with immutability rather than configuring it.
3. **The runtime behaviour of a v3.x GoReleaser binary with an empty `dist/`.** The embedded FS is
   verified empty and `index.html` verified absent, and the handler is
   `http.ServeFileFS(w, r, deps.StaticFS, indexPage)` (`internal/httpserver/server.go:183`), but
   the resulting status code was not observed. Immaterial — no such binary was ever published.

## References

- Failing runs: [30410541653](https://github.com/jacaudi/tempestwx-utilities/actions/runs/30410541653) (v3.0.0, toolchain), [30614807926](https://github.com/jacaudi/tempestwx-utilities/actions/runs/30614807926) (v3.1.0, 422)
- GoReleaser draft-then-publish: `internal/client/github.go:538-540`; `internal/pipe/release/release.go:144,190,200`
- GoReleaser existing-release lookup and update branch: `internal/client/github.go:606,608,647-649,652-655`
- GoReleaser default ldflags: `internal/builders/golang/build.go:120-122`
- GitHub immutable-releases announcement: <https://github.com/orgs/community/discussions/171210>
- GitHub immutable-releases repo API: <https://docs.github.com/en/rest/repos/repos>
- release-please `skip-github-release` semantics: <https://raw.githubusercontent.com/googleapis/release-please/main/schemas/config.json>
