// urls.ts — small URL helpers shared across routes/components (DRY).

// Where platform-api serves the console; the same path as vite.config.ts `base`.
export const CONSOLE_ROOT = '/console/';

// Console login (platform-api OAuth AS in-process, D-27). `next` is the
// in-app path to return to after the IdP round-trip; always URL-encoded.
export function loginUrl(next: string): string {
  return `/platform/console/session/login?next=${encodeURIComponent(next)}`;
}
