// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestLocaleIsUTF8 covers the POSIX precedence (LC_ALL > LC_CTYPE > LANG)
// and the safe default (unset → false → ASCII fallback).
func TestLocaleIsUTF8(t *testing.T) {
	cases := []struct {
		name               string
		lcAll, lcCtype, lc string
		want               bool
	}{
		{"lang-utf8", "", "", "en_US.UTF-8", true},
		{"lang-utf8-lowercase", "", "", "en_US.utf8", true},
		{"lang-C", "", "", "C", false},
		{"lang-posix", "", "", "POSIX", false},
		{"all-unset", "", "", "", false},
		{"lc_all-wins-utf8", "en_US.UTF-8", "C", "C", true},
		{"lc_all-wins-ascii", "C", "en_US.UTF-8", "en_US.UTF-8", false},
		{"lc_ctype-over-lang", "", "en_US.UTF-8", "C", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("LC_ALL", c.lcAll)
			t.Setenv("LC_CTYPE", c.lcCtype)
			t.Setenv("LANG", c.lc)
			if got := localeIsUTF8(); got != c.want {
				t.Errorf("localeIsUTF8() = %v; want %v", got, c.want)
			}
		})
	}
}

// TestIsTerminal_NonFile asserts buffers and pipes (the shapes tests and
// CI use) are never treated as terminals, so the banner + pre-open prompt
// stay gated off.
func TestIsTerminal_NonFile(t *testing.T) {
	if isTerminal(&bytes.Buffer{}) {
		t.Error("bytes.Buffer reported as terminal")
	}
	if isTerminal(nil) {
		t.Error("nil reported as terminal")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	if isTerminal(r) {
		t.Error("os.Pipe read end reported as terminal")
	}
}

// TestWriteBanner_Variant asserts the locale switch picks the right glyph
// set and both variants carry the tagline.
func TestWriteBanner_Variant(t *testing.T) {
	t.Run("utf8", func(t *testing.T) {
		t.Setenv("LC_ALL", "")
		t.Setenv("LC_CTYPE", "")
		t.Setenv("LANG", "en_US.UTF-8")
		var b bytes.Buffer
		writeBanner(&b)
		out := b.String()
		if !strings.Contains(out, "●") {
			t.Errorf("utf8 banner missing bullet ●: %q", out)
		}
		if !strings.Contains(out, "Agent Capability Hub") {
			t.Errorf("utf8 banner missing tagline: %q", out)
		}
	})
	t.Run("ascii", func(t *testing.T) {
		t.Setenv("LC_ALL", "")
		t.Setenv("LC_CTYPE", "")
		t.Setenv("LANG", "C")
		var b bytes.Buffer
		writeBanner(&b)
		out := b.String()
		if strings.Contains(out, "●") || strings.Contains(out, "─") {
			t.Errorf("ascii banner leaked UTF-8 glyphs: %q", out)
		}
		if !strings.Contains(out, "(o)") {
			t.Errorf("ascii banner missing (o) hub: %q", out)
		}
		if !strings.Contains(out, "Agent Capability Hub") {
			t.Errorf("ascii banner missing tagline: %q", out)
		}
	})
}

// TestPromptPreOpen_Parse covers the three-way menu mapping and the
// Enter/garbage → default-open behavior.
func TestPromptPreOpen_Parse(t *testing.T) {
	cases := []struct {
		in   string
		want openAction
	}{
		{"1\n", actOpen},
		{"2\n", actPrint},
		{"3\n", actCancel},
		{"\n", actOpen},       // bare Enter → default
		{"  2  \n", actPrint}, // trimmed
		{"banana\n", actOpen}, // unrecognized → safe default
		{"", actOpen},         // EOF (no line) → default
	}
	for _, c := range cases {
		var out bytes.Buffer
		got := promptPreOpen(strings.NewReader(c.in), &out)
		if got != c.want {
			t.Errorf("promptPreOpen(%q) = %d; want %d", c.in, got, c.want)
		}
		if !strings.Contains(out.String(), "Cancel") {
			t.Errorf("prompt did not render the menu for input %q", c.in)
		}
	}
}

// TestRoot_BareBannerGatedAndHelp asserts that bare `ach-cli` on a
// non-TTY (buffer) shows the help WITHOUT the banner (gated off), proving
// runRoot wiring + the no-pipe-pollution gate. The banner-on-TTY path is
// covered by writeBanner + isTerminal unit tests.
func TestRoot_BareBannerGatedAndHelp(t *testing.T) {
	var out, errb bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errb)
	rootCmd.SetArgs([]string{})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("bare ach-cli execute: %v", err)
	}
	combined := out.String() + errb.String()
	// Use a banner-only marker (the hub spokes) — NOT the tagline, which
	// also appears in rootCmd.Long help text.
	if strings.Contains(combined, `\  |  /`) {
		t.Errorf("banner leaked into non-TTY output: %q", combined)
	}
	if !strings.Contains(combined, "login") {
		t.Errorf("help missing subcommands listing: %q", combined)
	}
}

// TestResolvePreOpen_Gating asserts the non-prompt arms: --no-browser →
// print, and non-TTY → auto-open (never blocks on an unanswerable prompt).
func TestResolvePreOpen_Gating(t *testing.T) {
	// --no-browser overrides everything.
	if got := resolvePreOpen(true, strings.NewReader("1\n"), &bytes.Buffer{}); got != actPrint {
		t.Errorf("no-browser → %d; want actPrint", got)
	}
	// Non-TTY (buffers) → actOpen without consuming/parsing stdin.
	in := strings.NewReader("3\n") // would be cancel IF prompted
	if got := resolvePreOpen(false, in, &bytes.Buffer{}); got != actOpen {
		t.Errorf("non-tty → %d; want actOpen (no prompt)", got)
	}
}
