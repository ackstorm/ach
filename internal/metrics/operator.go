// SPDX-License-Identifier: Apache-2.0

package metrics

import "github.com/prometheus/client_golang/prometheus"

// OperatorCollectors holds the ACH-domain operator metrics (Hub §18.5, G7).
// Registered against the controller-runtime metrics registry
// (sigs.k8s.io/controller-runtime/pkg/metrics.Registry, a
// prometheus.Registerer) so they are exposed on the operator's existing
// /metrics endpoint alongside the controller-runtime built-ins.
//
//	ach_environment_available{name}                       1 series per Environment
//	ach_operator_external_ref_refresh_total{kind,type,result}
//	ach_environment_budget_spend_usd{environment}          1 series per budgeted Environment
//	ach_environment_budget_max_usd{environment}
//	ach_environment_budget_reset_timestamp_seconds{environment}
type OperatorCollectors struct {
	EnvironmentAvailable       *prometheus.GaugeVec
	ExternalRefRefresh         *prometheus.CounterVec
	EnvironmentBudgetSpend     *prometheus.GaugeVec
	EnvironmentBudgetMax       *prometheus.GaugeVec
	EnvironmentBudgetResetTime *prometheus.GaugeVec
}

// NewOperatorCollectors constructs and registers the operator metrics
// against reg. Calling twice against the same registry panics (standard
// prometheus re-register behavior) — hold the returned pointer in a single
// process-scoped variable.
func NewOperatorCollectors(reg prometheus.Registerer) *OperatorCollectors {
	c := &OperatorCollectors{
		EnvironmentAvailable: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ach_environment_available",
				Help: "1 when an Environment's Available condition is True, else 0 (Hub §18.5).",
			},
			[]string{"name"},
		),
		ExternalRefRefresh: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ach_operator_external_ref_refresh_total",
				Help: "Total upstream refresh attempts by kind/type/result (Hub §18.5).",
			},
			[]string{"kind", "type", "result"},
		),
		EnvironmentBudgetSpend: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "ach_environment_budget_spend_usd",
			Help: "Spend of the Environment's LiteLLM tag budget in its current cycle (USD).",
		}, []string{"environment"}),
		EnvironmentBudgetMax: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "ach_environment_budget_max_usd",
			Help: "Ceiling of the Environment's LiteLLM tag budget (USD).",
		}, []string{"environment"}),
		EnvironmentBudgetResetTime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "ach_environment_budget_reset_timestamp_seconds",
			Help: "Unix time of the Environment's next LiteLLM tag budget reset.",
		}, []string{"environment"}),
	}
	reg.MustRegister(c.EnvironmentAvailable, c.ExternalRefRefresh,
		c.EnvironmentBudgetSpend, c.EnvironmentBudgetMax, c.EnvironmentBudgetResetTime)
	return c
}
