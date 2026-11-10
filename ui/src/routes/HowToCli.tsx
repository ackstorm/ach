// HowToCli.tsx — the ach-cli guide (nav: Help → ach-cli).
//
// Static content: install, sign in, Environments, environment keys and the local
// package manager. The commands mirror the ach-cli plugin skill
// (plugins/ach-cli/skills/ach-cli/references/); a command or flag change updates
// both. Commands use the canonical `environment` word, never its `env` alias.
//
// The URL to sign in to is this console's own origin: the console is served at
// the platform's base URL, the same one `ach-cli login` takes.

import { useState } from 'react';
import { BookOpen, Download, KeyRound, Layers, LogIn, Package } from 'lucide-react';
import { Link } from 'react-router';

import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { CodeBlock } from '@/components/ui/CodeBlock';
import { EndpointChip, Section, scrollToSection } from '@/routes/HowTo';

const RELEASES = 'https://github.com/ackstorm/ach/releases/download';

const INSTALL_UNIX = `VERSION=$(curl -fsSL https://api.github.com/repos/ackstorm/ach/releases/latest | sed -n 's/.*"tag_name": *"v\\([^"]*\\)".*/\\1/p')
OS=$(uname -s | tr '[:upper:]' '[:lower:]')            # linux or darwin
ARCH=$(uname -m); case "$ARCH" in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; esac
FILE="ach-cli_\${VERSION}_\${OS}_\${ARCH}.tar.gz"
curl -fsSLO "${RELEASES}/v\${VERSION}/\${FILE}"
curl -fsSLO "${RELEASES}/v\${VERSION}/checksums.txt"
grep " \${FILE}\\$" checksums.txt | if command -v sha256sum >/dev/null; then sha256sum -c -; else shasum -a 256 -c -; fi  # must print "OK"
tar -xzf "$FILE" ach-cli
mkdir -p ~/.local/bin && mv ach-cli ~/.local/bin/    # ~/.local/bin must be on the PATH
ach-cli --version`;

const INSTALL_WINDOWS = `$v = (Invoke-RestMethod https://api.github.com/repos/ackstorm/ach/releases/latest).tag_name.TrimStart('v')
Invoke-WebRequest "${RELEASES}/v$v/ach-cli_\${v}_windows_amd64.zip" -OutFile ach-cli.zip
Expand-Archive ach-cli.zip -DestinationPath "$env:LOCALAPPDATA\\ach-cli" -Force
[Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path','User') + ";$env:LOCALAPPDATA\\ach-cli", 'User')
# open a new terminal, then:
ach-cli --version`;

const TOC = [
  { id: 'install', label: 'Install & update' },
  { id: 'login', label: 'Sign in & profiles' },
  { id: 'environments', label: 'Environments' },
  { id: 'keys', label: 'Environment keys' },
  { id: 'local', label: 'Local packages' },
] as const;

// A short explanation followed by its commands.
function Step({ note, caption, code }: { note: string; caption?: string; code: string }) {
  return (
    <div className="flex flex-col gap-2">
      <p className="font-sans text-sm leading-relaxed text-text-secondary">{note}</p>
      <CodeBlock code={code} caption={caption} />
    </div>
  );
}

export function HowToCli() {
  const url = window.location.origin;
  const [os, setOs] = useState('unix');

  return (
    <div className="catalog-page flex flex-col gap-6">
      <div className="flex flex-wrap items-start justify-between gap-5">
        <div className="min-w-0 flex-[1_1_20rem]">
          <h1 className="text-[23px] font-semibold leading-tight tracking-tight text-text-primary">
            ach-cli
          </h1>
          <p className="mt-1 font-sans text-sm text-text-secondary">
            Sign in from the terminal, install Environments into your tools and
            manage the keys your scripts and agents use.
          </p>
        </div>
        <EndpointChip endpoint={url} />
      </div>

      <div className="grid gap-10 lg:grid-cols-[1fr_180px]">
        <aside className="hidden lg:order-2 lg:block">
          <nav aria-label="On this page" className="sticky top-8 flex flex-col gap-1">
            <div className="mb-1 font-mono text-[11px] font-semibold uppercase tracking-wider text-text-primary">
              On this page
            </div>
            {TOC.map((item) => (
              <button
                key={item.id}
                type="button"
                onClick={() => scrollToSection(item.id)}
                className="cursor-pointer rounded-md px-2.5 py-1.5 text-left font-sans text-sm text-text-primary transition-colors hover:bg-primary/5 hover:text-primary"
              >
                {item.label}
              </button>
            ))}
          </nav>
        </aside>

        <div className="flex min-w-0 flex-col gap-12 lg:order-1">
          <Section
            id="install"
            icon={Download}
            title="Install & update"
            sub="One archive per OS and CPU on the GitHub releases page, checked against the published checksums."
          >
            <div className="flex flex-col gap-5">
              <Tabs value={os} onValueChange={setOs}>
                <TabsList variant="line" className="flex-wrap">
                  <TabsTrigger value="unix">Linux & macOS</TabsTrigger>
                  <TabsTrigger value="windows">Windows</TabsTrigger>
                </TabsList>
                <TabsContent value="unix" className="mt-4">
                  <Step
                    note="Downloads the latest release for this machine and installs it under ~/.local/bin. Stop if the checksum line does not print OK."
                    caption="bash"
                    code={INSTALL_UNIX}
                  />
                </TabsContent>
                <TabsContent value="windows" className="mt-4">
                  <Step
                    note="In PowerShell. The build is amd64; it also runs on ARM Windows."
                    caption="powershell"
                    code={INSTALL_WINDOWS}
                  />
                </TabsContent>
              </Tabs>
              <Step
                note="Update in place. Your profiles are untouched."
                code={`ach-cli upgrade                    # the latest release
ach-cli upgrade --version <x.y.z>  # a given release`}
              />
            </div>
          </Section>

          <Section
            id="login"
            icon={LogIn}
            title="Sign in & profiles"
            sub="Single sign-on with your company account, in a browser. No key needed."
          >
            <div className="flex flex-col gap-5">
              <Step
                note="On a desktop a browser opens and the result comes back on its own. On a remote or headless machine (SSH, a container) add --no-browser: it prints a URL and a code to enter on any other device."
                code={`ach-cli login ${url}
ach-cli login ${url} --no-browser
ach-cli whoami      # who you are, your budget and how many keys you may create
ach-cli refresh     # after joining an SSO group: pick up its teams, no browser`}
              />
              <Step
                note="A profile is a URL plus a credential, kept in ~/.config/ach/config.yaml. Any command takes --profile <name> to use another one once."
                code={`ach-cli profile list         # * marks the default
ach-cli profile use <name>   # make it the default
ach-cli logout               # end the session; the URL stays`}
              />
              <Step
                note="With ACH_URL and ACH_KEY set, ach-cli needs no config file at all: the way to run it in CI or an agent box."
                code={`export ACH_URL=${url}
export ACH_KEY=ek-...`}
              />
            </div>
          </Section>

          <Section
            id="environments"
            icon={Layers}
            title="Environments"
            sub="An Environment is what a tool may use: models, MCP servers and agents, plus plugins, skills, prompts and artifacts. Hydrating one writes it into your tool's config."
          >
            <div className="flex flex-col gap-5">
              <Step
                note="See what your teams may use. An Environment you need but do not see is granted by a platform administrator, not by signing in again."
                code={`ach-cli environment list
ach-cli environment describe <environment>   # what one contains
ach-cli environment search <text>            # find a plugin, skill, MCP server… across all of them`}
              />
              <Step
                note="Global install, for the organization's baseline: every project gets it."
                code={`ach-cli environment hydrate <environment> -g --target claude-code`}
              />
              <Step
                note="One folder per Environment: clone creates the folder and hydrates into it, like git clone. Every project under it picks the Environment up. Several tools at once: --target claude-code,opencode."
                code={`cd ~/Projects && ach-cli environment clone <environment> --target claude-code
# ~/Projects/<environment>/<project>/ …`}
              />
              <Step
                note="Keep it up to date. A file you edited is never overwritten silently: hydrate stops with exit code 2, and --force overwrites it."
                code={`cd ~/Projects/<environment> && ach-cli environment hydrate   # every Environment recorded here
ach-cli environment hydrate <environment> --sync           # also remove what the Environment dropped
ach-cli environment hydrate <environment> --dry-run        # preview the diff, write nothing
ach-cli environment status                                 # what is installed here
ach-cli environment uninstall <environment>                # remove what hydrate wrote`}
              />
              <Step
                note="Hydrate and start the tool in one step (claude, codex, gemini, opencode or pi)."
                code={`ach-cli launch claude --environment <environment>
ach-cli launch opencode    # the Environments already in this folder`}
              />
              <p className="font-sans text-sm leading-relaxed text-text-secondary">
                Hydrated MCP servers sign in from inside each tool; see{' '}
                <Link to="/howto" className="font-medium text-primary hover:underline">
                  How-to → MCP servers
                </Link>
                .
              </p>
            </div>
          </Section>

          <Section
            id="keys"
            icon={KeyRound}
            title="Environment keys"
            sub="An ek- key lets something that cannot sign in with a browser (an agent, a CI job, a script) use one Environment. It is secret and long-lived."
          >
            <div className="flex flex-col gap-5">
              <Step
                note="The key is printed once and stored nowhere: put it straight into a secret store."
                code={`ach-cli keys create <environment> --name <what-it-is-for>
ach-cli keys create <environment> --name ci --expires 90d --max-budget 50`}
              />
              <Step
                note="Manage them by name or ekid_ id."
                code={`ach-cli keys list                                # --status all|suspended|expired|invalid|revoked
ach-cli keys budget <name> --max-budget <USD>    # change its spend cap
ach-cli keys suspend <name>                      # pause it; keys resume undoes it
ach-cli keys revoke <name>                       # for good`}
              />
            </div>
          </Section>

          <Section
            id="local"
            icon={Package}
            title="Local packages"
            sub="Install a plugin or skill straight from a git repository, with no Environment and no sign-in. The quick path for trying something; for anything a team relies on, ask for it in an Environment."
          >
            <Step
              note="A source is github:owner/repo[#ref] or git:https://host/path[.git][#ref]. --dry-run on install and uninstall writes nothing."
              code={`ach-cli local repo add github:owner/repo --name <repo>
ach-cli local plugin install <plugin>@<repo> --target claude-code   # -g for the tool's global config
ach-cli local skill install <skill>@<repo> --target codex,opencode
ach-cli local plugin list
ach-cli local plugin update
ach-cli local plugin uninstall <plugin>@<repo>`}
            />
          </Section>

          <p className="flex items-center gap-2 font-sans text-xs text-text-tertiary">
            <BookOpen className="size-3.5" aria-hidden="true" />
            Every command takes --help.
          </p>
        </div>
      </div>
    </div>
  );
}
