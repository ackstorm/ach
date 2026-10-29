// ConsoleNavigation.tsx — the sidebar: Overview (standalone), then Access →
// Runtime → Observability → Help. Analytics is collapsed by default (chevron);
// visiting one of its pages opens it. In the icon-only sidebar (`collapsed`)
// every link is named by aria-label/title and the Analytics children show
// directly as icons (no toggle, no Coming-soon or Enterprise-only rows). Without
// Enterprise the Analytics and Traces entries are marked "Enterprise only".
import { useEffect, useState } from 'react';
import { NavLink, useLocation } from 'react-router';
import { House, ChartNoAxesCombined, Box, Network, Workflow, KeyRound, BookOpen, Layers, ChevronDown, FlaskConical } from 'lucide-react';
import { eeNav } from '@ee/routes';
import { useEnterprise } from '@/lib/edition';

// eeNav is the Analytics children followed by Traces, which sits outside the group.
const ANALYTICS = eeNav.slice(0, -1);
const TRACES_ITEM = eeNav.at(-1);

export function ConsoleNavigation({ onNavigate, collapsed = false }: { onNavigate?: () => void; collapsed?: boolean }) {
  const location = useLocation();
  const enterprise = useEnterprise();
  const [open, setOpen] = useState(() => {
    try { return window.sessionStorage.getItem('console.analytics.open') === 'true'; } catch { return false; }
  });
  useEffect(() => {
    try { window.sessionStorage.setItem('console.analytics.open', String(open)); } catch { /* storage blocked */ }
  }, [open]);
  useEffect(() => { if (location.pathname.startsWith('/analytics/') && location.pathname !== TRACES_ITEM?.to) setOpen(true); }, [location.pathname]);
  const link = (to: string, label: string, Icon: typeof House, className?: string) => <NavLink key={to} to={to} end={to === '/'} className={className} onClick={onNavigate} aria-label={collapsed ? label : undefined} title={collapsed ? label : undefined}><Icon aria-hidden="true" /><span>{label}</span></NavLink>;
  const disabled = (item: { to: string; label: string }, className: string) => <span key={item.to} className={className} aria-disabled="true">{item.label} · Enterprise only</span>;
  return <>
    <nav className="nav" aria-label="Overview">{link('/', 'Overview', House)}</nav>
    <div className="nav-label">Access</div>
    <nav className="nav" aria-label="Access">{link('/keys', 'Keys', KeyRound)}{link('/environments', 'Environments', Layers)}</nav>
    <div className="nav-label">Runtime</div>
    <nav className="nav" aria-label="Runtime">{link('/models', 'Models', Box)}{link('/mcp', 'MCPs', Network)}{link('/a2a', 'A2A', Workflow)}</nav>
    <div className="nav-label">Observability</div>
    <nav className="nav" aria-label="Observability">
      {!collapsed && <button type="button" className="nav-toggle" aria-expanded={open} aria-controls="analytics-navigation" onClick={() => setOpen(!open)}><ChartNoAxesCombined aria-hidden="true" />Analytics<ChevronDown className="chevron" aria-hidden="true" /></button>}
      {(open || collapsed) && <div id="analytics-navigation">
        {ANALYTICS.map((item) => enterprise ? link(item.to, item.label, item.Icon, 'nav-child') : !collapsed && disabled(item, 'nav-disabled nav-child'))}
      </div>}
      {!collapsed && <span className="nav-disabled nav-soon" aria-disabled="true"><FlaskConical aria-hidden="true" />Evals · Coming soon</span>}
      {TRACES_ITEM && (enterprise ? link(TRACES_ITEM.to, TRACES_ITEM.label, TRACES_ITEM.Icon) : !collapsed && disabled(TRACES_ITEM, 'nav-disabled'))}
    </nav>
    <div className="nav-label">Help</div>
    <nav className="nav" aria-label="Help">{link('/howto', 'How-to', BookOpen)}</nav>
  </>;
}
