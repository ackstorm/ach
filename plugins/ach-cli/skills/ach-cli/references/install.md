# Install or update ach-cli

`ach-cli` ships as one archive per OS and CPU on the releases page
<https://github.com/ackstorm/ach/releases/latest>:
`ach-cli_<version>_<os>_<arch>.tar.gz` for `linux` and `darwin` (macOS) on `amd64` or
`arm64`, `ach-cli_<version>_windows_amd64.zip` for Windows, plus `checksums.txt`.
`<version>` is the release tag without its leading `v`.

## Linux and macOS

```bash
VERSION=$(curl -fsSL https://api.github.com/repos/ackstorm/ach/releases/latest | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')
OS=$(uname -s | tr '[:upper:]' '[:lower:]')            # linux or darwin
ARCH=$(uname -m); case "$ARCH" in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; esac
FILE="ach-cli_${VERSION}_${OS}_${ARCH}.tar.gz"
curl -fsSLO "https://github.com/ackstorm/ach/releases/download/v${VERSION}/${FILE}"
curl -fsSLO "https://github.com/ackstorm/ach/releases/download/v${VERSION}/checksums.txt"
grep " ${FILE}\$" checksums.txt | if command -v sha256sum >/dev/null; then sha256sum -c -; else shasum -a 256 -c -; fi  # must print "OK"
tar -xzf "$FILE" ach-cli
mkdir -p ~/.local/bin && mv ach-cli ~/.local/bin/    # ~/.local/bin must be on the PATH
ach-cli --version
```

Stop if the checksum line does not print `OK`: the download is corrupt or not the
published one.

## Windows (PowerShell)

The build is amd64; it also runs on ARM Windows.

```powershell
$v = (Invoke-RestMethod https://api.github.com/repos/ackstorm/ach/releases/latest).tag_name.TrimStart('v')
Invoke-WebRequest "https://github.com/ackstorm/ach/releases/download/v$v/ach-cli_${v}_windows_amd64.zip" -OutFile ach-cli.zip
Expand-Archive ach-cli.zip -DestinationPath "$env:LOCALAPPDATA\ach-cli" -Force
[Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path','User') + ";$env:LOCALAPPDATA\ach-cli", 'User')
# open a new terminal, then:
ach-cli --version
```

## Update

Run the same commands again: they install the latest release over the old one. Compare
`ach-cli --version` before and after. The profile and saved keys in
`~/.config/ach/config.yaml` are untouched.
