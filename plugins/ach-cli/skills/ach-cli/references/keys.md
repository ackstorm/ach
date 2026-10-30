# Environment keys (ek-)

An `ek-` key lets something that cannot sign in with a browser (an agent runtime, a CI
job, a script) use ACH within ONE Environment. It is long-lived and secret. Have the
user run `keys create` in their own terminal, so the key never passes through the chat.

```bash
ach-cli keys create <environment> --name <what-it-is-for>          # shown once, saved in the profile under that name
ach-cli keys create <environment> --name ci-deploy --no-save       # CI / a secrets manager: stdout only, not saved
ach-cli keys list                                                   # --status all|suspended|expired|invalid|revoked, --env <env>
ach-cli keys suspend <name|ekid_…>                                  # pause it; keys resume undoes it
ach-cli keys revoke <name|ekid_…>                                   # for good; also deletes the saved copy
```

- `--name` is required: it is how the user and the console tell keys apart.
- `--expires 90d` (or a duration like `720h`, or an RFC 3339 date) sets an expiry.
- `--max-budget <USD>` with optional `--budget-duration 30d` caps what that key can
  spend. The user's own total budget still applies on top.
  `ach-cli keys budget <name> --max-budget <USD>` changes the cap later (about 10 s to
  take effect).
- Each user may hold a limited number of non-revoked keys; `ach-cli whoami` shows
  held / allowed. `403 key_limit_reached` means revoke one or ask an administrator to
  raise the limit.
- A key reads `invalid` in `keys list` when its owner lost access to its Environment. It
  stops working until access comes back.

## Using a key

Store it in the consumer's secret store. Send it as the `x-ach-key` header (or
`x-api-key`) to `<platform URL>/v1` for models (OpenAI-compatible) and
`<platform URL>/mcp/<server>` for MCP. `GET <platform URL>/v1/models` lists chat models;
add `?types=embedding` (any model mode, comma-separated) or `?types=all` for the others.

To run `ach-cli` itself with a key: `--key <saved name|ek-…>` on any command, or set
`ACH_KEY` (with `ACH_URL`, no config file is needed at all). `ach-cli env hydrate`
with a key needs no Environment name: the key already names one.
