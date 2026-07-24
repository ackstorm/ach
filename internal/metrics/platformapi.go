// SPDX-License-Identifier: Apache-2.0

package metrics

import "github.com/prometheus/client_golang/prometheus"

// PlatformAPICollectors holds the platform-api §18.5 collectors (G7):
//
//	ach_platform_api_hydrate_duration_seconds   (no label)
//	ach_platform_api_login_total{outcome}
type PlatformAPICollectors struct {
	HydrateDuration prometheus.Histogram
	Login           *prometheus.CounterVec
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
		Login: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ach_platform_api_login_total",
			Help: "Login attempts by outcome (SSO + CLI) (Hub §18.5).",
		}, []string{"outcome"}),
	}
	reg.MustRegister(c.HydrateDuration, c.Login)
	return c
}
