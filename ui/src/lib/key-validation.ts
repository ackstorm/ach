// key-validation.ts — client-side alias/duration validators that MIRROR
// src/api/app/session.py:session_create_key (lifted verbatim from
// src/ui/create-key.js, Preact -> React port). Surfacing the locked field
// messages client-side lets the create-key modal reject bad input without a
// backend round-trip; a server 422 maps back to the same per-field messages.
//
// Empty input is VALID for both fields — they are optional, so an empty value
// means "omit this field from the request body" (an empty body is valid;
// session.py supplies the defaults).

// alias safe chars: alphanumeric + dash/underscore/dot. session.py uses
// `c.isalnum() or c in "-_."`. JS has no isalnum(); `/^[\p{L}\p{N}\-_.]+$/u`
// covers the same alphanumeric-plus-three-symbols set with full Unicode letters
// and numbers (Python str.isalnum() is also Unicode-aware).
export const ALIAS_RE = /^[\p{L}\p{N}\-_.]+$/u;

// duration: a number followed by one of d/h/m/s — session.py regex ^\d+[dhms]$.
export const DURATION_RE = /^\d+[dhms]$/;

export const ALIAS_ERROR =
  'Name may only contain letters, numbers, dash, underscore, dot (max 128).';
export const DURATION_ERROR = 'Expiry must look like 90d, 24h, or 30m.';
export const CREATE_502_ERROR = "Couldn't create the key. Try again in a moment.";
// The shown-once notice, split so the key-visibility clause renders bold inline
// (mirrors LiteLLM's own dialog). The three parts concatenate to the full copy.
export const SHOWN_ONCE_WARNING_PRE =
  'Save this secret key somewhere safe and accessible. For security reasons, ';
export const SHOWN_ONCE_WARNING_EMPHASIS = "you won't be able to view it again.";
export const SHOWN_ONCE_WARNING_POST =
  " If you lose it, you'll need to generate a new one.";

/**
 * Validate a trimmed alias against the session.py rules. Empty is allowed (the
 * field is optional => omitted). Otherwise it must be at most 128 chars and
 * match ALIAS_RE. Returns an error string or null.
 */
export function validateAlias(value: string): string | null {
  if (value === '') return null; // optional
  if (value.length > 128 || !ALIAS_RE.test(value)) return ALIAS_ERROR;
  return null;
}

/**
 * Validate a trimmed duration. Empty is allowed (no expiry). Otherwise it must
 * match the LiteLLM duration shape mirrored from session.py. Returns an error
 * string or null.
 */
export function validateDuration(value: string): string | null {
  if (value === '') return null; // optional — no expiry
  if (!DURATION_RE.test(value)) return DURATION_ERROR;
  return null;
}
