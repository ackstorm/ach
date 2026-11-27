// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/litellm"
	achmetrics "github.com/ackstorm/ach/internal/metrics"
)

const environmentBudgetGaugeInterval = 5 * time.Minute

// EnvironmentBudgetGauges exports, for every Environment with spec.budget,
// the spend of its "environment:<name>" tag in the current budget cycle, the
// ceiling LiteLLM enforces and the next reset. LiteLLM exports no metric for
// tag budgets, so this reads them the way the console does (TagInfo, then
// TagSpend over SpendWindowStart), on a fixed interval: a scrape never
// reaches LiteLLM, and each pass costs two calls per budgeted Environment.
//
// A failed read is logged and leaves the last value in place: a 0 would read
// as "nothing spent" and silence the budget alert.
//
// Leader-elected (the manager's default for a Runnable), so one replica
// exports the series and pays the LiteLLM calls.
type EnvironmentBudgetGauges struct {
	Client    client.Client
	LiteLLM   litellm.Client
	Namespace string
	Metrics   *achmetrics.OperatorCollectors
	Log       logr.Logger

	exported map[string]bool
}

// Start refreshes at once, then every environmentBudgetGaugeInterval.
func (g *EnvironmentBudgetGauges) Start(ctx context.Context) error {
	t := time.NewTicker(environmentBudgetGaugeInterval)
	defer t.Stop()
	for {
		var list achv1alpha1.EnvironmentList
		if err := g.Client.List(ctx, &list, client.InNamespace(g.Namespace)); err != nil {
			g.Log.Error(err, "list Environments for budget gauges")
		} else {
			g.refresh(ctx, list.Items, time.Now())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// refresh exports exactly the budgeted Environments in envs; the series of
// any other Environment it exported before are deleted.
func (g *EnvironmentBudgetGauges) refresh(ctx context.Context, envs []achv1alpha1.Environment, now time.Time) {
	seen := map[string]bool{}
	for _, env := range envs {
		if env.Spec.Budget == nil {
			continue
		}
		seen[env.Name] = true
		g.refreshOne(ctx, env.Name, now)
	}
	for name := range g.exported {
		if !seen[name] {
			g.Metrics.EnvironmentBudgetSpend.DeleteLabelValues(name)
			g.Metrics.EnvironmentBudgetMax.DeleteLabelValues(name)
			g.Metrics.EnvironmentBudgetResetTime.DeleteLabelValues(name)
		}
	}
	g.exported = seen
}

func (g *EnvironmentBudgetGauges) refreshOne(ctx context.Context, name string, now time.Time) {
	tag := litellm.EnvironmentBudgetTag(name)
	entry, err := g.LiteLLM.TagInfo(ctx, tag)
	if err != nil {
		g.Log.Error(err, "read environment budget", "tag", tag)
		return
	}
	if entry == nil || entry.Budget == nil {
		return // the Environment reconcile has not written the budget yet
	}
	g.Metrics.EnvironmentBudgetMax.WithLabelValues(name).Set(entry.Budget.MaxBudget)
	if reset, err := litellm.ParseTime(entry.Budget.BudgetResetAt); err == nil {
		g.Metrics.EnvironmentBudgetResetTime.WithLabelValues(name).Set(float64(reset.Unix()))
	} else {
		g.Metrics.EnvironmentBudgetResetTime.DeleteLabelValues(name) // a budget that never resets
	}
	start, ok := entry.SpendWindowStart()
	if !ok {
		g.Log.Info("environment budget spend window unreadable", "tag", tag)
		return
	}
	spend, _, err := g.LiteLLM.TagSpend(ctx, tag, start, now)
	if err != nil {
		g.Log.Error(err, "read environment budget spend", "tag", tag)
		return
	}
	g.Metrics.EnvironmentBudgetSpend.WithLabelValues(name).Set(spend)
}
