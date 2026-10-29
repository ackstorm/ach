// chip.ts — the shared filter-chip button styling (active vs idle) used by the
// models catalog filter rows and the stats USAGE BREAKDOWN type filter, so the
// two stay pixel-identical. A control, not a Tag, but `.chip` (styles/ledger-ui.css)
// gives it the same radius and mono 10px font as `.tag`.
export const chipClass = (on: boolean): string =>
  `chip cursor-pointer border px-2 py-1 uppercase transition-colors ${
    on ? 'border-primary bg-primary/10 text-primary' : 'border-border text-text-secondary hover:border-primary'
  }`;
