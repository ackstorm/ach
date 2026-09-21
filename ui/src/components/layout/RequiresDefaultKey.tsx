// RequiresDefaultKey.tsx — the gate state shown on the Models/MCP pages when the
// user has no default key.
//
// The catalog reads are per-user: the service sends x-user-id and the gateway's
// custom auth resolves the caller's scope through their DEFAULT key. With no
// default key there is nothing to scope to (the custom auth 403s), so instead of
// firing the request and showing an error, the page renders this calm prompt and
// points the user at the Keys tab to set one. The link is a plain `#/` anchor
// (this is a hash-router app; `#/` is the Keys/dashboard route) so the component
// needs no Router context.

const HEADING = 'A default key is required';

export function RequiresDefaultKey({ subject }: { subject: string }) {
  return (
    <div
      data-slot="requires-default-key"
      className="flex flex-col items-center gap-3 rounded-xl border border-border bg-surface p-12 text-center"
    >
      <div className="font-sans text-2xl font-semibold text-text-primary">
        {HEADING}
      </div>
      <div className="max-w-md font-sans text-sm text-text-secondary">
        {subject} are scoped to your account through your default key. Set a
        default key on the Keys tab to view them.
      </div>
      <a
        href="#/"
        className="inline-flex cursor-pointer items-center justify-center rounded-lg border border-primary px-4 py-2 font-mono text-xs font-semibold uppercase tracking-wide text-primary transition-colors hover:bg-primary/10"
      >
        Go to Keys
      </a>
    </div>
  );
}
