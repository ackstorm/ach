// SPDX-License-Identifier: Apache-2.0

package inventory

import (
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

func ptrTime(t time.Time) *time.Time { return &t }
func ptrStr(s string) *string        { return &s }

// TestContentSync covers the three staleness states.
func TestContentSync(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)

	t.Run("never when last is nil", func(t *testing.T) {
		state, reason := contentSync(nil, 600, now)
		if state != syncNever || reason != "" {
			t.Errorf("got (%q,%q), want (never,)", state, reason)
		}
	})

	t.Run("fresh within window", func(t *testing.T) {
		last := now.Add(-5 * time.Minute) // 300s ago, window 600s
		state, reason := contentSync(ptrTime(last), 600, now)
		if state != syncFresh || reason != "" {
			t.Errorf("got (%q,%q), want (fresh,)", state, reason)
		}
	})

	t.Run("stale past window with humanized age", func(t *testing.T) {
		last := now.Add(-3 * time.Hour) // window 600s → ~2h50m over
		state, reason := contentSync(ptrTime(last), 600, now)
		if state != syncStale {
			t.Fatalf("state: got %q, want STALE", state)
		}
		if reason != "2h over" {
			t.Errorf("reason: got %q, want '2h over'", reason)
		}
	})
}

// TestMarkFalseGreen: only bare fresh is upgraded.
func TestMarkFalseGreen(t *testing.T) {
	cases := map[string]string{
		syncFresh:     syncFreshFalseGreen,
		syncStale:     syncStale,
		syncNever:     syncNever,
		syncProjected: syncProjected,
	}
	for in, want := range cases {
		if got := markFalseGreen(in); got != want {
			t.Errorf("markFalseGreen(%q): got %q, want %q", in, got, want)
		}
	}
}

// TestOptTime: epoch/zero → nil; real time → pointer.
func TestOptTime(t *testing.T) {
	if optTime(time.Unix(0, 0)) != nil {
		t.Error("unix epoch should map to nil (never)")
	}
	if optTime(time.Time{}) != nil {
		t.Error("zero time should map to nil (never)")
	}
	realTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := optTime(realTime); got == nil || !got.Equal(realTime) {
		t.Errorf("real time should round-trip, got %v", got)
	}
}

func TestRFC3339OrEmpty(t *testing.T) {
	if rfc3339OrEmpty(time.Time{}) != "" {
		t.Error("zero time should render empty")
	}
	if rfc3339OrEmpty(time.Unix(0, 0)) != "" {
		t.Error("epoch should render empty")
	}
	tm := time.Date(2026, 6, 4, 9, 30, 0, 0, time.UTC)
	if got := rfc3339OrEmpty(tm); got != "2026-06-04T09:30:00Z" {
		t.Errorf("got %q", got)
	}
}

// TestPromptArtifactFalseGreen: fresh prompts/artifacts render fresh*, STALE
// stays bare.
func TestPromptArtifactFalseGreen(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	freshAgo := ptrTime(now.Add(-1 * time.Minute))

	pv := promptRowToView(db.PromptRow{
		Namespace: "ach", Name: "greeting", ResourceVersion: "7",
		LastSuccessfulRefresh: freshAgo, MaxStalenessSeconds: 600,
		ContentType: ptrStr("text/markdown"), UpdatedAt: now,
	}, now)
	if pv.Sync != syncFreshFalseGreen {
		t.Errorf("prompt fresh sync: got %q, want fresh*", pv.Sync)
	}
	if pv.Version != "7" {
		t.Errorf("prompt version: got %q, want 7", pv.Version)
	}
	if pv.Extra["contentType"] != "text/markdown" {
		t.Errorf("prompt contentType extra: got %q", pv.Extra["contentType"])
	}

	av := artifactRowToView(db.ArtifactRow{
		Namespace: "ach", Name: "logo", ResourceVersion: "3", Scope: "object",
		LastSuccessfulRefresh: ptrTime(now.Add(-10 * time.Hour)), MaxStalenessSeconds: 600,
		UpdatedAt: now,
	}, now)
	if av.Sync != syncStale {
		t.Errorf("artifact stale sync: got %q, want STALE (no asterisk)", av.Sync)
	}
	if av.Extra["scope"] != "object" {
		t.Errorf("artifact scope extra: got %q", av.Extra["scope"])
	}
}

// TestPluginViewBareFresh: plugins are truly content-gated → bare fresh.
func TestPluginViewBareFresh(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	v := pluginRowToView(db.PluginRow{
		Namespace: "ach", Name: "caveman", ResourceVersion: "12",
		LastSuccessfulRefresh: ptrTime(now.Add(-1 * time.Minute)), MaxStalenessSeconds: 600,
		UpdatedAt: now,
	}, now)
	if v.Sync != syncFresh {
		t.Errorf("plugin sync: got %q, want fresh (no asterisk)", v.Sync)
	}
	if v.Kind != "plugin" || v.Namespace != "ach" || v.Name != "caveman" {
		t.Errorf("plugin identity fields wrong: %+v", v)
	}
}

// TestBIPView: version = observed_generation, sync = projected, target extra.
func TestBIPView(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	v := bipRowToView(db.BIPRow{
		Namespace: "ach", Name: "mcp-allow", TargetKind: "MCPServer", TargetName: "echo",
		ForwardIdentityJWT: true, ObservedGeneration: 4, ResourceVersion: "99",
		UpdatedAt: now, Origin: "cr", Locked: true,
	})
	if v.Sync != syncProjected {
		t.Errorf("bip sync: got %q, want projected", v.Sync)
	}
	if v.Version != "4" {
		t.Errorf("bip version: got %q, want 4 (observed_generation)", v.Version)
	}
	if v.Extra["target"] != "MCPServer/echo" {
		t.Errorf("bip target: got %q", v.Extra["target"])
	}
	if v.Extra["forwardJWT"] != "true" {
		t.Errorf("bip forwardJWT: got %q", v.Extra["forwardJWT"])
	}
	if !v.Locked || v.Origin != "cr" {
		t.Errorf("bip origin/locked: %+v", v)
	}
}

// TestLitellmConnView + marketplace + external-ref sanity.
func TestOtherKindViews(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)

	lc := litellmConnToView(db.LiteLLMConnectionRow{
		Namespace: "ach", Name: "default", Endpoint: "http://litellm:4000",
		ResourceVersion: "1", UpdatedAt: now, Origin: "cr", Locked: true,
	})
	if lc.Sync != syncProjected || lc.Extra["endpoint"] != "http://litellm:4000" {
		t.Errorf("litellm view wrong: %+v", lc)
	}

	mp := marketplacePluginToView(db.MarketplacePlugin{
		MarketplaceName: "anthropic", Name: "foo", UpstreamRev: "abc123",
		LastSuccessfulRefresh: now.Add(-1 * time.Minute), MaxStalenessSeconds: 3600,
	}, now)
	if mp.Sync != syncFresh {
		t.Errorf("marketplace fresh sync: got %q, want fresh", mp.Sync)
	}
	if mp.Version != "abc123" || mp.Namespace != "anthropic" {
		t.Errorf("marketplace fields wrong: %+v", mp)
	}

	// Never-refreshed marketplace (epoch) → never.
	mpNever := marketplacePluginToView(db.MarketplacePlugin{
		MarketplaceName: "x", Name: "y", LastSuccessfulRefresh: time.Unix(0, 0), MaxStalenessSeconds: 60,
	}, now)
	if mpNever.Sync != syncNever {
		t.Errorf("marketplace never sync: got %q, want never", mpNever.Sync)
	}

	er := externalRefToView(db.ExternalRef{
		Kind: "plugin", Name: "caveman", UpstreamRev: "deadbeef",
		LastSuccessfulRefresh: now.Add(-30 * time.Second), MaxStalenessSeconds: 600,
	}, now)
	if er.Sync != syncFresh || er.Extra["refKind"] != "plugin" || er.Version != "deadbeef" {
		t.Errorf("external-ref view wrong: %+v", er)
	}
}
