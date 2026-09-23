// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/ackstorm/ach/internal/litellm"
)

// seedDeps builds the OAuthDeps shell seedUserBudgetTag needs: only the
// embedded Auth (LiteLLM + Logger + UserBudget) is ever touched.
func seedDeps(flm *fakeLiteLLM, budget *litellm.TagBudget) OAuthDeps {
	deps := provisionDeps(flm)
	deps.UserBudget = budget
	return OAuthDeps{Auth: deps}
}

// TestSeedUserBudgetTagSeedsOutsideTheSSOCallback is the whole point of the
// change: a person who logged in before the default existed only ever
// refreshes, so the SSO callback never runs for them again. Every grant
// crosses ensureOAuthPK, so the ceiling must land from there too.
func TestSeedUserBudgetTagSeedsOutsideTheSSOCallback(t *testing.T) {
	flm := newFakeLiteLLM()
	flm.tagInfoBehaviour = func(name string) (*litellm.TagInfoEntry, error) {
		return &litellm.TagInfoEntry{Name: name, Budget: nil}, nil
	}
	d := seedDeps(flm, &litellm.TagBudget{MaxBudget: 100})

	d.seedUserBudgetTag(context.Background(), "pepe@example.com")

	got, ok := flm.upsertedTags["user:pepe@example.com"]
	if !ok || got.MaxBudget != 100 {
		t.Fatalf("refresh did not seed the ceiling: %+v (ok=%v)", got, ok)
	}
}

// TestSeedUserBudgetTagLeavesAnAdminCeilingAlone — this now runs on EVERY
// refresh, so a clobber here would silently undo an admin's PATCH within
// the hour rather than at the next interactive login.
func TestSeedUserBudgetTagLeavesAnAdminCeilingAlone(t *testing.T) {
	flm := newFakeLiteLLM()
	flm.tagInfoBehaviour = func(name string) (*litellm.TagInfoEntry, error) {
		return &litellm.TagInfoEntry{
			Name:   name,
			Budget: &litellm.TagBudget{BudgetID: name, MaxBudget: 7},
		}, nil
	}
	d := seedDeps(flm, &litellm.TagBudget{MaxBudget: 100})

	d.seedUserBudgetTag(context.Background(), "pepe@example.com")

	if flm.upsertTagCalls != 0 {
		t.Fatalf("a refresh rewrote an existing ceiling: %d writes, %v",
			flm.upsertTagCalls, flm.upsertedTags)
	}
}

// TestSeedUserBudgetTagIsBestEffort — deliberately NOT fail-loud, unlike
// the SSO callback. Token refresh runs through here, so turning a LiteLLM
// blip into an error would sign out every active user at once. A failed
// read must leave the tag untouched (a blind write would clobber an
// admin's ceiling) and let the grant proceed.
func TestSeedUserBudgetTagIsBestEffort(t *testing.T) {
	flm := newFakeLiteLLM()
	flm.tagInfoBehaviour = func(string) (*litellm.TagInfoEntry, error) {
		return nil, errors.New("litellm down")
	}
	d := seedDeps(flm, &litellm.TagBudget{MaxBudget: 100})

	d.seedUserBudgetTag(context.Background(), "pepe@example.com")

	if flm.upsertTagCalls != 0 {
		t.Fatalf("wrote a ceiling despite an unreadable tag: %v", flm.upsertedTags)
	}
}

// TestSeedUserBudgetTagNoDefaultConfigured — with no chart default there is
// nothing to seed, and the per-grant /tag/info read must not happen at all.
func TestSeedUserBudgetTagNoDefaultConfigured(t *testing.T) {
	flm := newFakeLiteLLM()
	flm.tagInfoBehaviour = func(string) (*litellm.TagInfoEntry, error) {
		t.Fatal("read the tag with no default ceiling configured")
		return nil, nil
	}
	d := seedDeps(flm, nil)

	d.seedUserBudgetTag(context.Background(), "pepe@example.com")

	if flm.upsertTagCalls != 0 {
		t.Fatalf("wrote a ceiling with none configured: %v", flm.upsertedTags)
	}
}
