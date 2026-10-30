# Content sources (shared by every content kind)

`Plugin`, `Skill`, `Prompt`, `Artifact`, `PluginMarketplace` and `SkillMarketplace` all
point at an upstream with the same block: a `type`, the one matching sub-object, and a
`refresh` block. The operator polls the upstream, validates the result, and caches it
for hydrate.

## Shape

```yaml
spec:
  type: gitlab                 # github | gitlab | bitbucket | s3 | gcs | http
  gitlab:                      # exactly the sub-object named by type (CEL-enforced)
    host: git.example.com      # bare host or https://host; default gitlab.com
    project: my-org/agent-plugins
    ref: main                  # branch or tag
    path: plugins/code-review  # optional; see "path" below
    authSecretRef:
      name: git-read-token     # Secret in the SAME namespace as this CR
      key: token               # optional for git types; see defaults below
  refresh:
    interval: 1h               # optional; must be <= maxStaleness
    maxStaleness: 24h          # required
```

## Source types

| `type` | Fields | Auth |
|--------|--------|------|
| `github` | `repo` (`owner/name`, required), `ref` (required), `path` | optional `authSecretRef`; token key defaults to `GITHUB_TOKEN` |
| `gitlab` | `host` (default `gitlab.com`), `project` (`namespace/project`, required), `ref` (required), `path` | optional `authSecretRef`; token key defaults to `GITLAB_TOKEN` |
| `bitbucket` | `workspace` (required), `repo` (required), `ref` (required), `path` | optional `authSecretRef`; token key defaults to `BITBUCKET_TOKEN` |
| `s3` | `bucket`, `key` (object key or prefix), `region` (all required), `endpoint` (S3-compatible stores) | **required** `authSecretRef` with `accessKeyIdKey` and `secretAccessKeyKey` |
| `gcs` | `bucket`, `object` (object or prefix), both required | **required** `authSecretRef`; `key` names the service-account JSON |
| `http` | `url` (required, http or https) | optional `authSecretRef` with `headerName` + `headerValueKey` |

`s3`, `gcs` and `http` have no `ref`: the operator detects change with the ETag,
generation or `If-None-Match`/`If-Modified-Since`. Use `https://` URLs in production.

Git types (`github`, `gitlab`, `bitbucket`) are fetched with git itself (`ls-remote` +
clone), so there is no REST rate limit to worry about.

## ref

`ref` is a branch or tag name. A commit SHA does not work: the operator resolves `ref`
with `git ls-remote`, which only advertises refs. Pick deliberately:

- A **branch** (`main`) follows upstream: every refresh can bring new content to every
  user at their next hydrate. Fine for your own repos.
- A **tag** (`v1.4.0`) is reproducible. Prefer it for third-party repos, and bump it in
  Git when you want the update.

`status.upstreamRev` shows the commit (or ETag/generation) the last successful fetch got.

## path

On git types, `path` means two different things depending on the kind:

| Kind | `path` does |
|------|-------------|
| `Plugin`, `Skill`, `Artifact` (directory) | Narrows the fetch to that subdirectory; it becomes the root of the content |
| `Prompt`, `Artifact` with `scope: object` | Points at one file; that file's bytes are the content |
| `PluginMarketplace` | Ignored. The whole repo is fetched and `.claude-plugin/marketplace.json` is found by convention |
| `SkillMarketplace` | The whole repo is fetched; `path` is the directory whose children are skill folders. Unset means `skills`; use `"."` for skills at the repo root |

Empty `path` on an object kind means the whole repo. A path that is missing, escapes the
repo, or is a symlink fails with `UpstreamInvalid`.

## authSecretRef

Use it for private repos and buckets. Without it the fetch is anonymous, which only works
for public sources (and a private or misspelled repo then fails with `Unauthorized`,
because git cannot tell "private" from "does not exist").

| Field | Used by |
|-------|---------|
| `name` | all; the Secret must be in the CR's namespace |
| `key` | git types (bearer token; defaults above) and `gcs` (service-account JSON) |
| `accessKeyIdKey`, `secretAccessKeyKey` | `s3` |
| `headerName`, `headerValueKey` | `http` (e.g. `Authorization` and the key holding `Bearer ...`) |

Self-hosted GitLab authenticates git over HTTPS with Basic `oauth2:<token>`, which works
with personal, group and project access tokens.

Keep the Secret out of Git in plain form. Example with External Secrets:

```yaml
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: git-read-token
spec:
  refreshInterval: 1h
  secretStoreRef:
    kind: ClusterSecretStore
    name: default
  target:
    name: git-read-token
    creationPolicy: Owner
  data:
    - secretKey: token
      remoteRef:
        key: ach/git-read-token
        property: token
```

One read-only group token Secret can serve every CR that pulls from the same Git group.
A rotated Secret is picked up on the next reconcile.

## refresh

| Field | Meaning |
|-------|---------|
| `maxStaleness` (required) | Oldest cached snapshot that may still be served. If fetches keep failing past this age, hydrate gets 503 `stale_cache_expired` for that content |
| `interval` | How often the operator polls. Unset means `maxStaleness / 2`. Must be `<= maxStaleness` (admission rejects otherwise) |

Both are Kubernetes durations (`30m`, `6h`, `72h`). Typical values: `1h`/`24h` for your
own repos, `6h`-`12h`/`72h` for slow-moving third-party repos. A very short interval
(`1m`) is only for tests.

To pick up an upstream change before the next interval, run
`ach-cli admin refresh <kind> <name>`. Any spec change also triggers a fresh fetch.
