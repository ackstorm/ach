// api-snippets.ts — shared constants for every rendered API-usage snippet
// (How-to, Models "copy curl"). ACH keys are `ek_…`/`pk_…` — there is no
// `sk-` key in ACH's user-facing flow. The forwarder resolves the declared
// credential header slots verbatim (no `Bearer ` prefix); `x-ach-key` is the
// chart default's first slot, so it's what every snippet should lead with.

/** The illustrative key value shown in copy — never a real secret. */
export const KEY_PLACEHOLDER = 'ek_...';

/** The forwarder's default "resolve" credential header (chart default). */
export const AUTH_HEADER = 'x-ach-key';

/** Fallback gateway host before the session endpoint resolves. */
export const FALLBACK_API_BASE = 'https://api.your-domain.example';
