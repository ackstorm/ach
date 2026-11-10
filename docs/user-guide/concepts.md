# Concepts

ACH decides which person or workload may use which models, tools and agent
context, and delivers that context into their coding tools. It sits between
your identity provider (through Dex) and LiteLLM.

## Environment

The central object. One Environment binds three things, reviewed together in
one pull request:

- **Runtime**: the models, MCP servers, A2A agents and guardrails its users
  may call, resolved against LiteLLM.
- **Context**: the plugins, skills, prompts and artifacts written into a
  user's workspace.
- **Access**: the LiteLLM teams (`authorizedTeams`) whose members may use it,
  and an optional spend ceiling (`budget`).

An Environment is `Available` once its content is fetched and its LiteLLM
access group is in sync.

## Credentials

| Term | What it is |
|------|------------|
| Login | `ach-cli login` signs you in through your company's identity provider. Your tools then act as you. |
| Personal key (`pk_`) | The key behind your login. ACH manages it; you rarely see it. |
| Environment key (`ek_`) | A key for one Environment, for an agent, a CI job or a script. Created with `ach-cli keys create <environment>`, shown once. It can expire, be suspended, resumed and revoked. |
| Key exchange | A pod trades its Kubernetes ServiceAccount token for a short-lived token acting for one Environment. No key is stored in the pod. |
| KeyGrant | The resource an admin writes to allow a key exchange: which ServiceAccount may act as which person in which Environment. |

When your identity provider refuses you (for example, you left the company),
your login and every Environment key you own stop working, within a day. This
needs a Dex connector that asks the identity provider on refresh (Google,
GitLab, Microsoft, GitHub and others); with SAML, an admin revokes the keys.

## Content

| Kind | What it is |
|------|------------|
| Plugin | A Claude Code-style plugin (commands, skills, agents, hooks, MCP config). |
| PluginMarketplace | A repository of plugins, listed by its `marketplace.json`. |
| Skill | One agent skill: a directory with a `SKILL.md`. |
| SkillMarketplace | A repository of skills, discovered by its directory layout. |
| Prompt | One prompt file. |
| Artifact | Any other file or directory delivered to the workspace. |

Content comes from GitHub, GitLab, Bitbucket, S3, Google Cloud Storage or
HTTP, and is refreshed on an interval.

## Delivery

`ach-cli environment hydrate <environment>` writes an Environment's content and
MCP configuration into Claude Code, Codex, Gemini CLI, OpenCode or Pi. It
records what it wrote, keeps your local edits, and `environment uninstall`
removes exactly what it installed.

## Runtime access

| Term | What it is |
|------|------------|
| Forwarder | The ACH endpoint your tools call for models (`/v1`, `/gemini`), MCP servers (`/mcp/<name>`) and A2A agents (`/a2a/<name>`). It checks your credential and passes the request to LiteLLM. |
| BackendIdentityPolicy | Tells the forwarder to send an MCP server or A2A agent a short-lived token naming the caller. Without one, the backend receives no identity. |
| Shell team | A LiteLLM team that allows nothing by itself. Each key lives in one, so it reaches only what its Environments grant. |
| LiteLLMConnection | How ACH reaches LiteLLM and where its master key is. |

## Budgets

Three ceilings apply independently: per person (covering their login and every
Environment key they own), per Environment, and per Environment key. LiteLLM
enforces them; the request that crosses a ceiling is served and the next one
is refused.
