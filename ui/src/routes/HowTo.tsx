// HowTo.tsx — the "How-to" onboarding guide (nav: KEYS | STATS | HOW-TO).
//
// Answers the "I just logged in and minted a key — now what?" question for a
// corporate user. Three sections, all STATIC (no backend, no fetch):
//
//   §1 Quickstart  — mint a key (link to the Keys tab) + a copy-paste `curl` to
//                    the `ackstorm.fast` model alias against the user's gateway.
//   §2 Editors/CLI — tabbed setup for Claude Code / Gemini / opencode / codex /
//                    GitHub Copilot / Qwen Code (env exports, except the JSON-config
//                    tools); each links the authoritative LiteLLM guide where one exists.
//   §3 MCP servers — connect MCP clients to the gateway's /mcp endpoint (same key);
//                    optional x-mcp-servers header / group URL to scope the tools.
//   §4 No terminal — chat-UI cards (ACKstorm Chat, hosted; openwork, coming soon).
//
// PERSONALIZATION (no rebuild): the gateway base URL is read live from the
// session (`me.endpoint` === settings.api_public_url, e.g. https://api.<domain>);
// the hosted-chat URL is derived by swapping the `api.` host label for `chat.`
// (the deployment's convention — chat.<domain>). Falls back to a neutral
// placeholder host when the endpoint is absent/unparseable.
//
// SECURITY (T-10-01 / info disclosure): this page NEVER renders a real key. The
// curl/exports show the literal `sk-...` placeholder; minting (and the one-time
// `sk-` reveal) happens only on the Keys tab. Copy buttons write exactly the
// shown text on an explicit click (useCopyFeedback) — no auto-copy, no logging.

import { useState } from 'react';
import {
  AlertCircle,
  ArrowRight,
  Check,
  Copy,
  ExternalLink,
  KeyRound,
  MessageSquare,
  Server,
  Terminal,
} from 'lucide-react';
import { useNavigate } from 'react-router';

import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { useCopyFeedback } from '@/hooks/use-copy-feedback';
import { useHasDefaultKey } from '@/hooks/use-keys';
import { useModels } from '@/hooks/use-models';
import { TAB_PILL, TAB_PILL_LIST } from '@/lib/ui';
import { cn } from '@/lib/utils';
import { deriveSubdomainUrl } from '@/lib/urls';
import { useConfigStore } from '@/stores/config';
import { useSessionStore } from '@/stores/session';

// The literal key placeholder shown everywhere a real `sk-` would go. The real
// key is shown ONCE, at creation, on the Keys tab — never on this page.
const KEY_PLACEHOLDER = 'sk-...';

// Featured model alias for the quickstart (locked copy — the standard alias).
const MODEL_ALIAS = 'ackstorm.fast';

// LiteLLM auth header (the deployment's standard: a Bearer value under the
// custom header name, not the bare Authorization header).
const AUTH_HEADER = 'x-litellm-api-key';

// Fallback host shown before the session endpoint resolves / when unparseable.
const FALLBACK_API_BASE = 'https://api.your-domain.example';

// `deriveSubdomainUrl` now lives in @/lib/urls (shared with the CHAT nav button).

// ── TOC ──────────────────────────────────────────────────────────────────────
// Sticky in-page nav. NOTE: this app is a HASH router (#/howto), so the URL hash
// belongs to react-router — we must NOT use `<a href="#id">` anchors (that would
// hijack the route hash). Instead each item scrolls its section into view.
const TOC = [
  { id: 'quickstart', label: 'Quickstart' },
  { id: 'tools', label: 'Editors & CLIs' },
  { id: 'mcp', label: 'MCP servers' },
  { id: 'chat', label: 'No terminal?' },
  { id: 'troubleshooting', label: 'Troubleshooting' },
] as const;

function scrollToSection(id: string): void {
  document
    .getElementById(id)
    ?.scrollIntoView({ behavior: 'smooth', block: 'start' });
}

// ── CodeBlock ──────────────────────────────────────────────────────────────—─
// A dark, horizontally-scrollable code box with a copy button. `code` is shown
// verbatim (mono, pre-wrapped); the copy writes exactly that string. An optional
// `caption` renders a thin header strip (e.g. a shell/file label).
function CodeBlock({ code, caption }: { code: string; caption?: string }) {
  const { copied, copy } = useCopyFeedback();
  return (
    <div className="overflow-hidden rounded-xl border border-border bg-background">
      <div className="flex items-center justify-between gap-3 border-b border-border bg-surface px-3 py-1.5">
        <span className="font-mono text-[11px] font-semibold uppercase tracking-wider text-text-tertiary">
          {caption ?? 'shell'}
        </span>
        <button
          type="button"
          onClick={() => void copy(code)}
          className={cn(
            'inline-flex cursor-pointer items-center gap-1.5 rounded-md border px-2 py-1 font-mono text-[11px] font-semibold lowercase tracking-wide transition-colors',
            copied
              ? 'border-primary bg-primary/15 text-primary'
              : 'border-border text-text-tertiary hover:border-primary hover:text-primary'
          )}
        >
          {copied ? (
            <Check className="size-3" aria-hidden="true" />
          ) : (
            <Copy className="size-3" aria-hidden="true" />
          )}
          {copied ? 'copied!' : 'copy'}
        </button>
      </div>
      <pre className="overflow-x-auto px-4 py-3 font-mono text-[13px] leading-relaxed text-text-primary">
        <code>{code}</code>
      </pre>
    </div>
  );
}

// ── Section ──────────────────────────────────────────────────────────────────
// A titled content block with a scroll anchor (`id`) the TOC targets. `scroll-mt`
// keeps the heading clear of the top edge when scrolled into view.
function Section({
  id,
  icon: Icon,
  title,
  sub,
  children,
}: {
  id: string;
  icon: typeof Terminal;
  title: string;
  sub: string;
  children: React.ReactNode;
}) {
  return (
    <section id={id} className="scroll-mt-8">
      <div className="flex items-center gap-2.5">
        <span className="inline-flex size-7 shrink-0 items-center justify-center rounded-lg border border-border">
          <Icon className="size-4 text-primary" aria-hidden="true" />
        </span>
        <h2 className="font-sans text-lg font-semibold leading-tight text-text-primary">
          {title}
        </h2>
      </div>
      <p className="mt-2 font-sans text-sm leading-relaxed text-text-secondary">
        {sub}
      </p>
      <div className="mt-5">{children}</div>
    </section>
  );
}

// A small numbered step marker (the quickstart 1·2 sequence).
function StepBadge({ n }: { n: number }) {
  return (
    <span className="inline-flex size-5 shrink-0 items-center justify-center rounded-full border border-primary/40 bg-primary/10 font-mono text-[11px] font-semibold text-primary">
      {n}
    </span>
  );
}

export function HowTo() {
  const me = useSessionStore((s) => s.me);
  const config = useConfigStore((s) => s.config);
  const navigate = useNavigate();

  // Model picker: personalize the quickstart snippets with a real alias from the
  // catalog (gated on a default key, like the other per-user reads). Defaults to
  // the standard MODEL_ALIAS; the picked value is always kept as a valid option.
  const hasDefaultKey = useHasDefaultKey();
  const modelsQuery = useModels(hasDefaultKey);
  const catalogAliases = (modelsQuery.data?.models ?? [])
    .map((m) => m.name)
    .filter((n): n is string => typeof n === 'string' && n.length > 0);
  const [pickedModel, setPickedModel] = useState<string>(MODEL_ALIAS);
  const modelOptions = Array.from(new Set([pickedModel, ...catalogAliases]));

  // Live gateway base (api_public_url). Falls back to a neutral placeholder so
  // the page reads sensibly before the session resolves.
  const apiBase = me?.endpoint || FALLBACK_API_BASE;
  const chatUrl =
    config.chat_public_url || deriveSubdomainUrl(me?.endpoint, 'chat');
  const brandShort = config.brand_short || 'LiteLLM';

  // The quickstart curl — the featured first call. Header is the deployment's
  // standard `x-litellm-api-key: Bearer sk-...` form; the key is the placeholder.
  const curlSnippet = `curl ${apiBase}/v1/chat/completions \\
  -H "${AUTH_HEADER}: Bearer ${KEY_PLACEHOLDER}" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${pickedModel}",
    "messages": [{ "role": "user", "content": "Hello!" }]
  }'`;

  // Same first call from the official OpenAI SDKs (the gateway is OpenAI-compatible).
  const pySnippet = `from openai import OpenAI

client = OpenAI(api_key="${KEY_PLACEHOLDER}", base_url="${apiBase}/v1")

resp = client.chat.completions.create(
    model="${pickedModel}",
    messages=[{"role": "user", "content": "Hello!"}],
)
print(resp.choices[0].message.content)`;

  const tsSnippet = `import OpenAI from "openai";

const client = new OpenAI({
  apiKey: "${KEY_PLACEHOLDER}",
  baseURL: "${apiBase}/v1",
});

const resp = await client.chat.completions.create({
  model: "${pickedModel}",
  messages: [{ role: "user", content: "Hello!" }],
});
console.log(resp.choices[0].message.content);`;

  const expectedResponse = `{
  "choices": [
    { "message": { "role": "assistant", "content": "Hello!" } }
  ]
}`;

  // ── MCP gateway ────────────────────────────────────────────────────────────────
  // LiteLLM exposes an MCP gateway on the SAME host, under /mcp, authenticated with
  // the SAME `x-litellm-api-key` Bearer key. An optional `x-mcp-servers` header (or
  // a /mcp/<group> URL) scopes the exposed tools to named servers and/or groups.
  const mcpUrl = `${apiBase}/mcp`;
  const mcpConfig = `{
  "mcpServers": {
    "litellm": {
      "url": "${mcpUrl}",
      "headers": {
        "${AUTH_HEADER}": "Bearer ${KEY_PLACEHOLDER}"
      }
    }
  }
}`;
  const mcpFilteredConfig = `{
  "mcpServers": {
    "litellm": {
      "url": "${mcpUrl}",
      "headers": {
        "${AUTH_HEADER}": "Bearer ${KEY_PLACEHOLDER}",
        "x-mcp-servers": "Zapier_Gmail,dev-group"
      }
    }
  }
}`;
  // Quick smoke test via the MCP REST API — list/call tools with curl, no LLM.
  const mcpCurl = `# List the MCP tools you can access
curl -s ${apiBase}/mcp-rest/tools/list \\
  -H "${AUTH_HEADER}: Bearer ${KEY_PLACEHOLDER}" | jq .

# Call a tool (server_id + tool name + arguments)
curl -s -X POST ${apiBase}/mcp-rest/tools/call \\
  -H "${AUTH_HEADER}: Bearer ${KEY_PLACEHOLDER}" \\
  -H "Content-Type: application/json" \\
  -d '{
    "server_id": "Zapier_Gmail",
    "name": "getProfile",
    "arguments": {}
  }' | jq .`;

  // ── Editor / CLI setup ───────────────────────────────────────────────────────
  // Live setup for Claude Code, Gemini CLI, opencode, and codex. The base URL is
  // the user's live gateway (apiBase); the key is the `sk-...` placeholder (mint
  // it on Keys). `caption` overrides the code-box label (e.g. a config-file path);
  // `note` adds a one-line instruction; `guide` links the authoritative doc.
  // Tools are grouped by family (top-level tab). A family with more than one
  // variant (e.g. OpenCode → Gemini / OpenAI, Claude Code → API / Pro·Max)
  // renders a second row of pill sub-tabs; single-variant families render the
  // body directly. `subLabel` is the sub-tab label within a family.
  type ToolVariant = {
    id: string;
    subLabel?: string;
    ready: boolean;
    code: string;
    caption?: string;
    note?: string;
    guide?: { url: string; label: string };
    // Optional trailing note with its own copyable snippet — a caveat (e.g.
    // disabling Anthropic server-side tools that can't run through the gateway)
    // or a follow-up config file (e.g. pi's settings.json next to models.json).
    caveat?: { note: string; caption: string; code: string };
  };
  const TOOL_GROUPS: {
    id: string;
    label: string;
    // `false` keeps the entry in source (deprecated / unmaintained setups stay
    // documented here) but hides it from the page — see VISIBLE_TOOLS below.
    enabled?: boolean;
    variants: ToolVariant[];
  }[] = [
    {
      id: 'opencode',
      label: 'OpenCode',
      variants: [
        {
          id: 'opencode-gemini',
          subLabel: 'Gemini',
          ready: true,
          caption: '~/.config/opencode/opencode.json',
          // OpenCode can instead use its native `google` provider against the gateway's
          // Gemini-compatible passthrough (/gemini/v1beta). Model names must match the
          // Gemini models your gateway exposes. apiKey is read from the LITELLM_API_KEY
          // env var (opencode `{env:...}` interpolation), same as Codex.
          code: `{
  "$schema": "https://opencode.ai/config.json",
  "enabled_providers": ["google"],
  "provider": {
    "google": {
      "options": {
        "baseURL": "${apiBase}/gemini/v1beta",
        "apiKey": "{env:LITELLM_API_KEY}",
        "timeout": 600000
      }
    }
  },
  "model": "google/gemini-flash-latest",
  "small_model": "google/gemini-flash-lite-latest"
}`,
          note: 'Export the key referenced by `{env:LITELLM_API_KEY}` first: `export LITELLM_API_KEY="sk-..."`, then run `opencode`. Model names must match the Gemini models your gateway exposes.',
          guide: {
            url: 'https://docs.litellm.ai/docs/tutorials/opencode_integration',
            label: 'OpenCode + LiteLLM guide',
          },
        },
        {
          id: 'opencode-openai',
          subLabel: 'OpenAI',
          ready: true,
          caption: '~/.config/opencode/opencode.json',
          // opencode is configured by a JSON file: an OpenAI-compatible provider pointed
          // at the gateway. The model keys MUST match LiteLLM aliases. apiKey is read from
          // the LITELLM_API_KEY env var (opencode `{env:...}` interpolation), same as Codex.
          code: `{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "litellm": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "LiteLLM",
      "options": {
        "baseURL": "${apiBase}/v1",
        "apiKey": "{env:LITELLM_API_KEY}"
      },
      "models": {
        "ackstorm.fast": { "name": "ACKstorm Fast" },
        "ackstorm.smart": { "name": "ACKstorm Smart" }
      }
    }
  }
}`,
          note: 'Export the key referenced by `{env:LITELLM_API_KEY}` first: `export LITELLM_API_KEY="sk-..."`, then run `opencode` and pick a LiteLLM model with `/models`.',
          guide: {
            url: 'https://docs.litellm.ai/docs/tutorials/opencode_integration',
            label: 'OpenCode + LiteLLM guide',
          },
        },
      ],
    },
    {
      id: 'pi',
      label: 'Pi agent',
      variants: [
        {
          id: 'pi',
          ready: true,
          caption: '~/.pi/agent/models.json',
          // Pi is configured by JSON, not env vars: providers keyed by name, each
          // pointing at the gateway. `$LITELLM_API_KEY` is pi's own env
          // interpolation, so the key never lands in the file.
          code: `{
  "providers": {
    "google": {
      "api": "google-generative-ai",
      "baseUrl": "${apiBase}/v1beta",
      "apiKey": "$LITELLM_API_KEY"
    },
    "litellm": {
      "api": "openai-completions",
      "baseUrl": "${apiBase}/v1",
      "apiKey": "$LITELLM_API_KEY",
      "models": [
        {
          "id": "ackstorm.fast",
          "name": "ACKstorm Fast",
          "reasoning": false,
          "input": ["text", "image"]
        },
        {
          "id": "ackstorm.smart",
          "name": "ACKstorm Smart",
          "reasoning": true,
          "input": ["text", "image"]
        },
        {
          "id": "gemini-flash-latest",
          "name": "Gemini Flash Latest",
          "reasoning": true,
          "input": ["text", "image"]
        }
      ]
    }
  }
}`,
          note: 'Install with npm install -g --ignore-scripts @earendil-works/pi-coding-agent (or curl -fsSL https://pi.dev/install.sh | sh), export the key pi interpolates — export LITELLM_API_KEY="sk-..." — then run pi. An openai-completions provider needs an explicit models array: pi ships no catalog for custom gateways, so add one entry per alias you use. The google provider needs no list — it reuses pi’s built-in Gemini catalog against the gateway.',
          guide: {
            url: 'https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/providers.md',
            label: 'Pi providers doc',
          },
          caveat: {
            note: 'Pick the default and which models show up in the model picker. Entries in enabledModels are namespaced provider/model-id:',
            caption: '~/.pi/agent/settings.json',
            code: `{
  "defaultProvider": "litellm",
  "defaultModel": "ackstorm.fast",
  "enabledModels": [
    "litellm/ackstorm.fast",
    "litellm/ackstorm.smart",
    "litellm/gemini-flash-latest",
    "google/gemini-flash-latest"
  ]
}`,
          },
        },
      ],
    },
    {
      id: 'claude',
      label: 'Claude Code',
      variants: [
        {
          id: 'claude-api',
          subLabel: 'API',
          ready: true,
          code: `# Claude Code → LiteLLM
export ANTHROPIC_BASE_URL="${apiBase}"
export ANTHROPIC_AUTH_TOKEN=${KEY_PLACEHOLDER}
export ANTHROPIC_MODEL="ackstorm.smart"
export ANTHROPIC_DEFAULT_OPUS_MODEL="ackstorm.smart"
export ANTHROPIC_DEFAULT_SONNET_MODEL="ackstorm.fast"
export ANTHROPIC_DEFAULT_HAIKU_MODEL="ackstorm.lite"
export CLAUDE_CODE_SUBAGENT_MODEL="ackstorm.fast"

claude`,
          note: 'Bills against your gateway key — no Claude subscription required. Models are LiteLLM aliases.',
          guide: {
            url: 'https://docs.litellm.ai/docs/anthropic_completion',
            label: 'Claude Code + LiteLLM guide',
          },
          caveat: {
            note: "WebSearch is an Anthropic server-side tool — it only runs on Anthropic's own API, so through the gateway it fails. You don't lose web search, though: plug in an external search MCP server (see the MCP servers section below) to add it back. Disable the built-in tool in Claude Code settings (~/.claude/settings.json for all projects, or .claude/settings.json in a project); add WebFetch too if it also errors:",
            caption: '~/.claude/settings.json',
            code: `{
  "permissions": {
    "deny": ["WebSearch"]
  }
}`,
          },
        },
        {
          id: 'claude-sub',
          subLabel: 'Pro/Max',
          ready: true,
          // MAX/Pro subscription flow (NOT an API key): ANTHROPIC_API_KEY is left empty
          // so Claude Code authenticates with your Claude subscription OAuth token. The
          // gateway key rides in ANTHROPIC_CUSTOM_HEADERS (x-litellm-api-key) purely for
          // budget/limit tracking. ANTHROPIC_MODEL must be a real model your gateway maps.
          code: `# Claude Code → LiteLLM (MAX/Pro subscription)
export ANTHROPIC_API_KEY=""
export ANTHROPIC_BASE_URL="${apiBase}"
export ANTHROPIC_CUSTOM_HEADERS="${AUTH_HEADER}: Bearer ${KEY_PLACEHOLDER}"
export ANTHROPIC_MODEL="claude-opus-4-8"

claude`,
          note: 'Uses your Claude MAX/Pro subscription, not an API key. On first run pick "Claude account with subscription" and authorize in the browser — Claude Code sends its OAuth token, while the gateway key only tracks budget/limits.',
          guide: {
            url: 'https://docs.litellm.ai/docs/tutorials/claude_code_max_subscription',
            label: 'Claude Code subscription + LiteLLM guide',
          },
        },
      ],
    },
    {
      id: 'codex',
      label: 'Codex',
      variants: [
        {
          id: 'codex',
          ready: true,
          caption: '~/.codex/config.toml',
          // Codex is configured by a TOML provider (NOT endpoint env vars): a named
          // OpenAI-compatible provider pointed at the gateway. The key is read from the
          // env var named by `env_key`, so export it before running `codex`.
          code: `model = "ackstorm.router"
model_provider = "ackstorm"
model_reasoning_effort = "medium"

[model_providers.ackstorm]
name = "ACKstorm"
base_url = "${apiBase}/v1"
env_key = "LITELLM_API_KEY"
wire_api = "responses"
supports_websockets = false`,
          note: 'Export the key named by env_key first: `export LITELLM_API_KEY="sk-..."`, then run `codex`. Fallback (no config file): `export OPENAI_API_KEY=… OPENAI_BASE_URL=…/v1` then `codex --model …`.',
          guide: {
            url: 'https://openrouter.ai/docs/cookbook/coding-agents/codex-cli',
            label: 'Codex CLI provider config',
          },
        },
      ],
    },
    {
      id: 'qwen',
      label: 'Qwen Code',
      variants: [
        {
          id: 'qwen',
          ready: true,
          code: `# Qwen Code CLI → LiteLLM (OpenAI-compatible)
export OPENAI_BASE_URL="${apiBase}/v1"
export OPENAI_API_KEY="${KEY_PLACEHOLDER}"
export OPENAI_MODEL="${MODEL_ALIAS}"

qwen`,
          guide: {
            url: 'https://docs.litellm.ai/docs/tutorials/litellm_qwen_code_cli',
            label: 'Qwen Code + LiteLLM guide',
          },
        },
      ],
    },
    // Below this line: hidden from the page, kept for reference.
    {
      id: 'gemini',
      label: 'Gemini CLI',
      // Deprecated upstream — use the Pi agent `google` provider instead.
      enabled: false,
      variants: [
        {
          id: 'gemini',
          ready: true,
          code: `# Gemini CLI → LiteLLM
export GOOGLE_GEMINI_BASE_URL=${apiBase}/gemini
export GEMINI_BASE_URL=${apiBase}/gemini/v1beta
export GEMINI_API_KEY=${KEY_PLACEHOLDER}

gemini`,
        },
      ],
    },
    {
      id: 'copilot',
      label: 'GitHub Copilot',
      enabled: false,
      variants: [
        {
          id: 'copilot',
          ready: true,
          caption: 'VS Code settings.json',
          // GitHub Copilot (VS Code) is pointed at the gateway by overriding its proxy
          // URL in settings.json; reload the window afterwards.
          code: `{
  "github.copilot.advanced": {
    "debug.overrideProxyUrl": "${apiBase}",
    "debug.testOverrideProxyUrl": "${apiBase}"
  }
}`,
          note: 'Reload VS Code after saving. Authenticate with your gateway key when prompted — see the guide for model + auth details.',
          guide: {
            url: 'https://docs.litellm.ai/docs/tutorials/github_copilot_integration',
            label: 'GitHub Copilot + LiteLLM guide',
          },
        },
      ],
    },
  ];
  const VISIBLE_TOOLS = TOOL_GROUPS.filter((g) => g.enabled !== false);
  const [toolGroup, setToolGroup] = useState<string>('opencode');
  const [toolVariant, setToolVariant] = useState<Record<string, string>>(() =>
    Object.fromEntries(TOOL_GROUPS.map((g) => [g.id, g.variants[0].id])),
  );

  // Body of one tool variant: the config code box plus its note / guide link.
  // Reused for both single-variant families and each pill sub-tab.
  const renderToolBody = (v: ToolVariant, groupLabel: string) => (
    <>
      <CodeBlock
        code={v.code}
        caption={
          v.caption ??
          `${groupLabel}${v.subLabel ? ` · ${v.subLabel}` : ''} · setup`
        }
      />
      {v.note && (
        <p className="mt-2 font-sans text-xs leading-relaxed text-text-secondary">
          {v.note}
        </p>
      )}
      {v.guide && (
        <a
          href={v.guide.url}
          target="_blank"
          rel="noopener noreferrer"
          className="mt-2 inline-flex items-center gap-1 font-sans text-xs font-medium text-primary hover:underline"
        >
          {v.guide.label}
          <ExternalLink className="size-3" aria-hidden="true" />
        </a>
      )}
      {v.caveat && (
        <div className="mt-4 rounded-lg border border-border bg-surface/50 p-3">
          <p className="mb-2 font-sans text-xs leading-relaxed text-text-secondary">
            {v.caveat.note}
          </p>
          <CodeBlock code={v.caveat.code} caption={v.caveat.caption} />
        </div>
      )}
      {!v.ready && (
        <p className="mt-2 font-sans text-xs text-text-secondary">
          Placeholder — tested {v.subLabel ?? groupLabel} values land here soon.
        </p>
      )}
    </>
  );
  const [mcpTab, setMcpTab] = useState<string>('access');

  return (
    <div className="flex flex-col gap-8">
      {/* Page header */}
      <div>
        <h1 className="font-sans text-2xl font-semibold leading-snug text-text-primary">
          How-to
        </h1>
        <p className="mt-1 font-sans text-sm text-text-secondary">
          You have a key — here is how to make your first call to{' '}
          <span className="font-mono text-text-primary">{brandShort}</span>,
          wire up your editor, or skip the terminal entirely.
        </p>
      </div>

      <div className="grid gap-10 lg:grid-cols-[1fr_180px]">
        {/* Sticky in-page TOC (desktop only) — placed on the RIGHT via order
            (DOM order keeps it readable). Buttons, not hash anchors — the hash
            belongs to the router. */}
        <aside className="hidden lg:order-2 lg:block">
          <nav
            aria-label="On this page"
            className="sticky top-8 flex flex-col gap-1"
          >
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

        {/* Content column */}
        <div className="flex min-w-0 flex-col gap-12 lg:order-1">
          {/* §1 Quickstart */}
          <Section
            id="quickstart"
            icon={Terminal}
            title="Quickstart"
            sub="Two steps to your first response: mint a key, then call the gateway."
          >
            <div className="flex flex-col gap-5">
              {/* Step 1 — mint a key */}
              <div className="flex gap-3">
                <StepBadge n={1} />
                <div className="min-w-0 flex-1">
                  <div className="font-sans text-sm font-medium text-text-primary">
                    Mint a virtual key
                  </div>
                  <p className="mt-1 font-sans text-sm leading-relaxed text-text-secondary">
                    Create one on the{' '}
                    <button
                      type="button"
                      onClick={() => navigate('/')}
                      className="inline-flex cursor-pointer items-center gap-1 font-medium text-primary hover:underline"
                    >
                      <KeyRound className="size-3.5" aria-hidden="true" />
                      Keys
                    </button>{' '}
                    tab. The{' '}
                    <span className="font-mono text-text-primary">sk-</span> value
                    is shown <span className="text-text-primary">once</span>, at
                    creation — copy it then; it is never displayed again.
                  </p>
                </div>
              </div>

              {/* Step 2 — call the gateway */}
              <div className="flex gap-3">
                <StepBadge n={2} />
                <div className="min-w-0 flex-1">
                  <div className="font-sans text-sm font-medium text-text-primary">
                    Call the gateway
                  </div>
                  <p className="mt-1 mb-3 font-sans text-sm leading-relaxed text-text-secondary">
                    Swap{' '}
                    <span className="font-mono text-text-primary">
                      {KEY_PLACEHOLDER}
                    </span>{' '}
                    for your key and run it. The endpoint is OpenAI-compatible —{' '}
                    <span className="font-mono text-text-primary">
                      {pickedModel}
                    </span>{' '}
                    is a standard model alias.
                  </p>
                  <label className="mb-3 flex items-center gap-2 font-sans text-xs text-text-secondary">
                    Model
                    <select
                      aria-label="Model"
                      value={pickedModel}
                      onChange={(e) => setPickedModel(e.target.value)}
                      className="cursor-pointer rounded-md border border-border bg-transparent px-2 py-1 font-mono text-xs text-text-primary outline-none focus-visible:border-primary"
                    >
                      {modelOptions.map((alias) => (
                        <option key={alias} value={alias}>
                          {alias}
                        </option>
                      ))}
                    </select>
                  </label>
                  <Tabs defaultValue="curl">
                    <TabsList variant="line" className="flex-wrap">
                      <TabsTrigger value="curl">cURL</TabsTrigger>
                      <TabsTrigger value="python">Python</TabsTrigger>
                      <TabsTrigger value="typescript">TypeScript</TabsTrigger>
                    </TabsList>
                    <TabsContent value="curl" className="mt-3">
                      <CodeBlock code={curlSnippet} caption="curl" />
                    </TabsContent>
                    <TabsContent value="python" className="mt-3">
                      <CodeBlock code={pySnippet} caption="python" />
                    </TabsContent>
                    <TabsContent value="typescript" className="mt-3">
                      <CodeBlock code={tsSnippet} caption="typescript" />
                    </TabsContent>
                  </Tabs>
                  <p className="mt-4 mb-2 font-sans text-sm text-text-secondary">
                    A successful call returns:
                  </p>
                  <CodeBlock code={expectedResponse} caption="200 OK" />
                </div>
              </div>
            </div>
          </Section>

          {/* §2 Editors & CLIs */}
          <Section
            id="tools"
            icon={ArrowRight}
            title="Editors & CLIs"
            sub="Point your AI coding tool at the gateway. Export the variables, then run the tool as usual."
          >
            <Tabs value={toolGroup} onValueChange={setToolGroup}>
              <TabsList variant="line" className="flex-wrap">
                {VISIBLE_TOOLS.map((g) => (
                  <TabsTrigger key={g.id} value={g.id}>
                    {g.label}
                  </TabsTrigger>
                ))}
              </TabsList>
              {VISIBLE_TOOLS.map((g) => (
                <TabsContent key={g.id} value={g.id} className="mt-4">
                  {g.variants.length > 1 ? (
                    <Tabs
                      value={toolVariant[g.id]}
                      onValueChange={(v) =>
                        setToolVariant((prev) => ({ ...prev, [g.id]: v }))
                      }
                    >
                      <TabsList className={TAB_PILL_LIST}>
                        {g.variants.map((v) => (
                          <TabsTrigger key={v.id} value={v.id} className={TAB_PILL}>
                            {v.subLabel}
                          </TabsTrigger>
                        ))}
                      </TabsList>
                      {g.variants.map((v) => (
                        <TabsContent key={v.id} value={v.id} className="mt-4">
                          {renderToolBody(v, g.label)}
                        </TabsContent>
                      ))}
                    </Tabs>
                  ) : (
                    renderToolBody(g.variants[0], g.label)
                  )}
                </TabsContent>
              ))}
            </Tabs>
            <p className="mt-3 font-sans text-xs leading-relaxed text-text-secondary">
              Swap {KEY_PLACEHOLDER} for your key (mint it on the Keys tab). The
              base URL is your live gateway; only the key differs per user.
            </p>
            <p className="mt-2 font-mono text-xs leading-relaxed text-text-tertiary">
              Tools that read OpenAI's env vars work too:{' '}
              <span className="text-text-secondary">export OPENAI_API_KEY=$LITELLM_API_KEY</span>{' '}
              and <span className="text-text-secondary">export OPENAI_BASE_URL={`${apiBase}/v1`}</span>.
            </p>
          </Section>

          {/* §3 MCP servers */}
          <Section
            id="mcp"
            icon={Server}
            title="MCP servers"
            sub="Give MCP-capable clients (Cursor, Claude Desktop, …) access to the gateway's tool servers. The MCP endpoint lives on the same gateway host, under /mcp, and uses your same virtual key."
          >
            <Tabs value={mcpTab} onValueChange={setMcpTab}>
              <TabsList variant="line" className="flex-wrap">
                <TabsTrigger value="access">MCP Access</TabsTrigger>
                <TabsTrigger value="group">MCP Group access</TabsTrigger>
                <TabsTrigger value="curl">Try with curl</TabsTrigger>
              </TabsList>

              <TabsContent value="access" className="mt-4">
                <p className="mb-3 font-sans text-sm leading-relaxed text-text-secondary">
                  Every tool you can reach. URL{' '}
                  <span className="font-mono text-text-primary">{mcpUrl}</span>,
                  auth header{' '}
                  <span className="font-mono text-text-primary">
                    {AUTH_HEADER}
                  </span>{' '}
                  (the same Bearer key as chat).
                </p>
                <Tabs defaultValue="cursor">
                  <TabsList className={TAB_PILL_LIST}>
                    <TabsTrigger value="cursor" className={TAB_PILL}>Cursor</TabsTrigger>
                    <TabsTrigger value="claude" className={TAB_PILL}>Claude Desktop</TabsTrigger>
                    <TabsTrigger value="vscode" className={TAB_PILL}>VS Code</TabsTrigger>
                    <TabsTrigger value="generic" className={TAB_PILL}>Generic JSON</TabsTrigger>
                  </TabsList>
                  {[
                    ['cursor', '~/.cursor/mcp.json'],
                    ['claude', '~/Library/Application Support/Claude/claude_desktop_config.json'],
                    ['vscode', '.vscode/mcp.json (workspace)'],
                    ['generic', "your client's MCP config"],
                  ].map(([id, path]) => (
                    <TabsContent key={id} value={id} className="mt-3">
                      <CodeBlock code={mcpConfig} caption={path} />
                    </TabsContent>
                  ))}
                </Tabs>
              </TabsContent>

              <TabsContent value="group" className="mt-4">
                <p className="mb-3 font-sans text-sm leading-relaxed text-text-secondary">
                  Scope to specific servers and/or groups with the{' '}
                  <span className="font-mono text-text-primary">
                    x-mcp-servers
                  </span>{' '}
                  header (comma-separated) — or target a group straight from the
                  URL,{' '}
                  <span className="font-mono text-text-primary">
                    {`${mcpUrl}/dev-group`}
                  </span>
                  .
                </p>
                <CodeBlock
                  code={mcpFilteredConfig}
                  caption="MCP client config · scoped"
                />
              </TabsContent>

              <TabsContent value="curl" className="mt-4">
                <p className="mb-3 font-sans text-sm leading-relaxed text-text-secondary">
                  List and call tools directly over the MCP REST API — no client,
                  no LLM. Swap{' '}
                  <span className="font-mono text-text-primary">
                    {KEY_PLACEHOLDER}
                  </span>{' '}
                  for your key and the server/tool names for real ones.
                </p>
                <CodeBlock code={mcpCurl} caption="curl" />
              </TabsContent>
            </Tabs>

            <a
              href="https://docs.litellm.ai/docs/mcp"
              target="_blank"
              rel="noopener noreferrer"
              className="mt-3 inline-flex items-center gap-1 font-sans text-xs font-medium text-primary hover:underline"
            >
              LiteLLM MCP gateway guide
              <ExternalLink className="size-3" aria-hidden="true" />
            </a>
          </Section>

          {/* §4 No terminal? */}
          <Section
            id="chat"
            icon={MessageSquare}
            title="No terminal?"
            sub="Prefer a chat window over a shell? Use a browser UI — paste your key once and start chatting."
          >
            <div className="grid gap-4 sm:grid-cols-2">
              {/* ACKstorm Chat — hosted */}
              <a
                href={chatUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="group flex flex-col gap-2 rounded-xl border border-border bg-surface p-5 transition-colors hover:border-primary"
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="font-sans text-sm font-semibold text-text-primary">
                    ACKstorm Chat
                  </span>
                  {/* Hosted + ready -> the open-in-new icon is accent green
                      (openwork's stays muted since it is not wired yet). */}
                  <ExternalLink
                    className="size-4 text-primary"
                    aria-hidden="true"
                  />
                </div>
                <p className="font-sans text-sm leading-relaxed text-text-secondary">
                  Hosted chat UI, ready to use — sign in and pick{' '}
                  <span className="font-mono text-text-primary">
                    {MODEL_ALIAS}
                  </span>
                  . Nothing to install.
                </p>
                <span className="mt-1 break-all font-mono text-[11px] text-text-tertiary">
                  {chatUrl}
                </span>
              </a>

              {/* openwork — coming soon (not yet wired to the gateway) */}
              <a
                href="https://github.com/different-ai/openwork"
                target="_blank"
                rel="noopener noreferrer"
                className="group flex flex-col gap-2 rounded-xl border border-border bg-surface p-5 transition-colors hover:border-primary"
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="flex items-center gap-2">
                    <span className="font-sans text-sm font-semibold text-text-primary">
                      openwork
                    </span>
                    <span className="inline-flex items-center rounded-md border border-amber-500/30 bg-amber-500/10 px-1.5 py-0.5 font-mono text-[10px] font-semibold uppercase tracking-wide text-amber-600 dark:text-amber-400">
                      Coming soon
                    </span>
                  </span>
                  <ExternalLink
                    className="size-4 text-text-tertiary transition-colors group-hover:text-primary"
                    aria-hidden="true"
                  />
                </div>
                <p className="font-sans text-sm leading-relaxed text-text-secondary">
                  Open-source AI workspace. Gateway support is on the way — not
                  available yet. Star the repo to follow along.
                </p>
                <span className="mt-1 break-all font-mono text-[11px] text-text-tertiary">
                  github.com/different-ai/openwork
                </span>
              </a>
            </div>
          </Section>

          {/* §5 Troubleshooting */}
          <Section
            id="troubleshooting"
            icon={AlertCircle}
            title="Troubleshooting"
            sub="The most common onboarding errors and what they mean."
          >
            <div className="flex flex-col gap-3">
              {[
                ['401 Unauthorized', 'Your API key is missing, invalid, expired, or copied incorrectly. Mint a fresh key on the Keys tab.'],
                ['404 Model not found', 'The model alias is not enabled for your team or does not exist. Check the Models tab for available aliases.'],
                ['429 Rate limited', 'Your key, team, or the upstream provider hit a rate limit. Retry with backoff or check your limits.'],
                ['MCP: no tools discovered', 'The MCP server is reachable but returned no tools. Check the server health and your access group on the MCP tab.'],
                ['MCP: authentication failed', `Confirm your MCP client sends the ${AUTH_HEADER} header with a Bearer key.`],
              ].map(([code, body]) => (
                <div key={code} className="rounded-lg border border-border bg-surface p-4">
                  <div className="font-mono text-sm font-semibold text-text-primary">{code}</div>
                  <p className="mt-1 font-sans text-sm leading-relaxed text-text-secondary">{body}</p>
                </div>
              ))}
            </div>
          </Section>
        </div>
      </div>
    </div>
  );
}
