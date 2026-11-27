// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/litellm/litellmtest"
	achmetrics "github.com/ackstorm/ach/internal/metrics"
	"github.com/ackstorm/ach/internal/observability"
)

// tagReadFake answers TagInfo/TagSpend from maps; an error field fails every read.
type tagReadFake struct {
	*litellmtest.NoopClient
	info     map[string]*litellm.TagInfoEntry
	spend    map[string]float64
	infoErr  error
	spendErr error
}

func (f *tagReadFake) TagInfo(_ context.Context, name string) (*litellm.TagInfoEntry, error) {
	return f.info[name], f.infoErr
}

func (f *tagReadFake) TagSpend(_ context.Context, name string, _, _ time.Time) (float64, []observability.DaySpend, error) {
	return f.spend[name], nil, f.spendErr
}

func budgeted(name string) achv1alpha1.Environment {
	env := achv1alpha1.Environment{}
	env.Name = name
	env.Spec.Budget = &achv1alpha1.BudgetBlock{MaxBudget: 100, BudgetDuration: "30d"}
	return env
}

func TestEnvironmentBudgetGauges(t *testing.T) {
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	reset := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	fake := &tagReadFake{
		NoopClient: &litellmtest.NoopClient{Log: logr.Discard()},
		info: map[string]*litellm.TagInfoEntry{
			"environment:a": {CreatedAt: "2026-10-01T00:00:00Z", Budget: &litellm.TagBudget{
				MaxBudget: 100, BudgetDuration: "30d", BudgetResetAt: reset.Format(time.RFC3339)}},
		},
		spend: map[string]float64{"environment:a": 42},
	}
	var logged strings.Builder
	m := achmetrics.NewOperatorCollectors(prometheus.NewRegistry())
	g := &EnvironmentBudgetGauges{LiteLLM: fake, Metrics: m,
		Log: funcr.New(func(prefix, args string) { logged.WriteString(args + "\n") }, funcr.Options{})}
	unbudgeted := achv1alpha1.Environment{}
	unbudgeted.Name = "free"
	envs := []achv1alpha1.Environment{budgeted("a"), unbudgeted}

	want := func(step string, spend, maxBudget float64) {
		t.Helper()
		if got := testutil.ToFloat64(m.EnvironmentBudgetSpend.WithLabelValues("a")); got != spend {
			t.Errorf("%s: spend = %v, want %v", step, got, spend)
		}
		if got := testutil.ToFloat64(m.EnvironmentBudgetMax.WithLabelValues("a")); got != maxBudget {
			t.Errorf("%s: max = %v, want %v", step, got, maxBudget)
		}
		if got := testutil.ToFloat64(m.EnvironmentBudgetResetTime.WithLabelValues("a")); got != float64(reset.Unix()) {
			t.Errorf("%s: reset = %v, want %v", step, got, reset.Unix())
		}
	}

	g.refresh(context.Background(), envs, now)
	want("first pass", 42, 100)
	if n := testutil.CollectAndCount(m.EnvironmentBudgetMax); n != 1 {
		t.Fatalf("an Environment without spec.budget must not be exported: %d series", n)
	}

	fake.spend["environment:a"], fake.info["environment:a"].Budget.MaxBudget = 99, 200
	fake.spendErr = errors.New("litellm down")
	g.refresh(context.Background(), envs, now)
	want("TagSpend error", 42, 200) // max still updates; spend keeps its last value, not 0
	if !strings.Contains(logged.String(), "litellm down") {
		t.Errorf("TagSpend error not logged; log:\n%s", logged.String())
	}

	fake.infoErr = errors.New("tag info down")
	g.refresh(context.Background(), envs, now)
	want("TagInfo error", 42, 200)
	if !strings.Contains(logged.String(), "tag info down") {
		t.Errorf("TagInfo error not logged; log:\n%s", logged.String())
	}

	g.refresh(context.Background(), nil, now)
	for _, v := range []*prometheus.GaugeVec{m.EnvironmentBudgetSpend, m.EnvironmentBudgetMax, m.EnvironmentBudgetResetTime} {
		if n := testutil.CollectAndCount(v); n != 0 {
			t.Errorf("a vanished Environment's series must be deleted: %d left", n)
		}
	}
}
