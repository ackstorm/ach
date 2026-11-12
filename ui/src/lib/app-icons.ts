// app-icons.ts — the glyphs an Apps tile may name (chart platformApi.console.apps[].icon).
// A fixed map keeps the bundle to these icons; an unknown name gets AppWindow.
// Each glyph carries an OKLCH hue: the tile takes the skin's primary lightness and
// chroma at that hue, so apps differ by color under every skin.
import {
  Activity, AppWindow, BookOpen, Bot, ChartLine, Cloud, Code, Database, GitBranch,
  Gauge, Globe, LayoutGrid, MessageCircle, Search, Shield, Sparkles, Terminal, Workflow,
  type LucideIcon,
} from 'lucide-react';

export interface AppGlyph { Icon: LucideIcon; hue?: number }

// A Map, not an object literal: a name like "constructor" must not resolve.
const ICONS = new Map<string, AppGlyph>(Object.entries({
  activity: { Icon: Activity, hue: 25 }, 'book-open': { Icon: BookOpen, hue: 75 },
  bot: { Icon: Bot, hue: 300 }, 'chart-line': { Icon: ChartLine, hue: 150 },
  cloud: { Icon: Cloud, hue: 230 }, code: { Icon: Code, hue: 262 },
  database: { Icon: Database, hue: 200 }, 'git-branch': { Icon: GitBranch, hue: 340 },
  gauge: { Icon: Gauge, hue: 60 }, globe: { Icon: Globe, hue: 190 },
  'layout-grid': { Icon: LayoutGrid, hue: 280 }, 'message-circle': { Icon: MessageCircle, hue: 322 },
  search: { Icon: Search, hue: 100 }, shield: { Icon: Shield, hue: 160 },
  sparkles: { Icon: Sparkles, hue: 45 }, terminal: { Icon: Terminal, hue: 130 },
  workflow: { Icon: Workflow, hue: 210 },
}));

export function appIcon(name?: string): AppGlyph {
  return (name && ICONS.get(name)) || { Icon: AppWindow };
}
