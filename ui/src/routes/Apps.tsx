// Apps.tsx — the #/apps page: tiles linking to the platform's other tools
// (bootstrap `apps`, chart platformApi.console.apps), already filtered by the
// server to what this user may see. Groups keep their first-seen order.
import type { CSSProperties } from 'react';
import { ArrowUpRight } from 'lucide-react';

import { CatalogHead } from '@/components/catalog/Catalog';
import { appIcon } from '@/lib/app-icons';
import type { ConsoleApp } from '@/lib/api-types';
import { useSessionStore } from '@/stores/session';

export function Apps() {
  const apps = useSessionStore((s) => s.me?.apps) ?? [];
  const groups = new Map<string, ConsoleApp[]>();
  for (const a of apps) groups.set(a.group ?? '', [...(groups.get(a.group ?? '') ?? []), a]);

  return (
    <div className="catalog-page flex flex-col gap-6">
      <CatalogHead title="Apps" description="Tools on this platform. Each one opens in a new tab and signs you in with its own login." />
      {[...groups].map(([group, items]) => (
        <section key={group} className="flex flex-col gap-3" aria-label={group || 'Apps'}>
          {group && (
            <div className="app-group">
              <h2 className="label">{group}</h2>
              <span className="app-count">{items.length}</span>
            </div>
          )}
          <div className="app-grid">
            {items.map((a, i) => {
              const { Icon, hue } = appIcon(a.icon);
              // The server only sends absolute http(s) urls (consoleApps validation).
              const host = new URL(a.url).host;
              const style = { '--app': hue === undefined ? 'var(--primary)' : `oklch(from var(--primary) l c ${hue})` } as CSSProperties;
              return (
                <a key={i} href={a.url} target="_blank" rel="noopener noreferrer" className="app-tile" style={style}>
                  <ArrowUpRight className="app-go" aria-hidden="true" />
                  <span className="app-icon"><Icon aria-hidden="true" /></span>
                  <span className="flex min-w-0 flex-col gap-0.5">
                    <span className="app-name">{a.name}</span>
                    {a.subtitle && <span className="app-sub">{a.subtitle}</span>}
                  </span>
                  <span className="app-host">{host}</span>
                </a>
              );
            })}
          </div>
        </section>
      ))}
    </div>
  );
}
