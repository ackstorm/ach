// SPDX-License-Identifier: Apache-2.0

package teams

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/ackstorm/ach/internal/litellm"
)

// fakeLiteLLM is a minimal litellm.Client implementation for the
// LookupCallerTeams unit tests. Only UserInfoByEmail is exercised; the
// other 9 interface methods exist solely to satisfy the compile-time
// canary `var _ litellm.Client = (*fakeLiteLLM)(nil)` below.
type fakeLiteLLM struct {
	userInfo func(email string) (*litellm.UserInfo, error)
	calls    atomic.Int64
}

func (f *fakeLiteLLM) UserInfoByEmail(_ context.Context, email string) (*litellm.UserInfo, error) {
	f.calls.Add(1)
	return f.userInfo(email)
}

// Stubs for the rest of the litellm.Client interface — return zero
// values; LookupCallerTeams must NOT invoke any of these.
func (f *fakeLiteLLM) DeleteAccessGroup(_ context.Context, _ string) error { return nil }
func (f *fakeLiteLLM) DeleteTag(_ context.Context, _ string) error         { return nil }
func (f *fakeLiteLLM) ListModels(_ context.Context) ([]litellm.ModelInfoResponse, error) {
	return nil, nil
}
func (f *fakeLiteLLM) ListMCPServers(_ context.Context) ([]litellm.MCPServerEntry, error) {
	return nil, nil
}
func (f *fakeLiteLLM) ListA2AAgents(_ context.Context) ([]litellm.AgentEntry, error) {
	return nil, nil
}
func (f *fakeLiteLLM) ListUserKeys(_ context.Context, _ string) ([]litellm.UserKeyInfo, error) {
	return nil, nil
}
func (f *fakeLiteLLM) RevokeKey(_ context.Context, _ string) error { return nil }
func (f *fakeLiteLLM) UserNew(_ context.Context, _ *litellm.UserNewRequest) (*litellm.UserInfo, error) {
	return nil, nil
}
func (f *fakeLiteLLM) TeamMemberAdd(_ context.Context, _, _, _ string) error { return nil }
func (f *fakeLiteLLM) KeyGenerate(_ context.Context, _ *litellm.KeyGenerateRequest) (*litellm.KeyGenerateResponse, error) {
	return nil, nil
}

// Compile-time canary — fakeLiteLLM satisfies litellm.Client. If
// litellm.Client widens, this test file MUST add the new stub to keep
// the package building (mirrors the Phase 02-01 fake catch-up
// discipline established in 03-01-SUMMARY.md Rule-3 ripple).
var _ litellm.Client = (*fakeLiteLLM)(nil)

// TestLookupCallerTeamsHappy — mock returns *UserInfo with two teams;
// the helper returns those teams verbatim.
func TestLookupCallerTeamsHappy(t *testing.T) {
	ll := &fakeLiteLLM{
		userInfo: func(string) (*litellm.UserInfo, error) {
			return &litellm.UserInfo{UserID: "u-1", UserEmail: "a@b.c", Teams: []string{"team-a", "team-b"}}, nil
		},
	}
	got, err := LookupCallerTeams(context.Background(), ll, "a@b.c")
	if err != nil {
		t.Fatalf("LookupCallerTeams: %v", err)
	}
	if len(got) != 2 || got[0] != "team-a" || got[1] != "team-b" {
		t.Fatalf("unexpected teams: %#v", got)
	}
}

// TestLookupCallerTeamsEmpty — mock returns UserInfo with Teams=nil;
// the helper normalizes to a zero-length slice (NOT nil).
func TestLookupCallerTeamsEmpty(t *testing.T) {
	ll := &fakeLiteLLM{
		userInfo: func(string) (*litellm.UserInfo, error) {
			return &litellm.UserInfo{UserID: "u-1", UserEmail: "a@b.c", Teams: nil}, nil
		},
	}
	got, err := LookupCallerTeams(context.Background(), ll, "a@b.c")
	if err != nil {
		t.Fatalf("LookupCallerTeams: %v", err)
	}
	if got == nil {
		t.Fatalf("expected zero-length slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected len 0, got %d (%#v)", len(got), got)
	}
}

// TestLookupCallerTeamsErrNotFoundSentinel — mock returns the typed
// ErrNotFound sentinel; the helper treats it as zero-intersection
// (returns empty slice, no error).
func TestLookupCallerTeamsErrNotFoundSentinel(t *testing.T) {
	ll := &fakeLiteLLM{
		userInfo: func(string) (*litellm.UserInfo, error) { return nil, litellm.ErrNotFound },
	}
	got, err := LookupCallerTeams(context.Background(), ll, "a@b.c")
	if err != nil {
		t.Fatalf("LookupCallerTeams: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty slice on ErrNotFound, got %#v", got)
	}
}

// TestLookupCallerTeamsErr404Wrapped — mock returns the legacy
// makeRequest 4xx wrapper (string contains "404"); the helper still
// treats it as zero-intersection. Covers the Phase 3 D-25 design where
// UserInfoByEmail does NOT translate 404 → ErrNotFound at the type
// level.
func TestLookupCallerTeamsErr404Wrapped(t *testing.T) {
	ll := &fakeLiteLLM{
		userInfo: func(string) (*litellm.UserInfo, error) {
			return nil, fmt.Errorf("litellm: GET /user/info?user_email=...: status=404 code=NOT_FOUND")
		},
	}
	got, err := LookupCallerTeams(context.Background(), ll, "a@b.c")
	if err != nil {
		t.Fatalf("LookupCallerTeams: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty slice on 404 wrapper, got %#v", got)
	}
}

// TestLookupCallerTeamsTransportErr — mock returns a non-404 error
// (connection refused / 5xx); the helper propagates verbatim so the
// caller emits 503 litellm_unreachable.
func TestLookupCallerTeamsTransportErr(t *testing.T) {
	wantErr := errors.New("connection refused")
	ll := &fakeLiteLLM{
		userInfo: func(string) (*litellm.UserInfo, error) { return nil, wantErr },
	}
	got, err := LookupCallerTeams(context.Background(), ll, "a@b.c")
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wantErr unwrapped, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil slice on transport error, got %#v", got)
	}
}

// TestLookupCallerTeamsUncachedByDesign — two consecutive calls produce
// TWO UserInfoByEmail invocations (proves Phase 3 is uncached; Phase 4
// will add Redis caching). NOTE: this test must be updated when Phase 4
// lands a cached implementation — at that point the expected call count
// depends on the cache TTL.
func TestLookupCallerTeamsUncachedByDesign(t *testing.T) {
	ll := &fakeLiteLLM{
		userInfo: func(string) (*litellm.UserInfo, error) {
			return &litellm.UserInfo{UserID: "u-1", UserEmail: "a@b.c", Teams: []string{"t-1"}}, nil
		},
	}
	for i := 0; i < 2; i++ {
		if _, err := LookupCallerTeams(context.Background(), ll, "a@b.c"); err != nil {
			t.Fatalf("LookupCallerTeams (iter %d): %v", i, err)
		}
	}
	if got := ll.calls.Load(); got != 2 {
		t.Fatalf("expected 2 UserInfoByEmail calls (uncached), got %d", got)
	}
}

// ListTeamsByAlias is a no-op shim — Client interface compliance.
func (f *fakeLiteLLM) ListTeamsByAlias(_ context.Context, _ string) ([]litellm.TeamListEntry, error) {
	return nil, nil
}
