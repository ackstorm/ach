# BackendIdentityPolicy and LiteLLMConnection

## BackendIdentityPolicy

Short name: `bip`. Opts one MCP server or A2A agent route into receiving an ACH-signed
identity JWT, so the backend knows which person is calling.

Without a matching policy the forwarder strips the client's `Authorization` header and
sends none of its own. With `forwardIdentityJWT: true` it signs a short-lived (120 s)
EdDSA JWT per request and sends it as `Authorization: Bearer <jwt>`:

| Claim | Value |
|-------|-------|
| `iss` | the ACH base URL |
| `sub`, `email` | the caller's email |
| `aud` | `mcp:<name>` or `a2a:<name>` |
| `groups` | the caller's LiteLLM team aliases (omitted when empty) |
| `iat`, `exp` | issue time, +120 s |

The backend verifies it against the ACH JWKS at `<iss>/.well-known/jwks.json`, checking
`iss`, `aud` and `exp`.

```yaml
# Identity JWT on for the feedback MCP server: it files issues as the caller.
apiVersion: ach.ackstorm.ai/v1alpha1
kind: BackendIdentityPolicy
metadata:
  name: bip-mcp-feedback
spec:
  target:
    kind: MCPServer
    name: mcp-feedback
  forwardIdentityJWT: true
---
# JWT plus chained consent: after the ACH login, the browser is sent on to the
# backend's own OAuth broker so the user grants access to their GitLab account.
apiVersion: ach.ackstorm.ai/v1alpha1
kind: BackendIdentityPolicy
metadata:
  name: bip-mcp-gitlab
spec:
  target:
    kind: MCPServer
    name: mcp-gitlab
  forwardIdentityJWT: true
  consentBroker: https://broker.example.com/gitlab-callback
  consentAudience: gitlab
```

### Fields

| Field | Rule |
|-------|------|
| `spec.target.kind` | `MCPServer` (route `/mcp/<name>`) or `A2AAgent` (route `/a2a/<name>`) |
| `spec.target.name` | The LiteLLM server or agent name, exactly the route segment. DNS-1123 subdomain: lowercase letters, digits, `-` and `.` |
| `spec.forwardIdentityJWT` | Required, no default. `false` is an explicit "no JWT" and behaves like no policy |
| `spec.consentBroker` | Optional, `http(s)://` issuer URL of the backend's own OAuth broker. `MCPServer` targets only |
| `spec.consentAudience` | Audience placed in the signed `login_hint` sent to the broker. Set together with `consentBroker`, or neither |

Admission enforces: `forwardIdentityJWT` present; `consentBroker` and `consentAudience`
both or neither; `consentBroker` only on `MCPServer`.

### Things to know

- **LiteLLM must pass the header through.** Every `/mcp` request goes through LiteLLM,
  which drops `Authorization` by default. Register the MCP server in LiteLLM with
  `extra_headers: ["authorization"]`. Without it the backend never sees the JWT and the
  symptom is an empty tool list or `401 invalid_token`.
- **The target does not need to exist.** The policy is a switch keyed on (kind, name); it
  applies the moment a server with that name is registered and reached.
- **Duplicates resolve alphabetically.** If several policies target the same route, the
  one whose `metadata.name` sorts first wins. Avoid duplicates; if you need one to win,
  rename it.
- **No status.** The operator writes no conditions on a BIP; `kubectl get bip` shows
  target and `ForwardJWT` only, and `ach-cli admin list bips` shows `projected` once it
  reached the database.
- **Personal logins only.** The JWT is minted for people (OAuth or `pk-` keys). Requests
  made with an `ek-` environment key get no JWT and no `Authorization` header; a backend
  that requires identity refuses them.
- **Consent flow.** With `consentBroker`, ACH probes the backend through the forwarder at
  login; only when it answers `auth_required` does it chain the browser through the
  broker, then probes again. Templates such as `${BASE_DOMAIN}` in the URL are fine if
  the GitOps tool substitutes them before apply (Flux `postBuild.substitute`); ACH sees
  only the final value.
- Keep the policy next to the Environment that grants the server, or in its own
  directory (`bip/`), one file per backend with a comment saying what the backend does
  with the identity.

## LiteLLMConnection

Short name: `llmconn`. The single connection from the operator to LiteLLM. Only one is
allowed per namespace, and it must be named `default` (admission rejects any other name).

**Do not manage it from the GitOps repo.** The operator creates and keeps
`LiteLLMConnection/default` in sync from the Helm chart's `litellmConnection.endpoint`
and `litellmConnection.masterKeySecretRef` values, correcting drift on every start. A
copy in Git would fight the operator. Change the endpoint or Secret through the chart
values instead.

For reference, the shape it has:

```yaml
apiVersion: ach.ackstorm.ai/v1alpha1
kind: LiteLLMConnection
metadata:
  name: default
spec:
  endpoint: http://litellm.litellm.svc.cluster.local:4000
  masterKeySecretRef:
    name: litellm-master-key    # Secret in the same namespace
    key: master_key
```

Its `Ready` condition is what to check when every Environment fails at once:

| `Ready` reason | Meaning |
|----------------|---------|
| `Synced` | Probe OK |
| `Connecting` | First probe in progress |
| `SecretNotFound` | The master-key Secret or its key is missing |
| `BadMasterKey` | LiteLLM answered 401 to the master key |
| `Unreachable` | The endpoint did not answer |

```bash
kubectl get llmconn default -n <ns>
```
