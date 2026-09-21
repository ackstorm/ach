// urls.ts — small URL helpers shared across routes/components (DRY).

// Derive a sibling-subdomain URL from the gateway endpoint by swapping the
// leading `api.` host label (e.g. https://api.acme.ai -> https://chat.acme.ai).
// A host without the `api.` prefix just gets the sub prepended to the bare host.
// On an absent/unparseable endpoint, falls back to a neutral placeholder host.
export function deriveSubdomainUrl(endpoint: string | undefined, sub: string): string {
  try {
    const u = new URL(endpoint ?? '');
    const baseHost = u.hostname.replace(/^api\./, '');
    return `${u.protocol}//${sub}.${baseHost}`;
  } catch {
    return `https://${sub}.your-domain.example`;
  }
}

// Console login (platform-api OAuth AS in-process, D-27). `next` is the
// in-app path to return to after the IdP round-trip; always URL-encoded.
export function loginUrl(next: string): string {
  return `/platform/console/session/login?next=${encodeURIComponent(next)}`;
}
