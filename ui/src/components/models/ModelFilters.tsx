// ModelFilters.tsx — quick-filter toggles above the models table: a MODE row
// (Chat / Embeddings / Audio / Image / Video, derived from the catalog) and a
// CAPABILITY row (Vision / Thinking / Tools / Search).
import * as React from 'react';
import type { ModelRow } from '@/lib/api-types';
import { chipClass } from '@/lib/chip';

export type ModelCapKey = 'vision' | 'reasoning' | 'function_calling' | 'web_search';

const CAP_FIELD: Record<ModelCapKey, keyof ModelRow> = {
  vision: 'supports_vision',
  reasoning: 'supports_reasoning',
  function_calling: 'supports_function_calling',
  web_search: 'supports_web_search',
};

const CAP_LABEL: Record<ModelCapKey, string> = {
  vision: 'Vision',
  reasoning: 'Thinking',
  function_calling: 'Tools',
  web_search: 'Search',
};

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

export function ModelModeFilters({
  options,
  active,
  onToggle,
  onClear,
}: {
  options: ModeBucket[];
  active: Set<string>;
  onToggle: (k: string) => void;
  onClear: () => void;
}): React.ReactElement {
  const allOn = active.size === 0;
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <span className="mr-1 font-mono text-[10px] font-semibold uppercase tracking-wider text-text-tertiary">
        Mode
      </span>
      <button type="button" aria-pressed={allOn} onClick={onClear} className={chipClass(allOn)}>
        All
      </button>
      {options.map((o) => (
        <button
          key={o.key}
          type="button"
          aria-pressed={active.has(o.key)}
          onClick={() => onToggle(o.key)}
          className={chipClass(active.has(o.key))}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function ModelFilters({
  active,
  onToggle,
  onClear,
}: {
  active: Set<ModelCapKey>;
  onToggle: (k: ModelCapKey) => void;
  onClear: () => void;
}): React.ReactElement {
  // "ALL" = no capability filter (the default). Selected whenever the active set
  // is empty; clicking it clears any active capability toggles.
  const allOn = active.size === 0;
  return (
    <div className="flex flex-wrap gap-1.5">
      <button type="button" aria-pressed={allOn} onClick={onClear} className={chipClass(allOn)}>
        All
      </button>
      {(Object.keys(CAP_LABEL) as ModelCapKey[]).map((k) => (
        <button
          key={k}
          type="button"
          aria-pressed={active.has(k)}
          onClick={() => onToggle(k)}
          className={chipClass(active.has(k))}
        >
          {CAP_LABEL[k]}
        </button>
      ))}
    </div>
  );
}
