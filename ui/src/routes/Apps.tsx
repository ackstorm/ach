// Apps.tsx — the #/apps page: tiles linking to the platform's other tools
// (bootstrap `apps`, chart platformApi.console.apps), already filtered by the
// server to what this user may see. Groups keep their first-seen order.
import { ExternalLink } from 'lucide-react';

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
          {group && <h2 className="label">{group}</h2>}
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {items.map((a, i) => {
              const Icon = appIcon(a.icon);
              return (
                <a key={i} href={a.url} target="_blank" rel="noopener noreferrer" className="panel flex items-center gap-3 p-4 hover:border-primary">
                  <span className="inline-flex size-10 shrink-0 items-center justify-center rounded-lg border border-border">
                    <Icon className="size-5 text-primary" aria-hidden="true" />
                  </span>
                  <span className="flex min-w-0 flex-col">
                    <span className="flex items-center gap-1.5 font-sans text-sm font-semibold text-text-primary">
                      {a.name}<ExternalLink className="size-3 text-text-tertiary" aria-hidden="true" />
                    </span>
                    {a.subtitle && <span className="font-sans text-xs text-text-secondary">{a.subtitle}</span>}
                  </span>
                </a>
              );
            })}
          </div>
        </section>
      ))}
    </div>
  );
}
