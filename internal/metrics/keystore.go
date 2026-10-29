// SPDX-License-Identifier: Apache-2.0

package metrics

import "github.com/prometheus/client_golang/prometheus"

// KeystoreCollectors holds the key-resolution cache hit/miss counters (G7).
// Both platform-api and the forwarder wrap their DB resolver in the cached
// resolver; each registers its own KeystoreCollectors on its process-local
// registry.
//
//	ach_key_resolution_cache_hits_total{key_type,layer}
//	ach_key_resolution_cache_misses_total{key_type,layer}
type KeystoreCollectors struct {
	Hits   *prometheus.CounterVec
	Misses *prometheus.CounterVec
}

// NewKeystoreCollectors constructs and registers the counters against reg.
func NewKeystoreCollectors(reg prometheus.Registerer) *KeystoreCollectors {
	c := &KeystoreCollectors{
		Hits: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ach_key_resolution_cache_hits_total",
			Help: "Total key-resolution cache hits by key_type/layer (Hub §18.5).",
		}, []string{"key_type", "layer"}),
		Misses: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ach_key_resolution_cache_misses_total",
			Help: "Total key-resolution cache misses by key_type/layer (Hub §18.5).",
		}, []string{"key_type", "layer"}),
	}
	reg.MustRegister(c.Hits, c.Misses)
	return c
}
