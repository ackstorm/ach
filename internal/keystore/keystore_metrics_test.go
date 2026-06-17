// SPDX-License-Identifier: Apache-2.0

package keystore

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"

	"github.com/ackstorm/ach/internal/keys"
	achmetrics "github.com/ackstorm/ach/internal/metrics"
)

// TestCachedResolver_HitMissMetrics — resolving the same bearer twice yields
// exactly one miss (first call, cache empty) and one hit (second call, cache
// warm), both labeled key_type="pk", layer="redis" (G7).
func TestCachedResolver_HitMissMetrics(t *testing.T) {
	plaintext := validBearer(t) // syntactically-valid pk- bearer
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	inner := &fakeResolver{respond: func(string) (*KeyInfo, error) {
		return &KeyInfo{KeyID: "pkid_x", KeyType: keys.PrefixPk, OwnerEmail: "a@b", ExpiresAt: &expires}, nil
	}}

	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })

	reg := achmetrics.NewRegistry()
	collectors := achmetrics.NewKeystoreCollectors(reg)

	r, err := NewCachedResolver(inner, rc, []byte("test-pepper-32-bytes-aaaaaaaaaaaa"), WithCacheMetrics(collectors))
	if err != nil {
		t.Fatalf("NewCachedResolver: %v", err)
	}

	ctx := context.Background()
	if _, err := r.Resolve(ctx, plaintext); err != nil { // miss → inner → cache set
		t.Fatalf("first Resolve: %v", err)
	}
	if _, err := r.Resolve(ctx, plaintext); err != nil { // hit
		t.Fatalf("second Resolve: %v", err)
	}

	kt := string(keys.PrefixPk)
	if got := testutil.ToFloat64(collectors.Misses.WithLabelValues(kt, cacheLayerRedis)); got != 1 {
		t.Errorf("misses{key_type=%s,layer=redis} = %v, want 1", kt, got)
	}
	if got := testutil.ToFloat64(collectors.Hits.WithLabelValues(kt, cacheLayerRedis)); got != 1 {
		t.Errorf("hits{key_type=%s,layer=redis} = %v, want 1", kt, got)
	}
}
