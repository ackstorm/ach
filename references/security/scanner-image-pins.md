# Secret-scanner image pins (pre-push gate)

`scripts/pre-push-check.sh` runs two secret scanners in throwaway
containers. Both are pinned by **version tag + immutable digest** so a
mutable upstream tag (or a compromised `:latest`) can never silently
change what the publication gate executes.

| Scanner    | Image                          | Version  | Digest |
|------------|--------------------------------|----------|--------|
| gitleaks   | `zricethezav/gitleaks`         | `v8.21.2`| `sha256:0e99e8821643ea5b235718642b93bb32486af9c8162c8b8731f7cbdc951a7f46` |
| trufflehog | `trufflesecurity/trufflehog`   | `3.95.3` | `sha256:9cc33bb080cac0efbbf228a17667172875b529eeeab01efcc4697adfb55f568a` |

The full reference used in `pre-push-check.sh` is `image:version@digest`
— the tag is informational, the digest is authoritative.

## Bumping a scanner

1. Pick the new version tag from the upstream release page.
2. Resolve its digest:

   ```bash
   docker buildx imagetools inspect zricethezav/gitleaks:<new-version> | grep -i '^Digest'
   docker buildx imagetools inspect trufflesecurity/trufflehog:<new-version> | grep -i '^Digest'
   ```

3. Update both the `image:version@digest` string in
   `scripts/pre-push-check.sh` AND the table above in the same commit.
4. Run `make pre-push` once to confirm the pinned image pulls and the
   gate still passes.
