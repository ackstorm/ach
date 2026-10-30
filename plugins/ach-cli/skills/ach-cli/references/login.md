# Sign in, profiles, tokens

## Sign in

```bash
ach-cli login            # URL from the argument, else ACH_URL, else a prompt
ach-cli whoami           # who you are, your budget and how many keys you may create
```

The URL is the platform's base URL (`https://…`). Pass it as the argument when you know
it, or let `ACH_URL` supply it. Single sign-on happens in a browser: on a desktop,
`ach-cli login` opens one and the result comes back on its own. On a remote or headless
machine (SSH, a container, no browser) add `--no-browser`: it prints a URL and a code to
enter in a browser on any other device.

### Running the login yourself

`ach-cli login` waits up to 10 minutes for the browser, so in the foreground your shell
tool shows nothing until it times out. Start it in the background, read the link from
its log, and hand it to the user:

```bash
LOG="${TMPDIR:-/tmp}/ach-login.log"
nohup ach-cli login "$ACH_URL" --no-browser --profile default </dev/null >"$LOG" 2>&1 &
for i in $(seq 1 20); do grep -q 'Waiting for you' "$LOG" && break; sleep 1; done; cat "$LOG"
```

Replace `"$ACH_URL"` with the URL if the variable is not set. The URL, `--profile` and
`--no-browser` keep `ach-cli` from prompting on a stdin it does not have; it exits by
itself when the code expires. Give the user the URL and code from the output, then wait
for them to say they signed in (do not poll). Check with `ach-cli whoami`; if it is not
signed in yet, `cat "${TMPDIR:-/tmp}/ach-login.log"` shows why (an expired code: run it
again). On Windows, run it from Git Bash.

## Profiles

A profile is a URL plus a credential, in `~/.config/ach/config.yaml`. Most users have
one, `default`.

```bash
ach-cli profile list                 # * marks the default
ach-cli profile show [name]          # its URL and saved keys (masked; --reveal prints them)
ach-cli profile use <name>           # make it the default
ach-cli profile rename <old> <new>
ach-cli profile remove <name>        # --force for the default one
ach-cli logout                       # end the session; URL and saved keys stay
```

Any command takes `--profile <name>` (or `ACH_PROFILE`) to use another profile once.

### A machine that cannot open a browser for good (CI, an agent box)

Make an environment key on a signed-in machine (see [keys](keys.md),
`--no-save`), then on the target machine:

```bash
ach-cli profile add ci --url <platform URL> --key ek-…
```

Or skip the config file entirely: with both `ACH_URL` and `ACH_KEY` set, `ach-cli` runs
in "synthetic mode" from those two variables alone. In that mode `login`, `logout` and
`profile` exit 1 on purpose.

## Tokens for other tools

`ach-cli token` prints a fresh access token for the active profile (or its key for a key
profile). It is meant as a credential helper that other tools call; never print it to
the chat yourself.
