// Catalog.tsx — page header and state card shared by the catalog pages
// (Models, MCPs, A2A, Environments, Keys) in the console design.
import type { ReactNode } from 'react';
import { formatInt } from '@/lib/format';
import './catalog.css';

/** `count` (items the user can see, before search/filters) follows the title; omitted while 0 — loading, unavailable or empty — so it never flashes "(0)". */
export function CatalogHead({ title, description, count, children }: { title: string; description: string; count?: number; children?: ReactNode }) {
  return <div className="head"><div><h1>{title}{count ? <> <span className="muted">({formatInt(count)})</span></> : null}</h1><p>{description}</p></div>{children}</div>;
}
export function CatalogState({ heading, children }: { heading: string; children: ReactNode }) {
  return <section className="panel empty"><h2>{heading}</h2><p>{children}</p></section>;
}
