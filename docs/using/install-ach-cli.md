# Install or update ach-cli

`ach-cli` is one binary with no dependencies. Each
[release](https://github.com/ackstorm/ach/releases/latest) carries one archive
per platform and a `checksums.txt`:

| Platform | Archive |
|---|---|
| Linux, macOS (`amd64`, `arm64`) | `ach-cli_<version>_<os>_<arch>.tar.gz` |
| Windows (`amd64`) | `ach-cli_<version>_windows_amd64.zip` |

`<version>` is the release tag without its leading `v`; `<os>` is `linux` or
`darwin`.

You need `curl` and `tar` on Linux and macOS, or PowerShell on Windows.

## Linux and macOS

```bash
VERSION=$(curl -fsSL https://api.github.com/repos/ackstorm/ach/releases/latest | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m); case "$ARCH" in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; esac
FILE="ach-cli_${VERSION}_${OS}_${ARCH}.tar.gz"
curl -fsSLO "https://github.com/ackstorm/ach/releases/download/v${VERSION}/${FILE}"
curl -fsSLO "https://github.com/ackstorm/ach/releases/download/v${VERSION}/checksums.txt"
grep " ${FILE}\$" checksums.txt | if command -v sha256sum >/dev/null; then sha256sum -c -; else shasum -a 256 -c -; fi
tar -xzf "$FILE" ach-cli
mkdir -p ~/.local/bin && mv ach-cli ~/.local/bin/
```

The checksum line must print `OK`. If it does not, stop: the download is
corrupt or is not the published file.

`~/.local/bin` must be on your `PATH`.

## Windows

In PowerShell. The build is `amd64`; it also runs on ARM Windows.

```powershell
$v = (Invoke-RestMethod https://api.github.com/repos/ackstorm/ach/releases/latest).tag_name.TrimStart('v')
Invoke-WebRequest "https://github.com/ackstorm/ach/releases/download/v$v/ach-cli_${v}_windows_amd64.zip" -OutFile ach-cli.zip
Expand-Archive ach-cli.zip -DestinationPath "$env:LOCALAPPDATA\ach-cli" -Force
[Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path','User') + ";$env:LOCALAPPDATA\ach-cli", 'User')
```

Open a new terminal so the `PATH` change applies.

## Update

```bash
ach-cli upgrade                    # the latest release
ach-cli upgrade --version 1.1.8    # a given release
```

[`upgrade`](../reference/cli.md#ach-cli-upgrade) downloads the release for
your platform, checks it against `checksums.txt` and replaces the running
binary. Your profiles in `~/.config/ach/config.yaml` are not touched.

| Message | Do |
|---|---|
| `permission denied` | You cannot write to the binary's directory. Run it with `sudo`, or install under `~/.local/bin` |
| `unknown command "upgrade"` | This `ach-cli` predates the command. Run the install commands above again |

## Verify

```bash
ach-cli --version
```

It prints the version you installed. Next: [sign in](sign-in.md).
