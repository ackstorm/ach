// Tag.tsx — the ONE category tag (neutral outline, no fill, mono 10px, case as
// given). Styles: `.tag` in styles/ledger-ui.css. `dim` = dashed (empty/absent).
import type { HTMLAttributes } from 'react';

export function Tag({ dim, className = '', ...rest }: { dim?: boolean } & HTMLAttributes<HTMLSpanElement>) {
  return <span {...rest} className={`tag${dim ? ' dim' : ''}${className ? ` ${className}` : ''}`} />;
}
