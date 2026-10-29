// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// registerRuntimeCollectors adds the standard Go runtime + process
// collectors to a service's process-local metrics Registry so /metrics
// exposes the go_* / process_* baseline (Hub §18.5 "Go runtime
// baseline"). The operator wires through controller-runtime's Registry,
// which already ships these; the forwarder / content-service /
// platform-api build a bare prometheus.NewRegistry() (Phase 5 D-09
// isolation from the global/controller-runtime registry) and add the
// baseline here — keeping unit-test callers of the bare registry free
// of runtime-collector noise.
func registerRuntimeCollectors(reg *prometheus.Registry) {
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
}
