// key-validation.ts — client-side name validation + expiry-preset conversion
// for the create-key modal.
//
// `validateName`'s character rules are lifted verbatim from the alitellm-auth
// alias validator (src/ui/create-key.js / session.py). Unlike the old alias,
// ACH's `name` is REQUIRED (internal/platformapi/envkeys/handler.go:204 — a
// 400 invalid_argument when empty), so empty input is now an error, not "omit
// the field".
//
// Duration free-text input is gone — ACH's CreateRequest takes an optional
// absolute `expires_at` (RFC3339), never a LiteLLM duration string. The modal
// offers a fixed preset (never/7d/30d/90d); `presetToExpiresAt` converts the
// pick to that absolute instant client-side (no validation needed — a select,
// not free text).

// name safe chars: alphanumeric + dash/underscore/dot. session.py used
// `c.isalnum() or c in "-_."`. JS has no isalnum(); `/^[\p{L}\p{N}\-_.]+$/u`
// covers the same alphanumeric-plus-three-symbols set with full Unicode letters
// and numbers.
export const ALIAS_RE = /^[\p{L}\p{N}\-_.]+$/u;

export const NAME_REQUIRED_ERROR = 'Name is required.';
export const ALIAS_ERROR =
  'Name may only contain letters, numbers, dash, underscore, dot (max 128).';
export const CREATE_502_ERROR = "Couldn't create the key. Try again in a moment.";
// The shown-once notice, split so the key-visibility clause renders bold inline
// (mirrors LiteLLM's own dialog). The three parts concatenate to the full copy.
export const SHOWN_ONCE_WARNING_PRE =
  'Save this secret key somewhere safe and accessible. For security reasons, ';
export const SHOWN_ONCE_WARNING_EMPHASIS = "you won't be able to view it again.";
export const SHOWN_ONCE_WARNING_POST =
  " If you lose it, you'll need to generate a new one.";

/**
 * Validate a trimmed key name. REQUIRED (ACH's `name` field has no default —
 * an empty body 400s). Otherwise must be at most 128 chars and match ALIAS_RE.
 * Returns an error string or null.
 */
export function validateName(value: string): string | null {
  if (value === '') return NAME_REQUIRED_ERROR;
  if (value.length > 128 || !ALIAS_RE.test(value)) return ALIAS_ERROR;
  return null;
}

/** The four expiry choices the create-key modal offers. */
export type ExpiryPreset = 'never' | '7d' | '30d' | '90d';

export const EXPIRY_PRESETS: ReadonlyArray<{ value: ExpiryPreset; label: string }> = [
  { value: 'never', label: 'Never' },
  { value: '7d', label: '7 days' },
  { value: '30d', label: '30 days' },
  { value: '90d', label: '90 days' },
];

const PRESET_DAYS: Record<Exclude<ExpiryPreset, 'never'>, number> = {
  '7d': 7,
  '30d': 30,
  '90d': 90,
};

const DAY_MS = 24 * 60 * 60 * 1000;

/**
 * Convert an expiry preset to an absolute RFC3339 UTC instant (Go's
 * time.Parse(time.RFC3339, …) accepts the fractional-second `Date#toISOString`
 * form). `'never'` -> null (perpetual key, the field is omitted from the
 * request body). `now` is injectable so it is unit-testable without a clock.
 */
export function presetToExpiresAt(preset: ExpiryPreset, now: Date = new Date()): string | null {
  if (preset === 'never') return null;
  return new Date(now.getTime() + PRESET_DAYS[preset] * DAY_MS).toISOString();
}
