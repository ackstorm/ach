// SPDX-License-Identifier: Apache-2.0

// Decorative banner for bare `ach-cli` (no subcommand) — printed by
// runRoot before the help text, gated on stdout being a TTY so it never
// lands in a pipe/CI and cannot pollute machine-readable output. It is
// shown ONLY on the bare invocation: `--help`, `--version`, and every
// subcommand (login included) short-circuit before runRoot.
//
// The preferred banner is UTF-8 (box-drawing `─`/bullet `●` for the hub
// motif). A pure-ASCII fallback (`-`/`o`) is used when the locale is not
// UTF-8, so a `LANG=C` terminal does not render mojibake. The ASCII
// fallback preserves column widths (`──`→`--`, `●`→`o`) so the figlet
// letters stay aligned.

package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// bannerUTF8 is the preferred banner. The hub spokes use ASCII `|`/`\`/`/`;
// only the hub row (`──`) and center (`●`) are box-drawing/bullet glyphs.
// Spacing is FROZEN — the row-3 `──(●)──` left edge intentionally sits one
// column right of the spokes above/below. Do not "fix" the asymmetry.
const bannerUTF8 = "" +
	"\n" +
	"\\  |  /              _\n" +
	" \\ | /     __ _  ___| |__\n" +
	"──(●)──   / _` |/ __| '_ \\\n" +
	" / | \\   | (_| | (__| | | |\n" +
	"/  |  \\   \\__,_|\\___|_| |_|\n" +
	"       Agent Capability Hub\n" +
	"\n"

// bannerASCII is the LANG=C / non-UTF-8 fallback: `──`→`--`, `●`→`o`. Same
// column widths as bannerUTF8 so the letters stay aligned.
const bannerASCII = "" +
	"\n" +
	"\\  |  /              _\n" +
	" \\ | /     __ _  ___| |__\n" +
	"--(o)--   / _` |/ __| '_ \\\n" +
	" / | \\   | (_| | (__| | | |\n" +
	"/  |  \\   \\__,_|\\___|_| |_|\n" +
	"       Agent Capability Hub\n" +
	"\n"

// writeBanner emits the locale-appropriate banner to w. It does NOT gate
// on TTY/first-run — callers decide whether to call it (see shouldBanner).
func writeBanner(w io.Writer) {
	b := bannerASCII
	if localeIsUTF8() {
		b = bannerUTF8
	}
	_, _ = fmt.Fprint(w, b)
}

// localeIsUTF8 reports whether the active locale advertises UTF-8, by the
// POSIX precedence LC_ALL > LC_CTYPE > LANG. Empty/unset → assume NOT
// UTF-8 (safe: fall back to ASCII rather than risk mojibake).
func localeIsUTF8() bool {
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := os.Getenv(k); v != "" {
			u := strings.ToUpper(v)
			return strings.Contains(u, "UTF-8") || strings.Contains(u, "UTF8")
		}
	}
	return false
}

// isTerminal reports whether v is an *os.File backed by a character
// device (a TTY). Buffers, pipes, and non-files → false. Used to gate
// the decorative banner and the interactive pre-open prompt so neither
// fires under `go test`, CI, or a redirected stdin/stdout.
func isTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
