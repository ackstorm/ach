# OpenCode integration (`genai.*`)

ACH configures OpenCode for every user with no API key: one command installs
ACH's sign-in plugin, a second signs the user in through SSO, and from then on
OpenCode's model list, default models and an onboarding skill come from ACH.
All of it is platform-api; these values shape it.

## Contents

- What users run
- The values
- Choosing `providerName`
- Checking it from outside

## What users run

```bash
opencode auth login https://ach.example.com   # installs the plugin from /.well-known/opencode
opencode auth login my-org                    # signs in (OpenCode v1: opencode auth login -p my-org)
opencode service restart                      # OpenCode v2: the service loads plugins only at start
```

Behind it:

- `GET /.well-known/opencode` (anonymous, cached 5 min) names the plugin
  (`genai.opencodePluginSpec`) with options `api` (`<ACH_BASE_URL>/v1`),
  `platform` (the origin) and `provider` (`genai.providerName`), and
  `enabled_providers: [providerName, ...opencodeExtraProviders]`.
- `GET /clients/opencode/config` (per user, with their token) returns their own
  chat models under provider `providerName`, the optional default models, and
  ONE skill named `<providerName>-ach`: a short onboarding that installs
  `ach-cli`, signs in, lists Environments and hydrates
  `genai.defaultEnvironment` globally.
- MCP servers are not in that config: they reach OpenCode only through a
  hydrated Environment (`ach-cli env hydrate <env> --target opencode`).

## The values

```yaml
genai:
  providerName: ai-platform
  opencodePluginSpec: "git+https://github.com/ackstorm/opencode-oidc-provider.git#v<tag>"
  opencodeExtraProviders: []
  defaultModel: ""
  defaultSmallModel: ""
  defaultEnvironment: ""
```

| Key | Env var | Effect |
|-----|---------|--------|
| `providerName` | `ACH_GENAI_PROVIDER_NAME` | The OpenCode provider id users sign in with and see models under; names the onboarding skill `<providerName>-ach`. Must match `^[a-z0-9][a-z0-9-]{0,63}$` and keep `<providerName>-ach` within 64 characters, or platform-api refuses to start. |
| `opencodePluginSpec` | `ACH_OPENCODE_PLUGIN_SPEC` | npm or git spec of the sign-in plugin. Git specs point at the repo root pinned to a tag. Empty disables the manifest's plugin. Keep the chart default unless told otherwise; the plugin lives in `github.com/ackstorm/opencode-oidc-provider`. |
| `opencodeExtraProviders` | `ACH_OPENCODE_EXTRA_PROVIDERS` | Other provider ids allowed in the model picker next to `providerName` (e.g. `claude-code` for a plugin users install themselves). On OpenCode v2 a user's own config cannot widen this list, so any provider users bring must be listed here. Same id rule; a bad id refuses to start. |
| `defaultModel` / `defaultSmallModel` | `ACH_GENAI_DEFAULT_MODEL` / `ACH_GENAI_DEFAULT_SMALL_MODEL` | Bare LiteLLM model names (no provider prefix). Emitted as `config.model` / `config.small_model` only for users whose own chat model list contains that name; otherwise omitted and OpenCode picks the first model. A user's own opencode.json wins. |
| `defaultEnvironment` | `ACH_GENAI_DEFAULT_ENVIRONMENT` | The Environment the onboarding skill hydrates globally (`ach-cli env hydrate <name> -g`). Empty drops that step. Make it an Environment every user can read (e.g. authorized for team `default`), and include the `ach-cli` plugin in it so users' agents get the full `ach-cli` skill. |

## Choosing `providerName`

Pick it once, before users onboard. OpenCode stores the user's credentials and
the plugin stores its data under this id, so changing it signs every OpenCode
user out and leaves their old provider entry behind. If another product already
signs your users in to OpenCode with the same plugin, use a different id unless
that product is being retired, or the two will fight over one credential slot.

## Checking it from outside

```bash
curl -fsS https://ach.example.com/.well-known/opencode | jq
# With a user's token (ach-cli token prints one):
curl -fsS -H "Authorization: Bearer $(ach-cli token)" \
  https://ach.example.com/clients/opencode/config \
  | jq '{auth, stale, model: .config.model, small: .config.small_model, models: [.config.provider[]?.models|keys[]]}'
```

`/clients/opencode/config` answers 200 for any Bearer (an invalid one gets an
empty baseline with `auth: "invalid"`), 401 only without one, and serves the
last good copy (`stale: true`) when LiteLLM or Postgres fail. A default model
that never shows is almost always a name missing from that user's own model
list, or a non-chat model.
