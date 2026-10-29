// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

func interval(d time.Duration) achv1alpha1.RefreshBlock {
	return achv1alpha1.RefreshBlock{
		Interval:     &metav1.Duration{Duration: d},
		MaxStaleness: metav1.Duration{Duration: 24 * time.Hour},
	}
}

func TestShouldSkipFetch(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name                    string
		refresh                 achv1alpha1.RefreshBlock
		lastRefresh             time.Time
		observedGen             int64
		generation              int64
		annotations             map[string]string
		forceRefreshRequestedAt time.Time
		wantSkip                bool
	}{
		{
			name:        "first reconcile (lastRefresh zero) does NOT skip",
			refresh:     interval(time.Hour),
			lastRefresh: time.Time{},
			observedGen: 0,
			generation:  1,
			wantSkip:    false,
		},
		{
			name:        "within interval, gen match, no annotation: SKIPS",
			refresh:     interval(time.Hour),
			lastRefresh: now.Add(-30 * time.Minute),
			observedGen: 1,
			generation:  1,
			wantSkip:    true,
		},
		{
			name:        "interval expired: does NOT skip",
			refresh:     interval(time.Hour),
			lastRefresh: now.Add(-90 * time.Minute),
			observedGen: 1,
			generation:  1,
			wantSkip:    false,
		},
		{
			name:        "spec change (Generation > ObservedGeneration): does NOT skip",
			refresh:     interval(time.Hour),
			lastRefresh: now.Add(-30 * time.Minute),
			observedGen: 1,
			generation:  2,
			wantSkip:    false,
		},
		{
			name:        "force-refresh annotation present: does NOT skip",
			refresh:     interval(time.Hour),
			lastRefresh: now.Add(-30 * time.Minute),
			observedGen: 1,
			generation:  1,
			annotations: map[string]string{"ach.ackstorm.ai/force-refresh": "now"},
			wantSkip:    false,
		},
		{
			name:        "interval nil + maxStaleness fallback (24h/2 = 12h): within window SKIPS",
			refresh:     achv1alpha1.RefreshBlock{MaxStaleness: metav1.Duration{Duration: 24 * time.Hour}},
			lastRefresh: now.Add(-1 * time.Hour),
			observedGen: 1,
			generation:  1,
			wantSkip:    true,
		},
		{
			name:        "nil annotations map: treated as no-annotation, SKIPS within window",
			refresh:     interval(time.Hour),
			lastRefresh: now.Add(-30 * time.Minute),
			observedGen: 1,
			generation:  1,
			annotations: nil,
			wantSkip:    true,
		},
		{
			name:                    "force_refresh_requested_at after lastRefresh: does NOT skip",
			refresh:                 interval(time.Hour),
			lastRefresh:             now.Add(-30 * time.Minute),
			observedGen:             1,
			generation:              1,
			forceRefreshRequestedAt: now.Add(-1 * time.Minute),
			wantSkip:                false,
		},
		{
			name:                    "force_refresh_requested_at before lastRefresh (already serviced): SKIPS",
			refresh:                 interval(time.Hour),
			lastRefresh:             now.Add(-30 * time.Minute),
			observedGen:             1,
			generation:              1,
			forceRefreshRequestedAt: now.Add(-45 * time.Minute),
			wantSkip:                true,
		},
		{
			name:                    "force_refresh_requested_at zero (no pending marker): SKIPS within window",
			refresh:                 interval(time.Hour),
			lastRefresh:             now.Add(-30 * time.Minute),
			observedGen:             1,
			generation:              1,
			forceRefreshRequestedAt: time.Time{},
			wantSkip:                true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldSkipFetch(tc.refresh, tc.lastRefresh, tc.observedGen, tc.generation, tc.annotations, tc.forceRefreshRequestedAt, now)
			if got != tc.wantSkip {
				t.Fatalf("shouldSkipFetch = %v, want %v", got, tc.wantSkip)
			}
		})
	}
}
