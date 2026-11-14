# Rotate the JWT signing keys

ACH signs two kinds of token with one Ed25519 key: the access tokens of
signed-in users, and the identity tokens the forwarder adds for MCP and A2A
backends. This page replaces that key without rejecting valid tokens.

You need `kubectl` access to the ACH namespace, `openssl` and `jq`.

## When to rotate

- On the schedule your security policy sets. ACH does not rotate the key by
  itself.
- At once, when the key may have leaked: use
  [Emergency revocation](#emergency-revocation).

A planned rotation takes a day. Backends keep the published keys for up to one
hour, so the new key is published for 24 hours before ACH signs with it.

## Secret layout

The key is in the Secret `ach-jwt-signing-keys` in the ACH namespace. The
operator creates it on its first start, and it is kept when the release is
uninstalled.

| Data key | Content |
|---|---|
| `current.kid` | Name of the key ACH signs with |
| `current.seed` | Its 32-byte seed |
| `next.kid`, `next.seed` | The key that comes next. Present only during a rotation |

`https://ach.example.com/.well-known/jwks.json` publishes the public key of
`current`, and of `next` while it is set.

## Steps

### 1. Generate the next key

```bash
NEXT_KID_B64=$(printf 'ach-jwt-%s' "$(date +%s)" | base64)
NEXT_SEED_B64=$(openssl rand -base64 32)
```

Keep both values until step 5, and treat the seed as a secret.

### 2. Add it as `next`

```bash
kubectl -n ach patch secret ach-jwt-signing-keys --type=json \
  -p="[{\"op\":\"add\",\"path\":\"/data/next.kid\",\"value\":\"$NEXT_KID_B64\"},
       {\"op\":\"add\",\"path\":\"/data/next.seed\",\"value\":\"$NEXT_SEED_B64\"}]"
```

### 3. Check that both keys are published

```bash
curl -s https://ach.example.com/.well-known/jwks.json | jq -r '.keys[].kid'
```

It prints two names. The forwarder logs `jwt signing keys reloaded` when it
reads the change.

### 4. Wait 24 hours

Every backend must fetch the new public key before ACH signs with it. A
backend that has not rejects the tokens signed after step 5.

### 5. Make `next` the current key

```bash
kubectl -n ach patch secret ach-jwt-signing-keys --type=json \
  -p="[{\"op\":\"replace\",\"path\":\"/data/current.kid\",\"value\":\"$NEXT_KID_B64\"},
       {\"op\":\"replace\",\"path\":\"/data/current.seed\",\"value\":\"$NEXT_SEED_B64\"},
       {\"op\":\"add\",\"path\":\"/data/next.kid\",\"value\":\"\"},
       {\"op\":\"add\",\"path\":\"/data/next.seed\",\"value\":\"\"}]"
```

An empty `next.kid` clears the slot.

The forwarder reads the change at once. platform-api and content-service read
it within about 90 seconds. An access token that platform-api signs with the
old key during that time is refused by the forwarder until the user's tool
renews it.

### 6. Check the result

```bash
curl -s https://ach.example.com/.well-known/jwks.json | jq -r '.keys[].kid'
```

It prints one name, the new one.

## Emergency revocation

When the current seed may have leaked, do not wait:

1. Generate a key as in step 1.
2. Replace `current.kid` and `current.seed` directly, with the two `replace`
   operations of step 5.
3. Expect up to one hour in which backends still accept tokens signed with
   the leaked key, and reject tokens signed with the new one, until they
   fetch the published keys again. ACH cannot shorten that hour.

Deleting the Secret also replaces the key, at the operator's next start, but
it signs every user out and leaves ACH without a key until then.

## Verify

```bash
curl -s https://ach.example.com/.well-known/jwks.json | jq '.keys | length'   # 1
ach-cli whoami
```

`whoami` answers, and an MCP server that checks the identity token lists its
tools.

## Related

- [Forwarder JWT](../reference/jwt.md): the claims and the published keys.
- [Trust path](../integrate/trust-path.md): how a backend checks the token.
