// SPDX-License-Identifier: Apache-2.0

package observability

// f64p and sp are test-only pointer-literal helpers.
func f64p(f float64) *float64 { return &f }
func sp(s string) *string     { return &s }
