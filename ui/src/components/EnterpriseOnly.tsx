// EnterpriseOnly.tsx — renders an Enterprise page, or a notice when the
// deployment runs without enterprise.enabled.
import type { ReactNode } from 'react';
import { useEnterprise } from '@/lib/edition';

export function EnterpriseOnly({ children }: { children: ReactNode }) {
  if (useEnterprise()) return <>{children}</>;
  return <section className="panel report-gap"><h1>Enterprise only</h1>
    <p className="report-notice">This page is part of ACH Enterprise. An administrator enables it with the Helm value enterprise.enabled.</p></section>;
}
