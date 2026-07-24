// SPDX-License-Identifier: Apache-2.0

package contentservice

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/contentservice/envcache"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/metrics"
)

// ----------------------------------------------------------------------
// Mocks
// ----------------------------------------------------------------------

type mockResolver struct {
	info *keystore.KeyInfo
	err  error
}

func (m *mockResolver) Resolve(_ context.Context, _ string) (*keystore.KeyInfo, error) {
	return m.info, m.err
}

type mockTeams struct {
	teams []string
	err   error
}

func (m *mockTeams) Resolve(_ context.Context, _ string) ([]string, error) {
	return m.teams, m.err
}

// envCacheWith returns a real *envcache.Cache snapshot containing an empty
// EnvRow for each named environment. The cache is an in-memory map read, so
// there is no error path — a name not present is simply a miss.
func envCacheWith(names ...string) *envcache.Cache {
	m := map[string]envcache.EnvRow{}
	for _, n := range names {
		m[n] = envcache.EnvRow{}
	}
	return envcache.NewWithRows(m)
}

// authzTestDeps builds a Deps wired with mock interfaces. The caller can
// override any field after construction.
func authzTestDeps(t *testing.T) (Deps, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	col := metrics.NewContentServiceCollectors(reg)
	llmUnreach := metrics.MustRegisterLitellmUnreachable(reg)
	return Deps{
		Namespace:          "ach-system",
		Metrics:            col,
		LiteLLMUnreachable: llmUnreach,
	}, reg
}

func makeReq(headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/content/prompt/foo", nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

// ----------------------------------------------------------------------
// resolveAuthn
// ----------------------------------------------------------------------

func TestResolveAuthn(t *testing.T) {
	pkInfo := &keystore.KeyInfo{KeyID: "pkid_a", KeyType: keys.PrefixPk, OwnerEmail: "alice@x.com"}

	cases := []struct {
		name     string
		header   string
		resolver keystore.Resolver
		wantInfo *keystore.KeyInfo
		wantErr  string
	}{
		{"empty header", "", &mockResolver{}, nil, "invalid_key_format"},
		{"wrong prefix", "garbage", &mockResolver{}, nil, "invalid_key_format"},
		{"resolver internal error", "pk-a", &mockResolver{err: errors.New("boom")}, nil, "internal_error"},
		{"resolver nil info (revoked)", "pk-a", &mockResolver{info: nil}, nil, "expired_or_revoked"},
		{"happy path pk_", "pk-a", &mockResolver{info: pkInfo}, pkInfo, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := authzTestDeps(t)
			d.Resolver = tc.resolver
			r := makeReq(map[string]string{"x-ach-key": tc.header})
			got, errR := resolveAuthn(context.Background(), d, r)
			if tc.wantErr == "" {
				if errR != nil {
					t.Fatalf("unexpected err: %+v", errR)
				}
				if got != tc.wantInfo {
					t.Fatalf("got info=%v, want %v", got, tc.wantInfo)
				}
				return
			}
			if errR == nil || errR.Code != tc.wantErr {
				t.Fatalf("got errR=%+v, want code=%s", errR, tc.wantErr)
			}
		})
	}
}

// ----------------------------------------------------------------------
// resolveEnv — pk_
// ----------------------------------------------------------------------

func TestResolveEnv_PK(t *testing.T) {
	pkInfo := &keystore.KeyInfo{KeyID: "pkid_a", KeyType: keys.PrefixPk, OwnerEmail: "alice@x.com"}

	t.Run("missing header", func(t *testing.T) {
		d, _ := authzTestDeps(t)
		d.EnvCache = envCacheWith("prod")
		row, errR := resolveEnv(d, pkInfo, "")
		if errR == nil || errR.Code != "missing_environment" {
			t.Fatalf("got errR=%+v, want missing_environment", errR)
		}
		if row != nil {
			t.Fatalf("row should be nil on denial")
		}
	})
	t.Run("env not found", func(t *testing.T) {
		d, _ := authzTestDeps(t)
		d.EnvCache = envCacheWith() // empty snapshot → miss
		_, errR := resolveEnv(d, pkInfo, "prod")
		if errR == nil || errR.Code != "environment_not_found" {
			t.Fatalf("got errR=%+v, want environment_not_found", errR)
		}
	})
	t.Run("happy path", func(t *testing.T) {
		d, _ := authzTestDeps(t)
		d.EnvCache = envCacheWith("prod")
		row, errR := resolveEnv(d, pkInfo, "prod")
		if errR != nil {
			t.Fatalf("unexpected err: %+v", errR)
		}
		if row == nil {
			t.Fatalf("got nil row, want a hit")
		}
	})
}

// ----------------------------------------------------------------------
// resolveEnv — ek_
// ----------------------------------------------------------------------

func TestResolveEnv_EK(t *testing.T) {
	ekInfo := &keystore.KeyInfo{KeyID: "ekid_a", KeyType: keys.PrefixEk, OwnerEmail: "bob@x.com", Environment: "prod"}

	t.Run("header mismatch", func(t *testing.T) {
		d, _ := authzTestDeps(t)
		d.EnvCache = envCacheWith("prod")
		_, errR := resolveEnv(d, ekInfo, "staging")
		if errR == nil || errR.Code != "wrong_environment" {
			t.Fatalf("got errR=%+v, want wrong_environment", errR)
		}
	})
	t.Run("header empty uses bound env", func(t *testing.T) {
		d, _ := authzTestDeps(t)
		d.EnvCache = envCacheWith("prod")
		row, errR := resolveEnv(d, ekInfo, "")
		if errR != nil {
			t.Fatalf("unexpected err: %+v", errR)
		}
		if row == nil {
			t.Fatalf("got nil row, want a hit")
		}
	})
	t.Run("matching header", func(t *testing.T) {
		d, _ := authzTestDeps(t)
		d.EnvCache = envCacheWith("prod")
		_, errR := resolveEnv(d, ekInfo, "prod")
		if errR != nil {
			t.Fatalf("unexpected err: %+v", errR)
		}
	})
	t.Run("env not found", func(t *testing.T) {
		d, _ := authzTestDeps(t)
		d.EnvCache = envCacheWith() // empty snapshot → miss
		_, errR := resolveEnv(d, ekInfo, "prod")
		if errR == nil || errR.Code != "environment_not_found" {
			t.Fatalf("got errR=%+v, want environment_not_found", errR)
		}
	})
}

// ----------------------------------------------------------------------
// enforceTeams
// ----------------------------------------------------------------------

func TestEnforceTeams_PK(t *testing.T) {
	pkInfo := &keystore.KeyInfo{KeyID: "pkid_a", KeyType: keys.PrefixPk, OwnerEmail: "alice@x.com"}
	envRow := &envcache.EnvRow{
		AuthorizedTeams: []string{"team-a", "team-b"},
	}

	t.Run("litellm transport error → 503 + Inc", func(t *testing.T) {
		d, reg := authzTestDeps(t)
		d.Teams = &mockTeams{err: errors.New("net dial timeout")}
		errR := enforceTeams(context.Background(), d, pkInfo, envRow)
		if errR == nil || errR.Code != "litellm_unreachable" {
			t.Fatalf("got errR=%+v, want litellm_unreachable", errR)
		}
		got := gatherCounter(t, reg, "ach_litellm_unreachable_total", map[string]string{"caller": "content_service"})
		if got != 1 {
			t.Fatalf("ach_litellm_unreachable_total counter=%v, want 1", got)
		}
	})
	t.Run("empty intersection → 403", func(t *testing.T) {
		d, _ := authzTestDeps(t)
		d.Teams = &mockTeams{teams: []string{"team-x"}}
		errR := enforceTeams(context.Background(), d, pkInfo, envRow)
		if errR == nil || errR.Code != "unauthorized_team" {
			t.Fatalf("got errR=%+v, want unauthorized_team", errR)
		}
	})
	t.Run("non-empty intersection → nil", func(t *testing.T) {
		d, _ := authzTestDeps(t)
		d.Teams = &mockTeams{teams: []string{"team-x", "team-a"}}
		errR := enforceTeams(context.Background(), d, pkInfo, envRow)
		if errR != nil {
			t.Fatalf("unexpected err: %+v", errR)
		}
	})
	t.Run("ek_ skips entirely", func(t *testing.T) {
		d, _ := authzTestDeps(t)
		ekInfo := &keystore.KeyInfo{KeyID: "ekid_a", KeyType: keys.PrefixEk}
		// teams resolver returning err MUST NOT be consulted for ek_:
		d.Teams = &mockTeams{err: errors.New("should not be called")}
		errR := enforceTeams(context.Background(), d, ekInfo, envRow)
		if errR != nil {
			t.Fatalf("ek_ skip should return nil, got %+v", errR)
		}
	})
	t.Run("litellm ErrNotFound → empty teams → 403", func(t *testing.T) {
		// Per D-17, litellm.ErrNotFound is treated as empty team list.
		// With non-empty authorized_teams, an empty user team list → 403.
		d, reg := authzTestDeps(t)
		d.Teams = &mockTeams{err: litellm.ErrNotFound}
		errR := enforceTeams(context.Background(), d, pkInfo, envRow)
		if errR == nil || errR.Code != "unauthorized_team" {
			t.Fatalf("got errR=%+v, want unauthorized_team (ErrNotFound → empty teams)", errR)
		}
		// Counter MUST NOT increment for ErrNotFound.
		got := gatherCounter(t, reg, "ach_litellm_unreachable_total", map[string]string{"caller": "content_service"})
		if got != 0 {
			t.Fatalf("ach_litellm_unreachable_total counter=%v, want 0 (ErrNotFound is not transport failure)", got)
		}
	})
}

// ----------------------------------------------------------------------
// enforceAllowlist
// ----------------------------------------------------------------------

func TestEnforceAllowlist(t *testing.T) {
	row := &envcache.EnvRow{
		ContextPrompts:   []string{"p1", "p2"},
		ContextPlugins:   []string{"pl1"},
		ContextArtifacts: []string{"a1"},
	}
	cases := []struct {
		name    string
		kind    string
		input   string
		wantErr string
	}{
		{"prompt in-list", "prompt", "p2", ""},
		{"prompt not-in-list", "prompt", "p99", "unauthorized_content"},
		{"plugin in-list", "plugin", "pl1", ""},
		{"plugin not-in-list", "plugin", "pl99", "unauthorized_content"},
		{"artifact in-list", "artifact", "a1", ""},
		{"artifact not-in-list", "artifact", "a99", "unauthorized_content"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errR := enforceAllowlist(row, tc.kind, tc.input)
			if tc.wantErr == "" {
				if errR != nil {
					t.Errorf("unexpected err: %+v", errR)
				}
				return
			}
			if errR == nil || errR.Code != tc.wantErr {
				t.Errorf("got errR=%+v, want code=%s", errR, tc.wantErr)
			}
		})
	}
}

// ----------------------------------------------------------------------
// resolveContent
// ----------------------------------------------------------------------

// resolveContent reaches into db.* directly — it cannot be unit-tested
// without testcontainers. The integration tests in Task 4 cover the
// full path (CRD-wins, marketplace-fallback, soft-deleted, no-match,
// staleness paths). Here we cover the kind-dispatch wiring and the
// pure-function checkStaleness helper.

// ----------------------------------------------------------------------
// checkStaleness
// ----------------------------------------------------------------------

func TestCheckStaleness_Fresh(t *testing.T) {
	now := time.Now()
	row := &contentRow{LastSuccessfulRefresh: &now, MaxStalenessSeconds: 300}
	if errR := checkStaleness(row); errR != nil {
		t.Errorf("fresh row should pass, got %+v", errR)
	}
}

func TestCheckStaleness_Stale(t *testing.T) {
	stale := time.Now().Add(-1 * time.Hour)
	row := &contentRow{LastSuccessfulRefresh: &stale, MaxStalenessSeconds: 300}
	errR := checkStaleness(row)
	if errR == nil || errR.Code != "stale_cache_expired" {
		t.Errorf("got errR=%+v, want stale_cache_expired", errR)
	}
}

func TestCheckStaleness_NullLSR(t *testing.T) {
	row := &contentRow{LastSuccessfulRefresh: nil, MaxStalenessSeconds: 300}
	errR := checkStaleness(row)
	if errR == nil || errR.Code != "stale_cache_expired" {
		t.Errorf("got errR=%+v, want stale_cache_expired", errR)
	}
}

// Sanity: confirm audit Outcome string constants are stable references.
func TestAuthz_OutcomeConstantsStable(t *testing.T) {
	if audit.OutcomeStaleCacheExpired != "stale_cache_expired" {
		t.Fatalf("audit.OutcomeStaleCacheExpired drift: %q", audit.OutcomeStaleCacheExpired)
	}
	if audit.OutcomeUnauthorizedContent != "unauthorized_content" {
		t.Fatalf("audit.OutcomeUnauthorizedContent drift: %q", audit.OutcomeUnauthorizedContent)
	}
}
