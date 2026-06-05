//go:build integration

// SPDX-License-Identifier: Apache-2.0

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

func TestUpsertGetListSkills(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	last := time.Now()
	for _, name := range []string{"bravo", "alpha"} {
		if err := db.UpsertSkill(ctx, pool, db.SkillRow{
			Namespace: "ach", Name: name, StorageLocation: "/var/cache/ach/skill/" + name + ".tar.gz",
			LastSuccessfulRefresh: &last, MaxStalenessSeconds: 600, ResourceVersion: "1",
		}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	got, err := db.ListSkills(ctx, pool, "ach")
	if err != nil || len(got) != 2 || got[0].Name != "alpha" || got[1].Name != "bravo" {
		t.Fatalf("ListSkills = %+v err=%v, want alpha,bravo", got, err)
	}
	one, err := db.GetSkillByName(ctx, pool, "ach", "alpha")
	if err != nil || one == nil || one.MaxStalenessSeconds != 600 {
		t.Fatalf("GetSkillByName = %+v err=%v", one, err)
	}
	res, err := db.ResolveSkillByName(ctx, pool, "ach", "alpha", "")
	if err != nil || res == nil || res.LastSuccessfulRefresh == nil {
		t.Fatalf("ResolveSkillByName = %+v err=%v", res, err)
	}
	if err := db.SoftDeleteSkill(ctx, pool, "ach", "alpha"); err != nil {
		t.Fatalf("SoftDeleteSkill: %v", err)
	}
	got, _ = db.ListSkills(ctx, pool, "ach")
	if len(got) != 1 || got[0].Name != "bravo" {
		t.Errorf("after soft-delete want only bravo; got %+v", got)
	}
	if err := db.DeleteSkill(ctx, pool, "ach", "bravo"); err != nil {
		t.Fatalf("DeleteSkill: %v", err)
	}
}
