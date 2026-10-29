# Wiring ach-memory into the ACH JWT trust path

[ach-memory](https://github.com/ackstorm/ach-memory) is an MCP backend. This
example wires it into ACH's `/mcp` JWT trust path
(`docs/developer-guide/jwt-forwarder.md`) so it receives the caller's identity
and LiteLLM team aliases (the `groups` claim) on every call, instead of an
unauthenticated request.

## 1. Register the backend in LiteLLM

```bash
curl -s -X POST http://litellm.litellm-system.svc:4000/v1/mcp/server \
  -H 'Authorization: Bearer <master-key>' \
  -H 'Content-Type: application/json' \
  -d '{
    "server_name":    "ach-memory",
    "transport":      "http",
    "url":            "http://ach-memory.ach-memory.svc:8000/mcp/",
    "extra_headers":  ["authorization"],
    "allow_all_keys": true
  }'
```

LiteLLM strips `Authorization` before dispatching unless `extra_headers`
opts in, so without it ach-memory never sees the JWT.

## 2. Apply the BIP

```bash
kubectl apply -f examples/ach-memory/backendidentitypolicy.yaml
```

## 3. Authorize it on an Environment

Add `ach-memory` to `spec.runtime.mcpServers`. The Environment's
`authorizedTeams` become the `groups` claim, so those aliases are the group
ids ach-memory must know.

## 4. Configure ach-memory

```bash
MEMORY_AUTH_JWT_ENABLED=true
# Must equal the token's `iss` EXACTLY. ACH emits its public ACH_BASE_URL
# verbatim, so this stays the public URL even in-cluster.
MEMORY_AUTH_JWT_ISSUER=https://ach.example.com
# Redirect only the KEY FETCH in-cluster. Pointing ISSUER at the in-cluster
# URL to save the egress rejects every token.
MEMORY_AUTH_JWT_JWKS_URI=http://ach-forwarder.ach-system.svc/.well-known/jwks.json
MEMORY_AUTH_JWT_AUDIENCE=mcp:ach-memory
MEMORY_AUTH_JWT_GROUPS_CLAIM=groups
```

The audience matches because the Forwarder mints `aud = "mcp:" + <route name>`,
and the route name here is `ach-memory`.

## 5. Create the groups

Create the groups in ach-memory with `id` set to the LiteLLM team alias
(e.g. `POST /groups {"id": "default"}`), since the claim carries aliases.

## 6. Caveats

- **Two staleness windows, not one.** There is no revocation channel.
  Removing a user from a LiteLLM team affects `pk_` only and takes effect
  at ach-memory after at most `exp` (120s) + the 60s LiteLLM
  team-membership cache ≈ 180s. Dropping a team from the Environment's
  `authorizedTeams` affects **both** `pk_` and `ek_` and takes effect after
  at most `exp` (120s) + the forwarder's `envstore` 5-minute
  NOTIFY-loss safety-net refresh ≈ ~7 minutes worst case.
- **An `ek_` is more privileged than any single human.** It carries the
  union of every team on its Environment, not one caller's teams.
- **An `ek_`'s identity is the minting admin, not the agent.** `sub` (and
  `email`) on an `ek_`-minted token is the `owner_email` of the key row —
  the human who ran `ach-cli keys create` — never the agent process using
  the key. Every pod sharing one `ek_` therefore transacts against
  ach-memory as that one human's identity while simultaneously carrying
  the union of every team on the Environment (the caveat above).
- **Renaming a LiteLLM team orphans the matching group.** The claim carries
  the team alias, not a stable id — rename the team in ach-memory too, or
  the group stops matching.
