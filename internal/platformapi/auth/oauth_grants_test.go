// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"testing"
)

func TestRedisGrants(t *testing.T) {
	store := newOAuthStore(t)
	g := NewRedisGrants(store.RDB)
	ctx := context.Background()
	if ok, err := g.Granted(ctx, "u@x.com", "zoho-desk-ro"); ok || err != nil {
		t.Fatalf("absent: %v %v", ok, err)
	}
	store.RDB.Set(ctx, "oauth:zoho-desk-ro:state:u@x.com", `{"granted":true,"x":1}`, 0)
	if ok, err := g.Granted(ctx, "u@x.com", "zoho-desk-ro"); !ok || err != nil {
		t.Fatalf("granted: %v %v", ok, err)
	}
	store.RDB.Set(ctx, "oauth:zoho-desk-ro:state:u@x.com", `not json`, 0)
	if ok, err := g.Granted(ctx, "u@x.com", "zoho-desk-ro"); ok || err != nil {
		t.Fatalf("garbage reads as not granted, no error: %v %v", ok, err)
	}
}
