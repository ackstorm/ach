// chip.ts — the shared filter-chip button styling (active vs idle) used by the
// models catalog filter rows and the stats USAGE BREAKDOWN type filter, so the
// two stay pixel-identical.
export const chipClass = (on: boolean): string =>
  `cursor-pointer rounded-md border px-2 py-1 font-mono text-[10px] font-semibold uppercase tracking-wide transition-colors ${
    on ? 'border-primary bg-primary/10 text-primary' : 'border-border text-text-secondary hover:border-primary'
  }`;
