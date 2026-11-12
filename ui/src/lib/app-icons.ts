// app-icons.ts — the glyphs an Apps tile may name (chart platformApi.console.apps[].icon).
// A fixed map keeps the bundle to these icons; an unknown name gets AppWindow.
import {
  Activity, AppWindow, BookOpen, Bot, ChartLine, Cloud, Code, Database, GitBranch,
  Gauge, Globe, LayoutGrid, MessageCircle, Search, Shield, Sparkles, Terminal, Workflow,
  type LucideIcon,
} from 'lucide-react';

// A Map, not an object literal: a name like "constructor" must not resolve.
const ICONS = new Map<string, LucideIcon>(Object.entries({
  activity: Activity, 'book-open': BookOpen, bot: Bot, 'chart-line': ChartLine, cloud: Cloud,
  code: Code, database: Database, 'git-branch': GitBranch, gauge: Gauge, globe: Globe,
  'layout-grid': LayoutGrid, 'message-circle': MessageCircle, search: Search, shield: Shield,
  sparkles: Sparkles, terminal: Terminal, workflow: Workflow,
}));

export function appIcon(name?: string): LucideIcon {
  return (name && ICONS.get(name)) || AppWindow;
}
