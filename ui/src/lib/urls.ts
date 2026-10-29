// urls.ts — small URL helpers shared across routes/components (DRY).

// Console login (platform-api OAuth AS in-process, D-27). `next` is the
// in-app path to return to after the IdP round-trip; always URL-encoded.
export function loginUrl(next: string): string {
  return `/platform/console/session/login?next=${encodeURIComponent(next)}`;
}
