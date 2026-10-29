// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
// routes.tsx — the Enterprise routes and navigation entries.
import type { RouteObject } from 'react-router';
import { Brain, CircleDollarSign, Cpu, Gauge, GitBranch, LayoutGrid, ShieldCheck, type LucideIcon } from 'lucide-react';
import { Traces } from './routes/Traces';
import { Cost } from './routes/Cost';
import { Performance } from './routes/Performance';
import { Runtime } from './routes/Runtime';
import { Reliability } from './routes/Reliability';
import { Workloads } from './routes/Workloads';
import { ContextSkills } from './routes/ContextSkills';
import { Overview } from './routes/Overview';

// The landing page with Enterprise on: the core Overview plus the generation evidence.
export const EeOverview = Overview;

export const eeRoutes: RouteObject[] = [
  { path: 'analytics/traces', element: <Traces /> },
  { path: 'analytics/cost', element: <Cost /> },
  { path: 'analytics/performance', element: <Performance /> },
  { path: 'analytics/runtime', element: <Runtime /> },
  { path: 'analytics/reliability', element: <Reliability /> },
  { path: 'analytics/workloads', element: <Workloads /> },
  { path: 'analytics/context', element: <ContextSkills /> },
];

// The Analytics children (alphabetical), then Traces.
export const eeNav: { to: string; label: string; Icon: LucideIcon }[] = [
  { to: '/analytics/cost', label: 'Cost', Icon: CircleDollarSign },
  { to: '/analytics/performance', label: 'Performance', Icon: Gauge },
  { to: '/analytics/reliability', label: 'Reliability', Icon: ShieldCheck },
  { to: '/analytics/runtime', label: 'Runtime', Icon: Cpu },
  { to: '/analytics/context', label: 'Skills & context', Icon: Brain },
  { to: '/analytics/workloads', label: 'Workloads', Icon: LayoutGrid },
  { to: '/analytics/traces', label: 'Traces', Icon: GitBranch },
];

// The page title of every Enterprise route.
export const eeTitles: Record<string, string> = Object.fromEntries(eeNav.map((item) => [item.to, item.label]));
