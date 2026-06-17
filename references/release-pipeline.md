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
with tag-last**: a push to `main` whose head commit message starts with
`chore(release): v<MAJOR>.<MINOR>.<PATCH>` fires the pipeline. The
workflow then runs the tests, bumps manifests itself, builds + signs
artifacts, and creates the git tag as the final step — so a failure
anywhere upstream leaves origin with no orphan tag.

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
2. **run-tests** job: `make test-unit` + `make test-envtest-fast`.
   Failures stop the pipeline here — no manifest mutation, no tag.
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
   - goreleaser runs with `GORELEASER_CURRENT_TAG=v<X.Y.Z>` (no git
     tag at HEAD yet). The GitHub release-create API call auto-creates
     the tag at default-branch HEAD, which is the bot-bump commit.
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
   - **LAST**: idempotently creates and pushes the annotated git tag
     `v<X.Y.Z>`. If goreleaser's release API call already implicitly
     created the tag, this is a no-op.

Orphan-tag posture: tag-creation is the LAST step. A failure in tests
or bump or goreleaser leaves no tag on origin and no GH release
attached to one. The bot bump commit may be on `main` if the failure
happened in goreleaser — that is reversible by reverting the bot
commit or by simply running the next release attempt, since `make release-bump`
inside the workflow is idempotent.

Snapshot builds (`.goreleaser.snapshot.yml`) are intentionally NOT
signed and do NOT generate SBOMs — they are ephemeral dev artifacts
pushed as `ghcr.io/ackstorm/ach:main` +
`:main-<shortcommit>`.

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
