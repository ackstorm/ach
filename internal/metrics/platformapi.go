// SPDX-License-Identifier: Apache-2.0

package metrics

import "github.com/prometheus/client_golang/prometheus"

// PlatformAPICollectors holds the platform-api §18.5 collectors (G7):
//
//	ach_platform_api_hydrate_duration_seconds   (no label)
//	ach_key_exchange_rejected_total             (reason)
type PlatformAPICollectors struct {
	HydrateDuration prometheus.Histogram
	// KeyExchangeRejected counts POST /platform/keys/exchange requests
	// refused before TokenReview, by reason.
	KeyExchangeRejected *prometheus.CounterVec
}

// NewPlatformAPICollectors constructs and registers the platform-api
// collectors against reg. Calling twice against the same registry panics
// (standard prometheus re-register behavior).
func NewPlatformAPICollectors(reg prometheus.Registerer) *PlatformAPICollectors {
	c := &PlatformAPICollectors{
		HydrateDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "ach_platform_api_hydrate_duration_seconds",
			Help:    "POST /platform/hydrate handler duration seconds (Hub §18.5).",
			Buckets: prometheus.DefBuckets,
		}),
		KeyExchangeRejected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ach_key_exchange_rejected_total",
			Help: "Key exchange requests rejected before TokenReview, by reason (guard, rate_limited, invalid_signature, keys_unavailable).",
		}, []string{"reason"}),
	}
	reg.MustRegister(c.HydrateDuration, c.KeyExchangeRejected)
	return c
}
