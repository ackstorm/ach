# Create and manage environment keys

An environment key (`ek-…`) lets something that cannot sign in with a browser
use ACH: an agent runtime, a CI job, a script. A key works for one
Environment, is long-lived and is a secret.

You need to be [signed in](sign-in.md) and have access to the Environment.

## Create a key

```bash
ach-cli keys create <environment> --name ci-deploy
```

The key is printed once and stored nowhere else. Put it straight into the
secret store of whatever uses it.

| Flag | Effect |
|---|---|
| `--name` | Required. What the key is for; every other command takes this name |
| `--expires 90d` | The key expires after that time. Also takes a duration (`720h`) or a date (`2026-12-31T00:00:00Z`). Cannot be changed later |
| `--max-budget 20` | The key may spend at most that many USD |
| `--budget-duration 30d` | The budget resets after that period. Needs `--max-budget` |
| `-o json` | Prints `{name, id, environment, key, expires_at}` |

Your own budget still applies on top of a key's budget.

## Use a key

Send it in the `x-ach-key` header (`x-api-key` works too):

| For | URL |
|---|---|
| Models, OpenAI-compatible | `https://ach.example.com/v1` |
| An MCP server of the Environment | `https://ach.example.com/mcp/<server>` |

```bash
curl -H "x-ach-key: $ACH_KEY" https://ach.example.com/v1/models
```

`/v1/models` lists chat models; add `?types=embedding` or `?types=all` for
the others.

To run `ach-cli` with a key, pass `--key ek-…`, set `ACH_KEY`, or
[save it in a profile](sign-in.md#set-up-a-machine-that-never-has-a-browser).
With a key, `ach-cli environment hydrate` needs no Environment name.

## List your keys

```bash
ach-cli keys list                          # active keys
ach-cli keys list --status all
ach-cli keys list --environment <environment>
```

| State | Meaning | To use it again |
|---|---|---|
| `active` | Works | |
| `suspended` | You paused it | `ach-cli keys resume` |
| `expired` | Its expiry passed | Create a new key |
| `invalid` | You lost access to its Environment, or your organisation's single sign-on no longer accepts you | It works again when access returns; in the second case, after you run `ach-cli login` |
| `revoked` | Withdrawn for good | Create a new key |

## Suspend, resume, revoke

```bash
ach-cli keys suspend ci-deploy
ach-cli keys resume ci-deploy
ach-cli keys revoke ci-deploy      # asks first; --yes skips the question
```

Each takes the key's name or its `ekid_…` id. A suspension applies within 60
seconds. A revoked key cannot be restored.

## Change a key's budget

```bash
ach-cli keys budget ci-deploy --max-budget 50 --budget-duration 30d
```

The new budget applies after about 10 seconds. The key keeps working.

## The key limit

You may hold a limited number of keys that are not revoked. `ach-cli whoami`
shows held and allowed after `Keys`. At the limit, `keys create` fails with
`403 key_limit_reached`: revoke a key, or ask a platform admin to raise your
limit.

## Related

- [`ach-cli keys`](../../reference/cli.md#ach-cli-keys) in the command
  reference.
- [Keys and budgets](../../explanation/keys-and-budgets.md) explains how the
  budgets of a user, an Environment and a key combine.
