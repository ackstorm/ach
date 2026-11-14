# Upgrade ACH

An upgrade is one `helm upgrade` with a newer chart version and the same
values file. This page gives the procedure, what Helm does during it, and what
a rollback cannot undo.

You need the `values.yaml` of the running install and a backup of the ACH
database. The database is small, and the upgrade does not touch the LiteLLM
database.

## How an upgrade runs

1. Helm renders the chart, including the ACH custom resource definitions,
   which upgrade with the release.
2. The Job `ach-migrate-<revision>` updates the database tables for the new
   version. If it fails three times, the upgrade fails and no component
   changes.
3. The Deployments roll to the new image. The operator stops before its
   replacement starts, so changes to ACH resources wait for a moment. With
   content-service in the operator Pod, installing an Environment waits too.

With `installCRDs: false`, apply the resource definitions from the new chart's
`crd-sources/` directory yourself before the upgrade.

## Procedure

```bash
NEW=<X.Y.Z>; OLD=<the running version>

# 1. Read the changelog, and compare the default values of both versions
diff <(helm show values oci://ghcr.io/ackstorm/charts/ach --version $OLD) \
     <(helm show values oci://ghcr.io/ackstorm/charts/ach --version $NEW)

# 2. Render with your values: a missing or removed value fails here
helm template ach oci://ghcr.io/ackstorm/charts/ach --version $NEW \
  -n ach -f values.yaml > /dev/null

# 3. Upgrade
helm upgrade ach oci://ghcr.io/ackstorm/charts/ach --version $NEW \
  -n ach -f values.yaml --wait --timeout 10m
```

In the comparison of step 1, look for:

- A value you set that was renamed. Helm ignores a key the chart no longer
  reads, and says nothing.
- A value that became required.
- `image.tag`, if you set it yourself: raise it in the same change. Left
  unset, it follows the chart version.

The [changelog](../reference/changelog.md) marks every change that needs a
step from you under **Breaking**. Versions can be skipped unless the changelog
says otherwise: the database updates of every version in between run in order.

## With Flux or Argo CD

- Pin the exact chart version, such as `1.2.3`. A range such as `1.x` upgrades
  production, database included, without review.
- Keep the Secrets in a unit that the ACH release depends on, so they exist
  before the first migration Job runs.
- A retry setting retries a failed migration Job; it does not undo it. Read
  the Job's log before letting retries continue.
- With Flux variable substitution, `$(VAR)` in `extraEnv` is safe, and
  `${VAR}` is replaced by Flux.

## Migrations and rollback

The database only moves forward. `helm rollback` restores the previous
components and leaves the tables as the newer version made them. Most versions
run on newer tables; read the changelog before you roll back.

Three cases where a rollback needs more:

| Rolling back | Needs first |
|---|---|
| Below 1.1.4 | Apply `000028_drop_deletion_timestamp.down.sql` from the ACH repository with `psql`. The rows that version removed do not come back |
| Below the version that added key suspension and expiry | Resume every suspended environment key and clear every expiry. The tables refuse to go back while such a key exists |
| Below the version that added token exchange | Revoke every environment key that was issued by token exchange |

To run the database update yourself, set `migrate.enabled: false` and run the
ACH image with `args: [migrate]` and `ACH_DB_URL` set.

## Values that must not change

| Value | A change breaks |
|---|---|
| Content of the Secret behind `security.keyEncryptionKey` | Every stored LiteLLM key becomes unreadable: users and environment keys get 401 until they are created again |
| Content of the Secret behind `security.credentialHashPepper` | Every ACH key stops being valid |
| `ACH_BASE_URL` | Sign-in for every user, the Dex redirect address, the download links of installed Environments and the MCP entries users' tools saved |
| `genai.providerName` | Signs every OpenCode user out |
| The Secret `ach-jwt-signing-keys` | Deleting it invalidates every access token in use, and components do not start until the operator has created a new one. Replace its keys with [Rotate the JWT signing keys](rotate-jwt-signing-keys.md) |
| The database behind `ACH_DB_URL` | All ACH state. Move the data, not only the address |

You can rename the Secrets as long as their content moves with them.

## Verify

```bash
kubectl -n ach wait deploy -l app.kubernetes.io/name=ach \
  --for=condition=Available --timeout=5m
kubectl -n ach get litellmconnection default        # READY is True
kubectl -n ach get environment                      # AVAILABLE is True for each
ach-cli whoami
```

A Job that succeeded is deleted. If the upgrade failed at the migration, read
its log with `kubectl -n ach logs job/ach-migrate-<revision>`.

OpenCode users who see errors right after an upgrade restart OpenCode
(`opencode service restart` on OpenCode v2).

## Related

- [Changelog](../reference/changelog.md)
- [Troubleshoot an ACH install](troubleshoot.md)
