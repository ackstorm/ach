# Release pipeline + docs site

> Relocated from `CLAUDE.md` to keep the hub lean. Authoritative narrative
> for the goreleaser/`release.yml` flow and the mkdocs site. Update in the
> SAME commit when `release.yml`, the goreleaser configs, the bump flow, or
> the docs deploy change (Documentation-hygiene rule).
>
> See also: `.goreleaser.yml` + `.github/workflows/release.yml` (authoritative
> source), and `references/makefile.md` for `release-cut` / `release-bump`.

## Release pipeline

Release artifacts are produced by **goreleaser** orchestrated by
`.github/workflows/release.yml`. A release is a PR: `make release-cut` opens
`chore(release): vX.Y.Z` with the rotated CHANGELOG and manifest bumps in one
commit, and sets auto-merge, so **running it publishes the release**. The merge
pushes that commit to `main`, which fires the pipeline. The workflow builds the
console, **tags exactly that commit**, then runs goreleaser to build and sign
artifacts. It runs no tests: branch protection, admins included, means the
commit already passed `ci-ok`. Because the tag precedes the build, a failure in
goreleaser or the chart push **does leave an orphan tag on origin** — see
"Orphan-tag posture" below.

Cutting a release (stable example, `v0.1.0`):

Before cutting, check whether the range since the last tag changes anything the
agent guidance tells users' agents to do: the `<provider>-ach` bootstrap
(`internal/platformapi/opencode/skill/SKILL.md` — install, login, `env list`,
named and default hydrate, the OpenCode sign-in flow) and the `plugins/` marketplace
(`ach-cli` commands/flags, CRD fields for `ach-gitops`, chart values for
`ach-install`). If it does, fix it in the release, not after, and bump the
plugin's `version` (both manifests):

```bash
git diff "$(git describe --tags --abbrev=0)"..HEAD --stat -- cmd/ach-cli internal/cli internal/platformapi/opencode api deploy/helm/ach/values.yaml plugins
```

```bash
# `[Unreleased]` must not be empty: `scripts/changelog.sh rotate` refuses it.
# Preconditions: on main, clean tree, in sync with origin/main.
make release-cut VERSION=0.1.0
```

The PR only touches `CHANGELOG.md` and version lines, so `ci-ok` passes without
running tests (`scripts/ci-changes.sh`). The GitHub release notes are that
version's CHANGELOG section (`scripts/changelog.sh extract`), so upgrade and
rollback warnings written there reach the release page. Do not set
`changelog.disable` in the goreleaser configs: a disabled changelog pipe never
loads `--release-notes`.

After the release is confirmed OK (`Latest` in `gh release list`, the
`Release` run green), run the post-release cleanup in CLAUDE.md "After every
release — clean up".

Per-release flow (after the `chore(release): v0.1.0` PR merges):

1. **parse** job (job-level `if` skips non-release pushes): pulls
   `X.Y.Z` from the head commit message via regex.
2. **build-and-release** job, checked out at `github.sha` so a merge landing
   during the run does not ship:
   - Configures the github-actions[bot] identity.
   - Checks that `Chart.yaml` carries the version, then extracts the release
     notes from the CHANGELOG.
   - Picks the goreleaser config:
     - `vX.Y.Z`                  → `.goreleaser.yml`            (stable)
     - `vX.Y.Z-{alpha,beta,rc}*` → `.goreleaser.prerelease.yml`
   - Persists the **native** runner Go caches (`~/.cache/go-build` +
     `~/go/pkg/mod`) via `actions/cache` with a `restore-keys` prefix
     fallback — setup-go's built-in cache is disabled (`cache: false`)
     because its single-`go.sum`-key scheme cold-misses on any dep
     change. The per-GOOS/GOARCH cross-compile rebuilds the full
     k8s.io + controller-runtime tree; the prefix fallback keeps that
     heavy tree warm across releases even when `go.sum` shifts. This is
     distinct from devtools' per-worktree `.gocache/` (which `make`
     targets use) — only the native cache feeds goreleaser.
   - cosign + cyclonedx-gomod installed on PATH (HRD-09).
   - `actions/setup-node` (22, npm cache on `ui/package-lock.json`) +
     `npm --prefix ui ci && npm --prefix ui run build` — the
     console is `go:embed`ded from `internal/platformapi/console/dist`, and goreleaser
     builds the binaries on this runner, so **dist/ must be populated
     before goreleaser runs** (`Dockerfile.goreleaser` only copies the
     finished binaries). The source archive ships `ui/` sources by explicit
     path (never `ui/node_modules`).
   - Asserts a **clean working tree** (`git status --porcelain`) before
     tagging. goreleaser refuses to run dirty, and it runs after the tag is
     already public — so a build step that dirties the tree would burn the
     version number. v0.9.11 died exactly this way (`vite build`'s
     emptyOutDir deleted the tracked `dist/.gitkeep`); the assert now fails
     while the tag is still private.
   - Force-creates and pushes the annotated tag `v<X.Y.Z>` at HEAD, BEFORE
     goreleaser, which then runs against a real tag. `-f` is deliberate: it
     re-points a stale tag left by a previous failed attempt whose HEAD has
     since moved. Guard: the step refuses to move a tag whose GitHub release
     already exists.
     - cross-builds amd64 + arm64 (CGO_ENABLED=0, alpine runtime with
       git + ca-certificates baked).
     - builds multi-arch manifest list at
       `ghcr.io/ackstorm/ach:vX.Y.Z` (+ `:latest` on
       stable).
     - the Enterprise code is part of that same image; there is no second
       image. It runs only with Helm `enterprise.enabled` (`ACH_ENTERPRISE`).
     - builds the **ach-cli** image (G4) — multi-arch manifest list at
       `ghcr.io/ackstorm/ach-cli:vX.Y.Z` (+ `:latest` on stable) from
       `Dockerfile.ach-cli` (distroless static, server-free: no git, no
       migrations). Consumed by the `examples/ach-cli-initcontainer.yaml`
       headless-agent bootstrap.
     - `sboms:` block generates the CycloneDX SBOM via cyclonedx-gomod.
     - `signs:` block signs the checksums file with cosign keyless OIDC.
     - `docker_signs:` block signs all image artifacts (per-arch +
       manifest list) with cosign keyless OIDC.
   - Pushes the chart to
     `oci://ghcr.io/ackstorm/charts/ach:<X.Y.Z>`.
Orphan-tag posture: **the tag is pushed BEFORE goreleaser runs**, so a
failure in goreleaser or the chart push leaves `refs/tags/vX.Y.Z` on origin
with no GitHub release, no image, and no chart behind it. This is not
theoretical — v0.6.13 and v0.9.11 both hit it. Recovery is one of:

- `gh run rerun --failed` — HEAD is unchanged, the tag already points at the
  right commit, and the run resumes.
- **abandoning that version** (what v0.9.11 did, moving on to v0.9.12) — then
  DELETE the orphan tag by hand: `git push origin :refs/tags/vX.Y.Z`. Left in
  place, the Go module proxy will still serve `@vX.Y.Z` and `helm pull
  --version X.Y.Z` 404s.

There is no snapshot config: `release.yml` selects `.goreleaser.yml`
(stable) or `.goreleaser.prerelease.yml` (`-alpha|beta|rc`).
`.goreleaser.snapshot.yml` was deleted 2026-09-21 — nothing invoked it.

`docker_signs:` and `signs:` blocks require:
- `id-token: write` in the workflow (already set).
- cosign on PATH (release.yml installs via `sigstore/cosign-installer`).

## Documentation site (mkdocs)

The public docs site at `docs/` is mkdocs-material based.

```bash
make gen-crd-ref-docs                    # regenerate docs/api-reference/ from CRDs
make docs-build                          # build site/ via docker (host)
make docs-serve                          # local preview at :8000
```

`docs/.crd-ref-docs.yaml` is the config for the `crd-ref-docs` tool
(installed via `make crd-ref-docs`); it targets the ACH API groups
(`ach.ackstorm.ai/v1alpha1`).

The site publishes to `https://ackstorm.github.io/ach/` via the
`mike` versioned-docs flow.

`.github/workflows/docs.yml` deploys the site to `gh-pages` on
pushes to `main` and on `v*` tags. PRs build the site (no deploy) to
catch broken links and missing pages.
