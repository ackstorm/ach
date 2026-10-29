// console-range.ts — the console-wide activity period (the RangePicker in each
// observability page header). Every observability page reads the same range, so
// one selection scopes them all; it survives a reload via sessionStorage.
import { create } from 'zustand';
import { presetToRange } from '@/lib/stats-presets';
import { useStats } from '@/hooks/use-stats';

export type DayRange = { start: string; end: string };
interface Saved { preset: string; custom: DayRange | null }

const KEY = 'console.range';
function load(): Saved {
  try {
    const saved = JSON.parse(window.sessionStorage.getItem(KEY) ?? 'null') as Saved | null;
    if (saved && typeof saved.preset === 'string') return { preset: saved.preset, custom: saved.custom ?? null };
  } catch { /* storage blocked or corrupt */ }
  return { preset: '30d', custom: null };
}
function save(state: Saved): Saved {
  try { window.sessionStorage.setItem(KEY, JSON.stringify(state)); } catch { /* storage blocked */ }
  return state;
}

export const useConsoleRange = create<Saved & { setPreset: (preset: string) => void; setCustom: (range: DayRange) => void }>((set) => ({
  ...load(),
  setPreset: (preset) => set(save({ preset, custom: null })),
  setCustom: (custom) => set(save({ preset: 'Custom', custom })),
}));

export function useConsoleDateRange(): DayRange {
  const preset = useConsoleRange((state) => state.preset);
  const custom = useConsoleRange((state) => state.custom);
  return preset === 'Custom' && custom ? custom : presetToRange(preset, new Date());
}

export function useConsoleStats() {
  return useStats(useConsoleDateRange());
}
