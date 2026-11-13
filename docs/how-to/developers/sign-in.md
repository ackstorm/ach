# Sign in to ACH

You sign in once per machine, through your organisation's single sign-on.
`ach-cli` keeps the session in a profile and renews it by itself.

You need [`ach-cli` installed](install-ach-cli.md) and the URL of your ACH,
for example `https://ach.example.com`. Your platform team has it.

## Sign in

```bash
ach-cli login https://ach.example.com
```

`login` asks for a profile name (accept `default`), then how to finish:

1. **The browser on this machine.** It opens the sign-in page; when you are
   done the terminal continues by itself.
2. **Another device.** It prints a URL and a code. Open the URL in a browser
   on any device, check that the code matches, and sign in.

Without the URL argument, `login` takes it from `ACH_URL`, or asks.

## Sign in from a machine without a browser

Over SSH or in a container, skip the menu:

```bash
ach-cli login https://ach.example.com --no-browser
```

It prints the URL and the code for another device, and waits.

## Check who you are

```bash
ach-cli whoami
```

| Line | Shows |
|---|---|
| `Profile` | The profile used |
| `URL` | Your ACH |
| `User` | Your email |
| `Auth` | `session` after `login`, or a masked key |
| `Budget` | Your spend and your ceiling |
| `Keys` | Environment keys you hold, and how many you may hold |

`whoami -o json` prints the same as JSON.

## Pick up a new group

When someone adds you to a single sign-on group, run:

```bash
ach-cli refresh
```

It joins the teams your groups map to, without a browser. The Environments of
those teams appear in `ach-cli environment list` a few minutes later.
`refresh` only adds teams.

## Use more than one profile

A profile is a URL plus a credential, stored in `~/.config/ach/config.yaml`.
One is enough unless you work with several ACH installs.

```bash
ach-cli login https://ach.example.com --profile prod   # a second profile
ach-cli profile list                                   # * marks the default
ach-cli profile show prod
ach-cli profile use prod                               # make it the default
ach-cli profile rename prod production
ach-cli profile remove production                      # --force for the default one
```

To use another profile for one command, pass `--profile <name>` or set
`ACH_PROFILE`.

## Sign out

```bash
ach-cli logout
```

The session is removed from the profile; the URL stays. Environment keys you
created keep working until you [revoke them](manage-keys.md).

## Set up a machine that never has a browser

A CI runner or an agent host signs in with an environment key instead of a
session. [Create the key](manage-keys.md) on a machine where you are signed
in, then on the target machine either save it in a profile:

```bash
ach-cli profile add ci --url https://ach.example.com --key ek-…
```

or set two variables and keep no file at all:

```bash
export ACH_URL=https://ach.example.com
export ACH_KEY=ek-…
```

With both variables set, `login`, `logout` and `profile` exit 1: there is no
profile to change.

## Related

- [`ach-cli login`](../../reference/cli.md#ach-cli-login),
  [`profile`](../../reference/cli.md#ach-cli-profile),
  [`whoami`](../../reference/cli.md#ach-cli-whoami) in the command reference.
- [Troubleshoot ach-cli](troubleshoot.md) when a command says you are not
  signed in.
