// CodeBlock.tsx — the one code surface: always dark (.codeblock, independent
// of the skin) with a copy button that writes exactly `code`. The default is a
// captioned block; `inline` is a one-row command with a `$` prompt.
import { Check, Copy } from 'lucide-react';
import { useCopyFeedback } from '@/hooks/use-copy-feedback';

export function CodeBlock({ code, caption, inline = false }: { code: string; caption?: string; inline?: boolean }) {
  const { copied, copy } = useCopyFeedback();
  const button = <button type="button" onClick={() => void copy(code)} className={copied ? 'btn sm is-done' : 'btn sm'}>{copied ? <Check className="size-3" aria-hidden="true" /> : <Copy className="size-3" aria-hidden="true" />}{copied ? 'copied!' : 'copy'}</button>;
  if (inline) return <div className="codeblock inline"><code><span className="prompt">$ </span>{code}</code>{button}</div>;
  return <div className="codeblock"><div className="codeblock-head"><span>{caption ?? 'shell'}</span>{button}</div><pre><code>{code}</code></pre></div>;
}
