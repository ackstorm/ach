// SPDX-License-Identifier: Apache-2.0

package httpclient

import (
	"net/http"
	"sort"
	"strings"

	"github.com/ackstorm/ach/internal/keys"
)

// achKeyHeader is the canonical form of the ACH credential carrier
// header. The CLI sets it via http.Header.Set, which auto-canonicalizes
// the name; HeaderDump matches against this exact string after lookup.
const achKeyHeader = "X-Ach-Key"

// Redact reduces a pk-/ek- plaintext to "<prefix>-***" for safe
// inclusion in `--verbose` header dumps (CLI-04 / D-15). Values that
// don't carry the pk-/ek- prefix collapse to the literal "redacted"
// marker — defensive default that never echoes unrecognized values.
//
// Distinct from config.Mask: Mask renders the operator-friendly
// `<prefix>-****<last-4>` (for `ach config show`); Redact renders the
// stricter "<prefix>-***" (for header dumps, where last-4 is excess
// fingerprint).
//
// Uses keys.PkBearerPrefix / keys.EkBearerPrefix so the prefix string
// is defined exactly once — no drift hazard.
func Redact(value string) string {
	switch {
	case strings.HasPrefix(value, keys.PkBearerPrefix):
		return "pk-***"
	case strings.HasPrefix(value, keys.EkBearerPrefix):
		return "ek-***"
	default:
		return "redacted"
	}
}

// HeaderDump returns a deterministic multi-line `Key: value` dump of
// http.Header with the x-ach-key value run through Redact. Other
// headers pass through verbatim. Lines are sorted by the canonical
// header name so repeated `--verbose` invocations produce identical
// stderr output.
//
// Multi-value headers join their values with ", " — the comma form
// matches how net/http would serialize them on the wire.
func HeaderDump(h http.Header) string {
	if len(h) == 0 {
		return ""
	}
	names := make([]string, 0, len(h))
	for k := range h {
		names = append(names, k)
	}
	sort.Strings(names)
	var sb strings.Builder
	for _, name := range names {
		vs := h.Values(name)
		joined := strings.Join(vs, ", ")
		if strings.EqualFold(name, achKeyHeader) {
			// Redact each value individually; never echo plaintext
			// even if a buggy caller stacked two pk_ values into
			// one header.
			redacted := make([]string, 0, len(vs))
			for _, v := range vs {
				redacted = append(redacted, Redact(v))
			}
			joined = strings.Join(redacted, ", ")
		}
		sb.WriteString(name)
		sb.WriteString(": ")
		sb.WriteString(joined)
		sb.WriteString("\n")
	}
	return sb.String()
}
