---
name: ach-gitops
description: "Write, review and fix ACH (Agent Capability Hub) custom resources in a GitOps repo: Environment, PluginMarketplace, Plugin, SkillMarketplace, Skill, Prompt, Artifact, BackendIdentityPolicy, KeyGrant and LiteLLMConnection (apiVersion ach.ackstorm.ai/v1alpha1). Use whenever you edit or review YAML of these kinds, grant models, MCP servers, A2A agents, guardrails, plugins or skills to LiteLLM teams through an Environment, set an Environment budget, add a plugin or skill marketplace from GitHub or GitLab, wire authSecretRef for a private repo, turn on the ACH identity JWT for an MCP or A2A backend, let a pod ServiceAccount exchange its token for an Environment credential with a KeyGrant, or debug an ACH resource that is not Synced, SourceReachable or Available (UnresolvedReferences, UpstreamInvalid, ResourceUnresolved, ShellTeamFailed, IdentityUnknown, EnvironmentMissing). Not for day-to-day ach-cli use or for installing the Helm chart."
---

# Writing ACH custom resources

ACH (Agent Capability Hub) is a Kubernetes control plane in front of LiteLLM. Platform
admins declare what each group of people or agents may use as custom resources in a Git
repo; a GitOps tool (Flux, Argo CD) applies them to the cluster where the ACH operator
runs. This skill is for the agent editing that repo.

All kinds are `apiVersion: ach.ackstorm.ai/v1alpha1`, namespaced. Never invent a field:
every field used here exists in the CRD, and depending on the apply tool an unknown
field may be dropped with only a warning, so a typo can apply cleanly and do nothing.
`kubectl explain environment.spec.runtime` (any kind, any path) shows the schema of
the cluster you are targeting.

## Mental model

```
Git (YAML) --GitOps--> CRs --ACH operator--> Postgres projection --> platform-api, forwarder,
                                     |                               content-service read it
                                     +--> LiteLLM: access group, shell team, budget tag
                                     +--> fetches upstream content (git/S3/GCS/HTTP) into a cache

user/agent: ach-cli env hydrate <env>  -> manifest + plugins/skills/prompts/artifacts
            ek-/pk- key or OAuth token  -> forwarder -> LiteLLM (models, /mcp, /a2a)
```

The kinds fall into five groups:

| Family | Kinds | What the operator does |
|--------|-------|------------------------|
| Grant | `Environment` | Resolves names against LiteLLM and writes one LiteLLM access group per Environment, bound to the `authorizedTeams` |
| Content | `Plugin`, `Skill`, `Prompt`, `Artifact`, `PluginMarketplace`, `SkillMarketplace` | Polls an upstream source on `spec.refresh`, validates it, caches it; hydrate serves it |
| Identity | `BackendIdentityPolicy` | Tells the forwarder to sign a JWT for one `/mcp/<name>` or `/a2a/<name>` route |
| Exchange | `KeyGrant` | Projects the grant to Postgres; platform-api lets a named pod ServiceAccount exchange its token for a JWT of one identity's key in one Environment; the operator revokes keys no grant allows any more |
| Connection | `LiteLLMConnection` | The single `default` link to LiteLLM, normally created by the operator from Helm values |

An Environment is the unit people see (`ach-cli env list`), hydrate, and mint `ek-`
keys for. The runtime half (models, MCP servers, agents, guardrails) is enforced by
LiteLLM; the context half (plugins, skills, prompts, artifacts) is installed into the
user's agent tool by `ach-cli env hydrate`.

## Read the reference for the kind you are touching

| Writing or debugging... | Read |
|-------------------------|------|
| An Environment: runtime grants, groups/tags, guardrails, context, teams, budget, conditions | `references/environment.md` |
| The source block shared by every content kind: `type`, `github`/`gitlab`/`bitbucket`/`s3`/`gcs`/`http`, `ref`, `path`, `authSecretRef`, `refresh` | `references/content-sources.md` |
| A PluginMarketplace or SkillMarketplace, `name@marketplace` refs, filters, what a valid plugin or skill directory needs | `references/marketplaces.md` |
| A standalone Plugin, Skill, Prompt or Artifact | `references/objects.md` |
| A BackendIdentityPolicy (JWT to an MCP/A2A backend, consent broker) or the LiteLLMConnection | `references/identity.md` |
| A KeyGrant (ServiceAccount token exchange): fields, Ready reasons, who may write one | `references/keygrant.md` |
| A resource that is not Ready: condition, reason, cause, fix | `references/troubleshooting.md` |

## Workflow

1. **Find the real names first.** Environment runtime entries and `authorizedTeams` must
   match what LiteLLM has, exactly. Ask ACH instead of guessing (admin session needed):

   ```bash
   ach-cli admin list models        # LiteLLM model_name values
   ach-cli admin list mcp           # MCP server_name values
   ach-cli admin list a2a           # A2A agent names
   ach-cli admin list teams         # team aliases for authorizedTeams
   ach-cli admin list guardrails    # names, MODE and DEFAULT-ON
   ach-cli admin list plugins       # bare Plugin CRs and <name>@<marketplace>
   ach-cli admin list skills        # bare Skill CRs and <name>@<marketplace>
   ```

   `ach-cli` already knows the ACH URL from the signed-in profile (or `ACH_URL` before
   the first login). Never hardcode or guess an ACH URL in the repo or in commands.

2. **Edit the YAML** following the repo's existing layout. A common kustomize shape is
   one directory per kind, each with its own `kustomization.yaml`, and a top-level
   kustomization that sets `namespace:` once:

   ```
   ach/
     kustomization.yaml        # namespace: <ACH namespace>; resources: [secrets, skills, ...]
     secrets/                  # ExternalSecret / SealedSecret for authSecretRef, never plaintext
     pluginmarketplace/
     skillmarketplace/
     skills/
     artifacts/
     bip/
     environments/
   ```

   Put every ACH resource in the namespace ACH is installed in: a bare name in an
   Environment resolves against that namespace only, and an `authSecretRef` Secret must
   sit in the same namespace as the resource that names it. Leave a short comment on
   anything non-obvious (why a team gets a server, why a ref is pinned); the next admin
   reads the YAML, not your commit message.

3. **Validate before committing.** Parse the YAML, then let the API server run the CRD
   schema and CEL rules without persisting anything:

   ```bash
   kustomize build ach/ | kubectl apply --dry-run=server -f -
   ```

   Without cluster access, at least check every field name against `kubectl explain` or
   the CRD reference, and check the CEL rules listed in each reference file by hand.

4. **Commit and let the GitOps tool apply it.** Do not `kubectl apply` by hand into a
   GitOps-managed namespace: the next sync reverts or fights it.

5. **Check the result.**

   ```bash
   kubectl get environments,plugins,skills,prompts,artifacts -n <ns>
   kubectl get pluginmarketplaces,skillmarketplaces,backendidentitypolicies -n <ns>
   kubectl get environment <name> -n <ns> -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}: {.message}{"\n"}{end}'
   ach-cli admin list environments       # Available / Degraded(<reason>) / Pending
   ach-cli admin list marketplaces       # Synced status and plugin count
   ach-cli admin refresh <kind> <name>   # re-fetch now instead of waiting for refresh.interval
   ```

   `admin refresh` kinds: `plugin`, `prompt`, `artifact`, `skill`, `marketplace`,
   `skill-marketplace`. Short names: `env`, `bip`, `llmconn`.

   What "healthy" looks like:
   - Content kinds and marketplaces: `SourceReachable=True`; marketplaces also `Synced=True`.
   - Environment: `Available=True`, which needs `AccessGroupSynced=True` and
     `ExecutionResourcesResolved=True`; `BudgetSynced=True` when a budget is set.

## Gotchas that cut across kinds

- **Renaming an Environment is delete + create.** Deleting an Environment revokes every
  `ek-` key issued for it, then removes its LiteLLM access group and shell team. Every
  agent or CI job holding one of those keys breaks. Add the new Environment, migrate
  keys, then delete the old one.
- **Never name an Environment `X` when LiteLLM already has an access group `X` or
  `ach-X`.** Those are older ACH naming schemes, so the operator adopts such a group by
  ID, renames it and rewrites its grants, silently taking it over. ACH's own group is
  `ach-env-<environment>`; check LiteLLM's access groups before picking a name.
- **Grants only add.** A user in several authorized teams reaches the union of every
  Environment granted to those teams. There is no deny list; to take something away,
  remove it from every Environment that grants it.
- **Group tags widen on their own.** `modelGroups`, `mcpServerGroups` and `agentGroups`
  grant whatever LiteLLM tags with that value, now and later, with no change in Git.
  Use explicit names where an audit trail matters.
- **Guardrails need a LiteLLM Enterprise licence.** On an unlicensed proxy any name in
  `spec.runtime.guardrails` blocks the Environment and 403s its keys. Leave it empty
  unless the proxy is licensed.
- **`ref` is a branch or a tag, never a commit SHA.** Pin a tag when reproducibility
  matters; a branch follows upstream on every refresh.
- **`refresh.maxStaleness` is required** and `refresh.interval` must not exceed it.
  Once a fetch keeps failing past `maxStaleness`, the content is no longer served.
- **Bare name vs `name@marketplace`.** In `context.plugins` and `context.skills`, a bare
  name only matches a standalone Plugin or Skill CR; anything that comes from a
  marketplace must be written `name@<marketplace metadata.name>`.
- **Secrets stay out of Git.** `authSecretRef` names a Kubernetes Secret; create it with
  whatever the repo already uses (External Secrets, Sealed Secrets, SOPS). Never put a
  token in a CR, a comment or a commit.
- **LiteLLM caches access-group grants per replica** (600 s by default). A new grant can
  read `AccessGroupSynced=True` and still 403 on some requests for a few minutes. That is
  cache, not a broken CR.
- **The operator owns `LiteLLMConnection/default`**, built from the chart's
  `litellmConnection` values. Do not manage it from the GitOps repo; see
  `references/identity.md`.
