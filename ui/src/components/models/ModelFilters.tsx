// ModelFilters.tsx — the models table's quick filters: MODE buckets (Chat /
// Embeddings / Audio / Image / Video, derived from the catalog) and
// CAPABILITIES (Vision / Thinking / Tools / Search). Rendered by FilterChips.
import type { ModelRow } from '@/lib/api-types';

export type ModelCapKey = 'vision' | 'reasoning' | 'function_calling' | 'web_search';

const CAP_FIELD: Record<ModelCapKey, keyof ModelRow> = {
  vision: 'supports_vision',
  reasoning: 'supports_reasoning',
  function_calling: 'supports_function_calling',
  web_search: 'supports_web_search',
};

export const CAP_OPTIONS: { key: ModelCapKey; label: string }[] = [
  { key: 'vision', label: 'Vision' },
  { key: 'reasoning', label: 'Thinking' },
  { key: 'function_calling', label: 'Tools' },
  { key: 'web_search', label: 'Search' },
];

// Keep only rows where EVERY active capability is supported (AND semantics).
export function applyModelFilters(rows: ModelRow[], active: Set<ModelCapKey>): ModelRow[] {
  if (active.size === 0) return rows;
  return rows.filter((r) => [...active].every((k) => Boolean(r[CAP_FIELD[k]])));
}

// ── Mode filter ─────────────────────────────────────────────────────────────
// LiteLLM `mode` is a free string ("chat" | "completion" | "embedding" |
// "image_generation" | "audio_transcription" | …). Bucket the raw values into a
// few friendly display groups; unknown modes pass through title-cased so nothing
// is unfilterable.
interface ModeBucket {
  key: string;
  label: string;
}

const MODE_MAP: Record<string, ModeBucket> = {
  chat: { key: 'chat', label: 'Chat' },
  completion: { key: 'chat', label: 'Chat' },
  responses: { key: 'chat', label: 'Chat' },
  embedding: { key: 'embeddings', label: 'Embeddings' },
  image_generation: { key: 'image', label: 'Image' },
  audio_transcription: { key: 'audio', label: 'Audio' },
  audio_speech: { key: 'audio', label: 'Audio' },
  video_generation: { key: 'video', label: 'Video' },
};

// The user-facing ordering for the known buckets; extras sort after, alpha.
const MODE_ORDER = ['chat', 'embeddings', 'audio', 'image', 'video'];

function modeBucket(mode: string | null): ModeBucket | null {
  if (!mode) return null;
  const m = mode.toLowerCase();
  if (MODE_MAP[m]) return MODE_MAP[m];
  if (m.startsWith('audio')) return { key: 'audio', label: 'Audio' };
  if (m.includes('embed')) return { key: 'embeddings', label: 'Embeddings' };
  if (m.includes('image')) return { key: 'image', label: 'Image' };
  if (m.includes('video')) return { key: 'video', label: 'Video' };
  return { key: m, label: m.charAt(0).toUpperCase() + m.slice(1).replace(/_/g, ' ') };
}

// The distinct mode buckets present in the catalog, in user-facing order. Used to
// decide which mode chips to render (and whether to show the row at all).
export function modelModeOptions(rows: ModelRow[]): ModeBucket[] {
  const seen = new Map<string, string>();
  for (const r of rows) {
    const b = modeBucket(r.mode);
    if (b && !seen.has(b.key)) seen.set(b.key, b.label);
  }
  return [...seen.keys()]
    .sort((a, b) => {
      const ia = MODE_ORDER.indexOf(a);
      const ib = MODE_ORDER.indexOf(b);
      if (ia !== -1 || ib !== -1) return (ia === -1 ? 99 : ia) - (ib === -1 ? 99 : ib);
      return a.localeCompare(b);
    })
    .map((key) => ({ key, label: seen.get(key) as string }));
}

// Keep rows whose mode bucket is in the active set (OR semantics — a model has
// exactly one mode). Empty active set → no filtering.
export function applyModeFilter(rows: ModelRow[], active: Set<string>): ModelRow[] {
  if (active.size === 0) return rows;
  return rows.filter((r) => {
    const b = modeBucket(r.mode);
    return b !== null && active.has(b.key);
  });
}
