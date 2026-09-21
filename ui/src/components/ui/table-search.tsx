// table-search.tsx — a shared search input + a matcher for client-side table filtering.
import * as React from 'react';
import { Search } from 'lucide-react';

export function matchesSearch(term: string, ...fields: (string | null | undefined)[]): boolean {
  const q = term.trim().toLowerCase();
  if (q === '') return true;
  return fields.some((f) => typeof f === 'string' && f.toLowerCase().includes(q));
}

export function TableSearch({
  value,
  onChange,
  placeholder = 'Search…',
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
}): React.ReactElement {
  return (
    <div className="relative w-full max-w-xs">
      <Search
        className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-text-tertiary"
        aria-hidden="true"
      />
      <input
        type="search"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        aria-label={placeholder}
        className="h-8 w-full rounded-md border border-border bg-transparent pl-8 pr-2 font-sans text-sm outline-none focus-visible:border-primary"
      />
    </div>
  );
}
