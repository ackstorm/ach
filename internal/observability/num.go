// SPDX-License-Identifier: Apache-2.0

package observability

import (
	"encoding/json"
	"fmt"
)

// num coerces a JSON metric to a number; nil and garbage are the real-zero
// default (stats.py _num). WR-05 in the Python: bool is a subclass of int,
// so True/False had to be rejected explicitly there to avoid serializing as
// a stray boolean. A Go bool never matches any case below, so it already
// falls through to the 0 default without a dedicated case.
func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		var f float64
		if _, err := fmt.Sscanf(x, "%g", &f); err == nil {
			return f
		}
	}
	return 0
}

// safePct is numerator/denominator, nil on a 0 denominator (D-08). Python's
// _safe_pct also nils out a None denominator; here every caller with a
// nullable denominator (e.g. a *float64 budget.MaxBudget) collapses it to
// 0.0 before calling in, so "unavailable" and "genuinely zero" share the
// same nil result — algebraically identical to the None-or-0 check.
func safePct(n, d float64) *float64 {
	if d == 0 {
		return nil
	}
	p := n / d
	return &p
}

func f64p(f float64) *float64 { return &f }
func sp(s string) *string     { return &s }

// unknownLabel is the "no attributable source/data" sentinel Python
// independently spells "unknown" in three places this package ports
// (session.py _budget_block's source, latency.py _row_model's model
// fallback, and its outcome-status fallback) — one shared constant instead
// of three coincidentally-identical literals.
const unknownLabel = "unknown"
