# forwarder contract

`ach forwarder`: the data path for `/v1`, `/gemini`, `/mcp/<n>`, `/a2a/<n>` (plus its MCP face `/a2a/<n>/mcp`) and `/.well-known`. Read before changing `internal/forwarder/`, together with [`../../docs/developer-guide/jwt-forwarder.md`](../../docs/developer-guide/jwt-forwarder.md).

## Routes and discovery documents

JWT trust path, `/v1`/`/gemini`/`mcp`/`a2a` rewrite; anonymous `/.well-known/jwks.json` + **ACH-composed** RFC 8414 (`/.well-known/oauth-authorization-server`) + RFC 9728 (`/.well-known/oauth-protected-resource[/v1|/gemini|/mcp/<n>|/a2a/<n>]`) documents (both are served on the path's SHAPE and never on whether the named server exists — an unknown but well-formed `/mcp/<n>` still gets a 200 document, so the anonymous surface is not an existence oracle).

## Credential slots

Authn reads the **declared** credential slots (`forwarder.headers` → `ACH_CREDENTIAL_HEADERS`, in order; defaults `x-ach-key`, `x-api-key`): `mode: resolve` → pk_/ek_ resolved, header removed, the caller's own LiteLLM key forwarded as `x-litellm-api-key`; `mode: passthrough` → the backend's own key forwarded as it came + mirrored to `x-litellm-api-key`, no ACH identity; a `pk-`/`ek-` value is resolved in ANY slot (passthrough and `Authorization` included) and removed — an ACH key never reaches LiteLLM; every other header passes as it came.

## Gemini model names

`/gemini` ALSO rewrites the model name in the path (`forwarder.gemini.stripModelPrefixes` → `ACH_GEMINI_STRIP_MODEL_PREFIXES`, a JSON list, declared order, first match wins): LiteLLM registers a model as `<vendor>.<model>` while a native Gemini client addresses it by its bare Google name, so the listed prefixes are stripped from the segment AFTER `/models/` — anchored there precisely so an entry like `gemini/` cannot match ACH's own `/gemini` route prefix. Empty/unset = no rewrite. `/v1` has no equivalent and will not get one: the model travels in the JSON body there and the Director never reads `req.Body` (streaming) — use LiteLLM's own `model_group_alias` for that surface.

## Budget tags and client tags

Every authenticated request is **stamped with the LiteLLM budget tags** in the Director — `x-litellm-tags: user:<email>[,environment:<env>,key:<ek id>]` (ek_ gets all three, pk_ only `user:`, no ACH identity gets none), which is how ALL of ACH's spend ceilings reach `/v1`, `/gemini`, `/mcp`, `/a2a` and `/v2/model/info` alike; budgets live ONLY on tags, never on a LiteLLM team or user object (measured — see `references/litellm-permission-model.md` §15), and client-supplied body tags (`tags`, `metadata.tags`, `litellm_metadata.tags`) are stripped from JSON request bodies on `/v1`, `/gemini` and `/a2a` (also under the JSON-RPC `params`; 32 MiB cap, 413 `request_too_large`), fail-closed: every non-form body is scanned as JSON whatever its Content-Type (string `metadata` included), a non-empty body that is not valid JSON is 400 `invalid_request`, and a form body with a `tags`/`metadata`/`litellm_metadata` field is 400 `client_tags_not_allowed`.

## Authorization bearer

With no declared slot present, `Authorization: Bearer` is resolved only when it is ours: ACH's own OAuth token (JWS-shaped; a JWS that does not verify is 401 + challenge so MCP clients re-auth) or a `pk-`/`ek-` key — anything else there (LiteLLM's UI bearer, a raw key) is not ours: forwarded untouched with no ACH identity (no precheck, no BIP JWT, no env tag), LiteLLM authenticates it. Nothing presented at all → 401 + `WWW-Authenticate: Bearer resource_metadata=…`.

## Trusted external IdP

**Trusted external IdP** (`forwarder.trustedIdP` → `ACH_TRUSTED_IDP_{ISSUER,AUDIENCES,CLAIM}`, off when the issuer is empty, issuer without audiences refuses to start): a JWS whose unverified `iss` equals that ONE issuer (Dex, for LibreChat) is verified (RS256 discovery JWKS, exp, aud list, claim non-empty, `email_verified` not false) and resolves to the user's EXISTING oauth `pk_` — never minted; each use slides that row like a plain pk_ (7 d, 90 d cap); no live row → 403 `ach_login_required` (names the console), issuer down → 503 `idp_unreachable`; detail in `docs/developer-guide/jwt-forwarder.md`.

## Model list filter

`GET /v1/models` lists only `chat`/`completion` models by default (the console's "chat"; a model without a LiteLLM `mode`, absent or `""`, stays); `?types=<LiteLLM mode>[,…]` adds others, `?types=all` disables the filter (`proxy/models.go`, one of the forwarder's two response-body rewrites); ACH's own views (console, `environment describe`) never read it, and any ACH-internal call through it must pass `?types=all` (heuristic guard test `proxy/models_selfcall_test.go`).

## Refused per-request controls

Request bodies on `/v1`, `/gemini` and `/a2a` are refused with 400 `client_param_not_allowed` when they set a LiteLLM per-request control: `api_key`, `deployment_id`, `guardrails`, `guardrail_config` (also inside `metadata`/`litellm_metadata`, string `metadata` included), or a `tools` entry of type `mcp`; form and multipart bodies refuse the same top-level names. The Environment ceiling lives on the team, not the key, so these are refused, never stripped.

## Gemini routes

`/gemini` serves only `GET /v1beta/models`, `GET /v1beta/models/{m}` and `POST /v1beta/models/{m}:{generateContent|streamGenerateContent|countTokens|embedContent|batchEmbedContents}` (anything else 404 `route_not_allowed`); a body `model`, a `batchEmbedContents` entry model (or any `target_model_names`) that does not name the path's model, or a POST body whose Content-Type is neither `application/json` (parameters allowed) nor absent, is 400 `model_mismatch`.

## Long-running requests

A request with an ACH identity is re-checked against its key row after 60 s and every 60 s until it ends (streams, `/v1/realtime`): a suspended, revoked, expired or missing row ends it (a request still waiting on LiteLLM answers 401 `expired_or_revoked`); Postgres errors fail open with a Warn.

## No catch-all; /v2/model/info

There is **no catch-all**: a path outside `/v1`, `/gemini`, `/mcp/<n>`, `/a2a/<n>`, `/.well-known` is 404 — LiteLLM's own surface (`/ui`, `/key/*`, `/health`…) lives on LiteLLM's own host, never behind ACH. One owned exception: `GET /v2/model/info` (credential required, forwarded like `/v1`) because ach-agent's `litellm_usage` cost source prices there with its `ek_`. LiteLLM has no row for a `model_group_alias`, so `?model=<alias>` goes upstream unchanged (LiteLLM's 403 for a name the caller is not granted stands) and a 200 is answered with the alias's own row: the target deployment's allow-listed caps from a master-key `litellm.CapsCache`, refreshed hourly (`proxy/modelinfo.go`, the other response-body rewrite; `references/litellm-permission-model.md` §16); an alias whose target LiteLLM does not describe passes through.

## The A2A agent's MCP face

`/a2a/<name>/mcp` is the agent's MCP face (`internal/forwarder/a2amcp`, official MCP and A2A Go SDKs): a stateless streamable-HTTP MCP server with one tool, `send_message(message, context_id?)`, described from the agent card LiteLLM serves. An ACH caller is prechecked exactly like `/a2a/<name>` (same 403 codes). The tool then calls `SendStreamingMessage` on the forwarder's OWN `/a2a/<name>` over loopback (`Deps.SelfURL`, from `ACH_FORWARDER_BIND_ADDRESS`) with the caller's credential headers, so the BIP JWT, budget tags, tag stripping and the caller's LiteLLM key all come from that route; never the card's URL, which names LiteLLM's host. Each `working` status update with text becomes an MCP progress notification; the call returns the text plus `context_id` on completed or input-required, and a tool error on failed, canceled, rejected or auth-required. There is no task polling: LiteLLM v1.108.1 forwards `GetTask` as `tasks/get`, which A2A 1.0 agents reject (ackstorm/ach-agent#63).

## Backend identity JWTs and the owner gate

OAuth JWTs have no consent scopes and there is no static scope gate; a BIP JWT carries identity only and an ek_ acts as its owner (`sub`/`email`/`groups` equal the owner's pk_ JWT for that target, plus `azp: ek:<environment>`), while an ek_ with no BIP gets no JWT and no Authorization upstream; the `EkOwnerGate` middleware re-checks owner Environment-access on every authenticated family (`/v1`, `/gemini`, `/mcp`, `/a2a`, `/v2/model/info`) for `ek_` callers only — access lost is 403 `unauthorized_team`, a TeamsResolver failure is 503 `litellm_unreachable`, never a verdict
