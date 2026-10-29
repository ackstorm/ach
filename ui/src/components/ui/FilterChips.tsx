// FilterChips.tsx — a labelled row of toggle chips (title above the chips): ALL clears the selection,
// each option toggles. Past `max` options a "…" chip reveals the rest inline;
// a selected chip always stays visible. Filter semantics (AND/OR) belong to
// the caller. Styling: lib/chip.ts.
import { useState } from 'react';
import { chipClass } from '@/lib/chip';

export interface ChipOption<K extends string> { key: K; label: string }

/** A copy of `set` with `key` flipped. */
export function toggled<K>(set: Set<K>, key: K): Set<K> {
  const next = new Set(set);
  if (!next.delete(key)) next.add(key);
  return next;
}

export function FilterChips<K extends string>({ label, allLabel, options, active, onToggle, onClear, max = Infinity }: { label: string; allLabel: string; options: ChipOption<K>[]; active: Set<K>; onToggle: (key: K) => void; onClear: () => void; max?: number }) {
  const [open, setOpen] = useState(false);
  const hidden = open ? [] : options.slice(max).filter((o) => !active.has(o.key));
  const shown = options.filter((o) => !hidden.includes(o));
  return <div className="filter-chips" role="group" aria-label={label}>
    <span className="label">{label}</span>
    <div className="chips">
      <button type="button" aria-pressed={active.size === 0} aria-label={allLabel} onClick={onClear} className={chipClass(active.size === 0)}>All</button>
      {shown.map((o) => <button key={o.key} type="button" aria-pressed={active.has(o.key)} onClick={() => onToggle(o.key)} className={chipClass(active.has(o.key))}>{o.label}</button>)}
      {(open || hidden.length > 0) && <button type="button" aria-expanded={open} aria-label={open ? `Show fewer ${label.toLowerCase()}` : `Show ${hidden.length} more ${label.toLowerCase()}`} onClick={() => setOpen(!open)} className={chipClass(false)}>{open ? 'less' : '…'}</button>}
    </div>
  </div>;
}
