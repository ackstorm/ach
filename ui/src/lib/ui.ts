// ui.ts — shared class tokens for the in-page "segmented selector" family:
// Stats period presets (DateRange), the HowTo tool/variant tabs, and the Mcp
// section tabs. ONE source of truth so the active/idle treatment can't drift
// per-route — the bug this consolidates was exactly that drift (the active pill
// was green-text + green-tint on Stats but white-text on HowTo, and rounded-lg
// on one vs rounded-full on the other).
//
// Selected-state hierarchy across the app (all green, ranked):
//   L1 primary nav (AppShell topbar) → green UNDERLINE  (defined in AppShell)
//   L2 in-page tabs / L3 filters     → green-TINT PILL  (these tokens)
//
// Typography is added at the call site (mono-uppercase for data filters like
// 7D/30D; sans for names like "Claude Code"); the chrome + the green accent
// live here so every selector reads as the same control.

// Plain <button> pills (Stats DateRange presets) — the caller toggles active by
// concatenating PILL_IDLE or PILL_ACTIVE onto PILL_BASE.
export const PILL_BASE =
  'inline-flex cursor-pointer items-center justify-center rounded-lg border bg-transparent transition-colors';
// bg-surface (not transparent): a transparent idle pill is shape-defined by its
// border ALONE, and on the pastel skin --border (~0.9L) vanishes against the
// saturated header gradient. The faint surface fill reads as a chip on every skin
// (like the cards do) so the frame is visible even when the border isn't. Active
// (PILL_ACTIVE, bg-primary/10) overrides it — the two are mutually exclusive.
export const PILL_IDLE =
  'border-border bg-surface text-text-secondary hover:border-text-tertiary hover:text-text-primary';
export const PILL_ACTIVE = 'border-primary bg-primary/10 text-primary';

// Radix <TabsTrigger> pills (HowTo L2 tool groups + L3 variants, Mcp L2) — the
// active state is driven by `data-state=active`, not a caller-side toggle.
// The `dark:data-[state=active]:*` overrides are REQUIRED: shadcn's TabsTrigger
// base ships dark-mode active defaults (text-foreground / bg-input/30 /
// border-input) that out-specify a plain `data-[state=active]:*` in dark mode —
// without the matching dark: keys the active pill renders white-on-grey instead
// of the green tint.
export const TAB_PILL =
  'flex-none cursor-pointer rounded-lg border border-border bg-transparent px-3 py-1 font-sans text-sm font-medium text-text-secondary shadow-none transition-colors hover:border-text-tertiary hover:text-text-primary data-[state=active]:border-primary data-[state=active]:bg-primary/10 data-[state=active]:text-primary data-[state=active]:shadow-none dark:data-[state=active]:border-primary dark:data-[state=active]:bg-primary/10 dark:data-[state=active]:text-primary';

// The transparent <TabsList> container for a pill row (replaces variant="line",
// which rendered an underlined tab strip).
export const TAB_PILL_LIST = 'h-auto gap-2 bg-transparent p-0';
