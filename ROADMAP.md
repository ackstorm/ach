# Roadmap

ACH is identity and access for AI agents. An Environment binds what an agent
may call (models, MCP servers, A2A agents, budgets) to what it is given
(plugins, skills, prompts, artifacts) and to who may use it, and `ach-cli`
delivers it into Claude Code, Codex, Gemini CLI, OpenCode and Pi.

Work is grouped as **Now**, **Next** and **Later**. An item moves up when the
one before it ships; dates are not promised. Shipped work is in
[`CHANGELOG.md`](CHANGELOG.md).

## Now — easy to trust

| Item | What it gives you |
|------|-------------------|
| Human documentation | A concepts page with a glossary (`pk_`, `ek_`, Environment, KeyGrant, BackendIdentityPolicy, shell team) and an architecture page. Superseded documents removed. |
| Enterprise licence | The final text for the code under `ee/`: any use of the Enterprise features needs a signed agreement with ACKstorm, evaluation included. |
| Release discipline | Semantic versioning (a breaking change bumps the major version), a `Breaking` section in the changelog, the end-to-end suite green before every release, and a published supported LiteLLM range. |

## Next — extend the identity lead

| Item | What it gives you |
|------|-------------------|
| Admins from an IdP group ([#47](https://github.com/ackstorm/ach/issues/47)) | Admin rights come from a Dex `groups` value instead of a list of e-mail addresses. |
| Content pinning ([#48](https://github.com/ackstorm/ach/issues/48)) | An Environment uses a resolved content revision, recorded by digest. A new upstream revision waits until an admin promotes it. |
| KeyGrant for CI ([#46](https://github.com/ackstorm/ach/issues/46)) | GitHub Actions and GitLab CI OIDC tokens exchange for an Environment-scoped, budgeted credential, as Kubernetes ServiceAccount tokens do today. No stored secret in CI. |

## Later

| Item | What it gives you |
|------|-------------------|
| Signed Enterprise licence key | Offline-verifiable activation instead of a Helm flag. |
| Signature verification | Optional cosign check per marketplace before content is served. |
| More hydrate targets | Cursor and VS Code Copilot. |
| MCP Enterprise-Managed Authorization | MCP clients that follow the standard sign in without ACH-specific login. |
| Roles and delegated ownership | Platform admin, Environment owner and auditor roles; teams own Environments by name prefix. |

## Not planned

- **A second gateway backend.** ACH builds on LiteLLM's virtual keys, teams,
  access groups and tag budgets; another backend would mean rebuilding them.
  The supported LiteLLM range, tested end to end, is the answer to that
  dependency.
- **Content scanning, fleet view, canary rollout, Environment composition, a
  policy engine, a Backstage plugin.** Each is a new subsystem. They come back
  when a customer needs one.
