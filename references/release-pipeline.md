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
`.github/workflows/release.yml`. The flow is **commit-message-driven
with tag-before-build**: a push to `main` whose head commit message starts
with `chore(release): v<MAJOR>.<MINOR>.<PATCH>` fires the pipeline. The
workflow then runs the tests, bumps manifests itself, builds the console,
**creates and force-pushes the git tag**, and only then runs goreleaser to
build + sign artifacts. Because the tag precedes the build, a failure in
goreleaser or the chart push **does leave an orphan tag on origin** — see
"Orphan-tag posture" below.

Cutting a release (stable example, `v0.1.0`):

```bash
# Most common — empty release commit (no manifest pre-bump).
# `make release-cut` runs preconditions (on main, clean tree, in-sync
# with origin/main), creates `chore(release): v0.1.0` as an empty
# commit, runs the 17-gate pre-push, and pushes to main.
make release-cut VERSION=0.1.0

# Bundle the release intent with a real change:
# (edit, then commit the change yourself, then:)
git commit -am 'chore(release): v0.1.0'
make pre-push
git push origin main
```

There is no need to `make release-bump` locally or to create the tag
yourself. `make release-bump VERSION=X.Y.Z` is still available as the
internal target release.yml invokes; it can also be run by hand if you
want to pre-bump manifests in the same commit (the workflow detects the
clean tree and skips its own bump step), but it is not the expected
workflow.

Per-release flow (after the `chore(release): v0.1.0` push):

1. **parse** job (job-level `if` skips non-release pushes): pulls
   `X.Y.Z` from the head commit message via regex.
2. **test-unit** + **test-envtest** jobs (parallel): `make test-unit` and
   `make test-envtest-fast` run as two concurrent jobs, both gating
   build-and-release — the test phase costs `max(unit, envtest)` (envtest-fast
   dominates) instead of their sum. Either failing stops the pipeline here —
   no manifest mutation, no tag.
3. **build-and-release** job:
   - Configures the github-actions[bot] identity.
   - Runs `make release-bump VERSION=X.Y.Z`, commits the four bumped manifests
     to `main` with a `[skip ci]` marker, and pushes the bot commit.
     If the tree is already clean (user pre-bumped), this is a no-op.
   - Picks the goreleaser config:
     - `vX.Y.Z`                  → `.goreleaser.yml`            (stable)
     - `vX.Y.Z-{alpha,beta,rc}*` → `.goreleaser.prerelease.yml`
   - `make gen-code gen-manifests` regenerates CRDs (sanity).
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
     `npm --prefix ui ci && npm --prefix ui run build` — the console is
     `go:embed`ded from `internal/platformapi/console/dist`, and goreleaser
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
- a fresh `chore(release): vX.Y.Z` push — HEAD moves and the force-push
  re-points the tag.
- **abandoning that version** (what v0.9.11 did, moving on to v0.9.12) — then
  DELETE the orphan tag by hand: `git push origin :refs/tags/vX.Y.Z`. Left in
  place, the Go module proxy will still serve `@vX.Y.Z` and `helm pull
  --version X.Y.Z` 404s.

The bot bump commit may also be on `main` if the failure happened in
goreleaser — reversible by reverting the bot commit or by simply running the
next release attempt, since `make release-bump` inside the workflow is
idempotent.

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
