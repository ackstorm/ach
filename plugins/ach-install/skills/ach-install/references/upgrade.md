# Upgrading ACH

## Contents

- How an upgrade runs
- Procedure
- GitOps (Flux / Argo CD)
- Migrations and rollback
- Values that must never change
- After the upgrade

## How an upgrade runs

`helm upgrade` with the chart at version `<X.Y.Z>` does, in order:

1. Renders the templates, including the CRDs (`installCRDs: true` ships them
   as normal templates, so they upgrade with the release; they carry
   `helm.sh/resource-policy: keep` and survive `helm uninstall`).
2. Runs the pre-upgrade hook Job `ach-migrate-<revision>` (`ach migrate`),
   which applies every pending Postgres migration bundled in the new image
   against `ACH_DB_URL`. It reads only the top-level `extraEnv`. If it fails
   (3 retries), the upgrade fails and no Deployment changes.
3. Rolls the Deployments onto the new image. The operator uses the Recreate
   strategy (brief reconcile and, with the sidecar topology, hydrate pause).

There is no separate CRD step: ignore any instruction (including an older
`NOTES.txt`) to `kubectl apply` the CRDs by hand before upgrading, unless you
run with `installCRDs: false`, in which case apply the new chart's
`crd-sources/` yourself first.

## Procedure

```bash
NEW=<X.Y.Z>; NS=ach
# 1. Read what changed: release notes and the new default values
helm show values oci://ghcr.io/ackstorm/charts/ach --version $NEW > values-$NEW.default.yaml
helm get values ach -n $NS > values-current.yaml
diff <(helm show values oci://ghcr.io/ackstorm/charts/ach --version <OLD>) values-$NEW.default.yaml
# 2. Render with your values: every `fail`/`required` shows here, not mid-upgrade
helm template ach oci://ghcr.io/ackstorm/charts/ach --version $NEW -n $NS -f values.yaml >/dev/null
# 3. Optional: see what changes (helm-diff plugin)
helm diff upgrade ach oci://ghcr.io/ackstorm/charts/ach --version $NEW -n $NS -f values.yaml
# 4. Upgrade
helm upgrade ach oci://ghcr.io/ackstorm/charts/ach --version $NEW -n $NS -f values.yaml \
  --wait --timeout 10m
```

Things to look for in the diff of default values:

- New required values or renamed keys (a renamed key you still set is silently
  ignored; a removed one may now fail the render).
- A new `genai.opencodePluginSpec` tag: users get the new plugin on their next
  `opencode auth login <url>`.
- New Enterprise-only values: they stay inert unless `enterprise.enabled`.
- Image tag: the chart default follows its own version; if you pin
  `image.tag`, bump it in the same change.

Before upgrading across several versions, upgrade one release at a time only if
the release notes ask for it; migrations are cumulative and run in order.

## GitOps (Flux / Argo CD)

- Pin the chart version exactly (`version: 1.2.3`), not a range like `1.x` or
  `1.2.x`. A floating range rolls every ACH patch into production unreviewed,
  migrations included.
- Keep the Secrets (DEK, pepper, Dex client, LiteLLM key) in a separate
  Kustomization/Application that the HelmRelease depends on, so they exist
  before the first migrate Job.
- Flux `install/upgrade.remediation.retries` retries a failed migrate; it does
  not roll the schema back. Investigate a failing `ach-migrate-*` Job rather
  than letting retries spin.
- If Flux substitutes variables (`postBuild.substitute`), Kubernetes-style
  `$(VAR)` references in `extraEnv` are safe; `${VAR}` ones are substituted by
  Flux.

## Migrations and rollback

- Migrations only move forward. `helm rollback` restores the old manifests and
  image but leaves the schema at the newer version, because no hook runs a
  down migration. Read the release notes before rolling back: most releases
  tolerate it, some do not.
- One known hard stop: once any `ek-` key has been suspended or given an
  expiry, the schema cannot be migrated below the release that introduced key
  states; the down migration refuses. Resume suspended keys and clear expiries
  first if such a rollback is ever required.
- Back up the ACH database before every upgrade (it is small). LiteLLM's
  database is separate and is not touched by ACH migrations.
- To run migrations by hand (e.g. with `migrate.enabled: false` in a pipeline
  that migrates separately), run the same image with `args: [migrate]` and
  `ACH_DB_URL` set. `ACH_MIGRATIONS_PATH` defaults to the bundled
  `/db/migrations`.

## Values that must never change

| Value | What a change breaks |
|-------|----------------------|
| `security.keyEncryptionKey` Secret content | Every sealed LiteLLM key becomes unreadable: users and `ek-` keys get 401 until re-minted. |
| `security.credentialHashPepper` Secret content | Every stored `pk-`/`ek-` credential hash stops matching: all keys invalid. |
| `ACH_BASE_URL` | OAuth issuer and every issued token, the Dex redirect URI, hydrated download URLs, MCP entries users' tools saved, and which LiteLLM keys the orphan reaper considers this release's. |
| `genai.providerName` | Signs every OpenCode user out. |
| `ach-jwt-signing-keys` | Deleting it signs everyone out; rotate it with the overlap procedure in `references/auth.md`. |
| `ACH_DB_URL` target | ACH's whole state; move the data, not just the URL. |

Renaming the Secrets is fine as long as their content moves with them.

## After the upgrade

Run the verification checklist in `SKILL.md`. Also:

```bash
kubectl -n ach get env            # every Environment still Available
kubectl -n ach logs job/ach-migrate-<revision>   # only if it failed; succeeded Jobs are deleted
kubectl -n ach logs deploy/ach-platform-api | head -50
```

If users report OpenCode problems right after an upgrade, have them restart
OpenCode (`opencode service restart` on v2) so the plugin reloads.
