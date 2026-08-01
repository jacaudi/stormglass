# Remove GoReleaser — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development`
> (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Delete GoReleaser from the release pipeline so tagged releases stop failing, and remove
two dead inputs from the docker composite action.

**Architecture:** CI-configuration-only change. `release-please.yml` keeps creating the tag and the
immutable GitHub Release; the tag push keeps triggering `on-release.yml`, which is reduced from
`tests → {release-image, release-binaries}` to `tests → release-image`. Nothing attaches assets to
a published release any more, so immutability is no longer contended. **No Go source is touched.**

**Tech Stack:** GitHub Actions (workflows + composite actions), YAML. Verification via
`actionlint` and `git grep`.

**Design:** `docs/designs/2026-07-31-remove-goreleaser-binaries-design.md`

> **For Claude:** REQUIRED EXECUTION WORKFLOW (follow in order):
> 1. `superpowers:using-git-worktrees` — already satisfied; work happens in the existing worktree
>    `/Users/acaudill/Projects/github/tempestwx-exporter/.claude/worktrees/release-binaries`
>    on branch `worktree-release-binaries`. Do **not** create another worktree.
> 2. `superpowers:subagent-driven-development` — Dispatch a fresh subagent per task
> 3. `superpowers:test-driven-development` — see **Testing approach** below for how TDD applies
> 4. `superpowers:verification-before-completion` — Verify per task
> 5. `superpowers:requesting-code-review` — Code review after each task (built in)
> 6. After all tasks: comprehensive review on the full diff from branch point (`sr-eng-review`)
> 7. `superpowers:finishing-a-development-branch` — Complete the branch
>
> Skills carry their own model and effort settings. Do not override them.

## Global Constraints

- **Work only in** `/Users/acaudill/Projects/github/tempestwx-exporter/.claude/worktrees/release-binaries`.
  It is a git worktree. Do **not** `cd` to the parent repo at
  `/Users/acaudill/Projects/github/tempestwx-exporter`. Every task brief must open with `cd` to
  the worktree path and confirm `docs/designs/2026-07-31-remove-goreleaser-binaries-design.md`
  exists; if it does not, STOP and report `NEEDS_CONTEXT`.
- **Never bypass commit signing.** 1Password's SSH agent intermittently drops keys, producing
  `1Password: failed to fill whole buffer` / `fatal: failed to write commit object`. Retry the
  same `git commit` two or three times — it usually succeeds. **Never** use `--no-gpg-sign`.
- **Never use bare `git stash` / `git stash pop`** — the stash stack is shared with other
  worktrees. Use a WIP commit instead.
- **Do not push, open a PR, or merge.** The human opens and approves the PR. Committing to the
  local branch is expected and fine.
- **`go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12`** is the exact actionlint
  invocation, matching `.github/workflows/on-pull-request.yml:36`. Do not install actionlint
  another way. First run downloads modules and may take ~2 minutes.
- **No Go source file may change.** If `git diff --name-only` ever lists a `.go` file, something
  is wrong — stop and report.
- `timeout` does not exist on macOS (it is `gtimeout`). Use the Bash tool's own timeout.
- Line numbers below are anchored at `a1b07f1`. **Re-confirm each anchor before editing** — the
  steps below include the confirming command.

## Testing approach — how TDD applies here

This change deletes CI configuration. There is no unit test to write for "a workflow job no longer
exists," so classic red-green-refactor does not apply literally. It is driven instead by
**executable assertions that fail before the change and pass after**:

- `git grep` assertions over the tree — these have a genuine failing state (they print matches
  before the edit, and print nothing after).
- `actionlint` — validates the workflow files still parse and are internally consistent.

Every task below runs its assertions **first**, observes the failing state, makes the change, then
observes the passing state. That ordering is mandatory: a subagent that edits first and greps
afterwards has not demonstrated the assertion could fail, and its report must be rejected.

**Genuinely untestable within this change**, and honestly declared as such: the two comment edits
in `release-please.yml` (Task 1, Step 6) have no observable behavior. They are verified by reading
the resulting text, nothing more.

**One critical caveat, load-bearing for Task 2:** `actionlint` checks **workflow files only** —
composite `action.yml` files are outside its scope
(<https://github.com/rhysd/actionlint/blob/main/docs/usage.md>). So actionlint does **not** cover
the input-declaration deletion. Task 2's grep assertions are the only thing that does, and they
must check **both sides** — declarations *and* callers. Deleting a declaration while a caller still
passes it is the exact failure mode.

## File Structure

Six edits across five files. Two tasks.

| File | Task | Change |
|---|---|---|
| `.goreleaser.yaml` | 1 | delete file |
| `.github/actions/go-release/action.yml` | 1 | delete file (and the emptied `go-release/` dir) |
| `.github/workflows/on-release.yml` | 1 | delete the `release-binaries` job (`:40-52`, runs to EOF) |
| `.github/workflows/release-please.yml` | 1 | comment-only, two sites (`:30`, `:44-45`) |
| `.github/actions/docker/action.yml` | 2 | delete `latest` (`:13-16`) and `tag-strategy` (`:17-20`) declarations |
| `.github/workflows/on-release.yml` | 2 | delete caller lines `:36`, `:38` |
| `.github/workflows/on-push-main.yml` | 2 | delete caller lines `:44`, `:46` |

`on-release.yml` is touched by both tasks, for unrelated reasons. Task 1 removes a job; Task 2
removes two input lines from the *surviving* job. Do them in order.

---

### Task 1: Remove GoReleaser from the release pipeline

**Files:**
- Delete: `.goreleaser.yaml`
- Delete: `.github/actions/go-release/action.yml`
- Modify: `.github/workflows/on-release.yml:40-52` (delete the `release-binaries` job)
- Modify: `.github/workflows/release-please.yml:30` and `:44-45` (comments only)

**Interfaces:**
- Consumes: nothing.
- Produces: an `on-release.yml` of exactly 38 lines whose only jobs are `tests` and
  `release-image`. Task 2 edits that job's `with:` block, which this task leaves at `:34-38`
  (`with:` at `:34`, `token` `:35`, `latest` `:36`, `push` `:37`, `tag-strategy` `:38`).

- [ ] **Step 1: Confirm the worktree and the failing state**

```bash
cd /Users/acaudill/Projects/github/tempestwx-exporter/.claude/worktrees/release-binaries
test -f docs/designs/2026-07-31-remove-goreleaser-binaries-design.md || { echo NEEDS_CONTEXT; exit 1; }
git status --porcelain
git grep -nE 'goreleaser|go-release|release-binaries' -- ':!CHANGELOG.md' ':!docs/'
git ls-files | grep -iE 'goreleaser|go-release' | grep -v '^docs/'
```

Expected — a clean tree, then these exact matches (this is the **failing state**; the assertions
must find something now so that finding nothing later is meaningful):

```
.github/actions/go-release/action.yml:29:      uses: goreleaser/goreleaser-action@f06c13b6b1a9625abc9e6e439d9c05a8f2190e94 # v7.2.3
.github/workflows/on-release.yml:40:  release-binaries:
.github/workflows/on-release.yml:50:        uses: ./.github/actions/go-release
.github/actions/go-release/action.yml
.goreleaser.yaml
```

If the content grep prints nothing, the change is already applied — STOP and report.

- [ ] **Step 2: Confirm the job's exact extent before deleting it**

```bash
wc -l .github/workflows/on-release.yml
sed -n '39,52p' .github/workflows/on-release.yml
```

Expected: `52` lines total, and line 39 blank followed by the `release-binaries:` job through EOF.
The job therefore runs `:40-52` with nothing after it.

- [ ] **Step 3: Delete the two files**

```bash
git rm .goreleaser.yaml
git rm -r .github/actions/go-release
```

Expected: `rm '.goreleaser.yaml'` and `rm '.github/actions/go-release/action.yml'`. Confirm the
directory is gone: `test -d .github/actions/go-release && echo STILL-THERE || echo removed`
→ `removed`.

- [ ] **Step 4: Delete the `release-binaries` job**

Write `.github/workflows/on-release.yml` so its **complete** contents are exactly:

```yaml
---
name: Versioned Release

on:
  push:
    tags:
      - v*

permissions:
  contents: read

jobs:
  tests:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@df4cb1c069e1874edd31b4311f1884172cec0e10 # v6.0.3

      - name: Tests
        uses: ./.github/actions/tests

  release-image:
    runs-on: ubuntu-latest
    needs: tests
    permissions:
      contents: read
      packages: write
    steps:
      - name: Checkout
        uses: actions/checkout@df4cb1c069e1874edd31b4311f1884172cec0e10 # v6.0.3

      - name: Build Tagged Image
        uses: ./.github/actions/docker
        with:
          token: "${{ secrets.GITHUB_TOKEN }}"
          latest: false
          push: true
          tag-strategy: "semver"
```

The `latest:` and `tag-strategy:` lines stay for now — Task 2 removes them. Verify:

```bash
wc -l .github/workflows/on-release.yml
```

Expected: `38`.

- [ ] **Step 5: Re-run the Step 1 assertions — they must now pass**

```bash
git grep -nE 'goreleaser|go-release|release-binaries' -- ':!CHANGELOG.md' ':!docs/'; echo "exit=$?"
git ls-files | grep -iE 'goreleaser|go-release' | grep -v '^docs/'; echo "exit=$?"
```

Expected: no output from either, `exit=1` from both (grep exits 1 when it matches nothing). Any
printed line is a failure.

- [ ] **Step 6: Fix the two now-stale comments in `release-please.yml`**

Two sites reference binaries. Confirm both first:

```bash
git grep -n 'binaries' -- .github/workflows/release-please.yml
```

Expected:

```
.github/workflows/release-please.yml:30:      # (on-release.yml) would never fire and no image or binaries would be
.github/workflows/release-please.yml:45:      # binaries). This workflow deliberately builds nothing itself.
```

Edit 1 — replace this exact text:

```
      # (on-release.yml) would never fire and no image or binaries would be
      # published. An app installation token does trigger it.
```

with:

```
      # (on-release.yml) would never fire and no image would be published. An
      # app installation token does trigger it.
```

Edit 2 — replace this exact text:

```
      # Maintains a release PR from conventional commits. Merging that PR cuts
      # the vX.Y.Z tag, which triggers on-release.yml (tests -> tagged image ->
      # binaries). This workflow deliberately builds nothing itself.
```

with:

```
      # Maintains a release PR from conventional commits. Merging that PR cuts
      # the vX.Y.Z tag, which triggers on-release.yml (tests -> tagged image).
      # This workflow deliberately builds nothing itself.
```

Verify:

```bash
git grep -nc 'binaries' -- .github/workflows/release-please.yml; echo "exit=$?"
```

Expected: no output, `exit=1`.

- [ ] **Step 7: Run actionlint**

```bash
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12; echo "exit=$?"
```

Expected: no output, `exit=0`. (First run may take ~2 minutes downloading modules.)

- [ ] **Step 8: Confirm no Go source changed, and the suite is green**

```bash
git diff --cached --name-only; git diff --name-only
go test ./...
```

Expected: the changed-file lists contain **no** `.go` files — only `.goreleaser.yaml`,
`.github/actions/go-release/action.yml`, `.github/workflows/on-release.yml`,
`.github/workflows/release-please.yml`. `go test ./...` passes. This confirms no collateral
damage; it does not test the change itself.

- [ ] **Step 9: Commit**

```bash
git add -A .github .goreleaser.yaml
git status --porcelain
git commit -m "ci: remove GoReleaser; the container image is the release artifact

release-binaries has failed on both tagged releases since the release-please
migration -- v3.0.0 in GoReleaser's before hook on the Go toolchain floor,
v3.1.0 with 422 Cannot upload assets to an immutable release. release-please
publishes the release before on-release.yml runs, and GoReleaser's
GetReleaseByTag finds it and switches to update-and-upload, which
immutability forbids.

Nobody used the binaries: download counts were 1-3 per asset, uniform across
all eight platforms, and nothing in the tree consumes a release asset. The
README's only documented install path is the container image, which is
already tagged per release. Releases now carry no assets by design.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01S2gxnijhMiDjc7hC1PVB9D"
```

Before committing, `git status --porcelain` must show exactly these four paths and nothing else:

```
D  .goreleaser.yaml
D  .github/actions/go-release/action.yml
M  .github/workflows/on-release.yml
M  .github/workflows/release-please.yml
```

If signing fails with `failed to fill whole buffer`, retry the identical `git commit` up to three
times. Do not add `--no-gpg-sign`.

---

### Task 2: Remove the dead `latest` and `tag-strategy` inputs

**Files:**
- Modify: `.github/actions/docker/action.yml:13-20` (delete both input declarations)
- Modify: `.github/workflows/on-release.yml:36,38` (delete both caller lines)
- Modify: `.github/workflows/on-push-main.yml:44,46` (delete both caller lines)

**Interfaces:**
- Consumes: Task 1's `on-release.yml`, which is 38 lines with `latest: false` at `:36` and
  `tag-strategy: "semver"` at `:38`.
- Produces: a docker action declaring exactly two inputs, `token` and `push`.

**Why this is behavior-neutral:** the action body reads only `inputs.token` (`:50` pre-edit) and
`inputs.push` (`:62` pre-edit). `latest` and `tag-strategy` are never referenced anywhere; the
version step branches on `github.ref_type` / `github.ref_name` instead. Deleting them changes no
tag that gets pushed.

- [ ] **Step 1: Prove the inputs are unreferenced, and capture the failing state**

```bash
cd /Users/acaudill/Projects/github/tempestwx-exporter/.claude/worktrees/release-binaries
git grep -n 'inputs\.' -- .github/actions/docker/action.yml
git grep -nE '^[[:space:]]{2}(latest|tag-strategy):' -- .github/actions/docker/action.yml
git grep -nE '^[[:space:]]+(latest|tag-strategy):' -- .github/workflows/
```

Expected — exactly two `inputs.` references, proving both doomed inputs are dead, then the
declarations and callers that must disappear (the **failing state**):

```
.github/actions/docker/action.yml:50:        password: ${{ inputs.token }}
.github/actions/docker/action.yml:62:        push: ${{ fromJSON(inputs.push) }}
.github/actions/docker/action.yml:13:  latest:
.github/actions/docker/action.yml:17:  tag-strategy:
.github/workflows/on-push-main.yml:44:          latest: true
.github/workflows/on-push-main.yml:46:          tag-strategy: "latest"
.github/workflows/on-release.yml:36:          latest: false
.github/workflows/on-release.yml:38:          tag-strategy: "semver"
```

If the first command prints any reference to `inputs.latest` or `inputs.tag-strategy`, the premise
is wrong — STOP and report; do not delete anything.

- [ ] **Step 2: Delete both input declarations**

In `.github/actions/docker/action.yml`, replace this exact block:

```yaml
  push:
    description: Whether to push the built image
    required: false
    default: "false"
  latest:
    description: Also tag the image as latest
    required: false
    default: "false"
  tag-strategy:
    description: Tag strategy (e.g. latest, semver)
    required: false
    default: "latest"
```

with:

```yaml
  push:
    description: Whether to push the built image
    required: false
    default: "false"
```

- [ ] **Step 3: Delete the caller lines in `on-release.yml`**

Replace this exact block:

```yaml
        with:
          token: "${{ secrets.GITHUB_TOKEN }}"
          latest: false
          push: true
          tag-strategy: "semver"
```

with:

```yaml
        with:
          token: "${{ secrets.GITHUB_TOKEN }}"
          push: true
```

- [ ] **Step 4: Delete the caller lines in `on-push-main.yml`**

Replace this exact block:

```yaml
        with:
          token: "${{ secrets.GITHUB_TOKEN }}"
          latest: true
          push: true
          tag-strategy: "latest"
```

with:

```yaml
        with:
          token: "${{ secrets.GITHUB_TOKEN }}"
          push: true
```

- [ ] **Step 5: Re-run the Step 1 assertions — both sides must now be clean**

```bash
git grep -nE '^[[:space:]]{2}(latest|tag-strategy):' -- .github/actions/docker/action.yml; echo "decls exit=$?"
git grep -nE '^[[:space:]]+(latest|tag-strategy):' -- .github/workflows/; echo "callers exit=$?"
git grep -n 'inputs\.' -- .github/actions/docker/action.yml
```

Expected: no output from the first two, `exit=1` from both. The third must still print exactly two
lines — the same `inputs.token` and `inputs.push` references — but at **shifted line numbers**,
because Step 2 deleted 8 lines above them:

```
.github/actions/docker/action.yml:42:        password: ${{ inputs.token }}
.github/actions/docker/action.yml:54:        push: ${{ fromJSON(inputs.push) }}
```

Two references, no more and no fewer. **Both** the declarations grep and the callers grep must be
empty — a clean declarations grep alone does not prove the change is safe, because it would also
be clean if you had deleted the declarations and left the callers passing them.

Confirm the action now declares exactly two inputs:

```bash
sed -n '/^inputs:/,/^runs:/p' .github/actions/docker/action.yml
```

Expected: `token` and `push` only.

- [ ] **Step 6: Run actionlint**

```bash
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12; echo "exit=$?"
```

Expected: no output, `exit=0`. Remember this covers the two workflow files but **not**
`.github/actions/docker/action.yml` — Step 5's greps are what cover that.

- [ ] **Step 7: Confirm no Go source changed, and the suite is green**

```bash
git diff --name-only
go test ./...
```

Expected: exactly `.github/actions/docker/action.yml`,
`.github/workflows/on-push-main.yml`, `.github/workflows/on-release.yml`. No `.go` files.
`go test ./...` passes.

- [ ] **Step 8: Commit**

```bash
git add .github
git status --porcelain
git commit -m "ci: drop the dead latest and tag-strategy inputs from the docker action

The composite action declares four inputs but its body reads only
inputs.token and inputs.push. latest and tag-strategy were never
referenced -- the version step branches on github.ref_type/ref_name
instead -- so both callers were configuring knobs that did nothing.

No behaviour change: docker-bake.hcl writes a single
\${REGISTRY_IMAGE}:\${VERSION} tag, so a tag build never applied :latest
regardless of what latest: said.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01S2gxnijhMiDjc7hC1PVB9D"
```

`git status --porcelain` must show exactly:

```
M  .github/actions/docker/action.yml
M  .github/workflows/on-push-main.yml
M  .github/workflows/on-release.yml
```

---

## Final verification (after both tasks)

Run from the worktree root. All of these must hold simultaneously:

```bash
# 1. No GoReleaser anywhere outside historical records
git grep -nE 'goreleaser|go-release|release-binaries' -- ':!CHANGELOG.md' ':!docs/'; echo "exit=$?"   # expect exit=1, no output
git ls-files | grep -iE 'goreleaser|go-release' | grep -v '^docs/'; echo "exit=$?"                    # expect exit=1, no output

# 2. Dead inputs gone from both sides
git grep -nE '(latest|tag-strategy):' -- .github/actions/docker/action.yml .github/workflows/; echo "exit=$?"  # expect exit=1

# 3. Workflows still valid
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12; echo "exit=$?"   # expect exit=0, no output

# 4. Surviving jobs intact
grep -nE '^  (tests|release-image):|needs:' .github/workflows/on-release.yml

# 5. No Go source touched on the whole branch
git diff --name-only main...HEAD | grep '\.go$'; echo "exit=$?"   # expect exit=1, no output

# 6. Suite green
go test ./...
```

Step 4 expects `tests:`, `release-image:` and `needs: tests` — confirming the hardened CI gate from
#112 still gates the image build.

**Not verifiable before merge, and that is expected:** the real acceptance is the next tagged
release completing with zero failing jobs and `ghcr.io/jacaudi/tempestwx-utilities:vX.Y.Z`
existing. The strongest pre-merge argument is that in the failing run `30614807926` both surviving
jobs already passed (`tests` ✓ 08:02:03Z, `release-image` ✓ 08:15:27Z); this branch deletes only
the third job and removes unreferenced inputs, so nothing that must keep working is modified.

## Out of scope — do not do these

- **Do not touch `README.md`.** Explicit human decision. The gap that `README.md:17` is untagged
  and resolves to `:latest` (which a tagged release never writes) is recorded in the design's
  Consequences as a known follow-up, not work for this branch.
- **Do not touch the `${{ github.ref_name }}` interpolation** in `.github/actions/docker/action.yml`.
  Explicit human decision — it is a behavior-adjacent edit to the one job that is currently healthy.
- **Do not change `release-please.yml` beyond the two comments** in Task 1 Step 6.
- **Do not re-enable, reconfigure, or replace binary publishing.** Zero assets is the intended
  steady state.
- **Do not touch immutable releases**, which is enabled at the repository level
  (`GET /repos/jacaudi/tempestwx-utilities/immutable-releases` →
  `{"enabled":true,"enforced_by_owner":false}`).
- **Do not push, open a PR, or merge.**
