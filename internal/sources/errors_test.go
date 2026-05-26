// SPDX-License-Identifier: Apache-2.0

package sources_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/ackstorm/ach/internal/sources"
)

// TestReasonOfMapping asserts every sentinel maps to its documented
// Hub §6.6 SourceReachable.reason enum value, and that wrapping via
// fmt.Errorf("%w", ...) preserves the classification (errors.Is
// traversal through wrapped errors).
func TestReasonOfMapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil → Unreachable (default)", nil, "Unreachable"},
		{"ErrUnauthorized direct", sources.ErrUnauthorized, "Unauthorized"},
		{"ErrUnauthorized wrapped", fmt.Errorf("github: %w", sources.ErrUnauthorized), "Unauthorized"},
		{"ErrNotFound direct", sources.ErrNotFound, "NotFound"},
		{"ErrNotFound wrapped", fmt.Errorf("s3 404: %w", sources.ErrNotFound), "NotFound"},
		{"ErrUpstreamInvalid direct", sources.ErrUpstreamInvalid, "UpstreamInvalid"},
		{"ErrUpstreamInvalid wrapped", fmt.Errorf("parse: %w", sources.ErrUpstreamInvalid), "UpstreamInvalid"},
		{"ErrUnreachable direct", sources.ErrUnreachable, "Unreachable"},
		{"ErrUnreachable wrapped", fmt.Errorf("net: %w", sources.ErrUnreachable), "Unreachable"},
		{"unclassified → Unreachable", errors.New("totally novel error"), "Unreachable"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sources.ReasonOf(tc.err)
			if got != tc.want {
				t.Errorf("ReasonOf(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// TestReasonOf_PriorityOrder asserts the dispatch order in [ReasonOf]
// when an error somehow wraps two sentinels via errors.Join — the FIRST
// matching sentinel in dispatch order (Unauthorized → NotFound →
// UpstreamInvalid → Unreachable) wins. This pins the documented order
// so future edits to the function preserve the contract.
func TestReasonOf_PriorityOrder(t *testing.T) {
	t.Parallel()

	// Auth wins over NotFound when both are wrapped.
	err := errors.Join(sources.ErrUnauthorized, sources.ErrNotFound)
	if got := sources.ReasonOf(err); got != "Unauthorized" {
		t.Errorf("dispatch order: want Unauthorized first, got %q", got)
	}

	// NotFound wins over UpstreamInvalid.
	err = errors.Join(sources.ErrNotFound, sources.ErrUpstreamInvalid)
	if got := sources.ReasonOf(err); got != "NotFound" {
		t.Errorf("dispatch order: want NotFound second, got %q", got)
	}

	// UpstreamInvalid wins over Unreachable.
	err = errors.Join(sources.ErrUpstreamInvalid, sources.ErrUnreachable)
	if got := sources.ReasonOf(err); got != "UpstreamInvalid" {
		t.Errorf("dispatch order: want UpstreamInvalid third, got %q", got)
	}
}

// TestUnknownSourceIsSentinel asserts ErrUnknownSource is the value the
// Registry returns for an unrecognized spec.Type — callers compare via
// errors.Is, not value equality.
func TestUnknownSourceIsSentinel(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("dispatch: %w", sources.ErrUnknownSource)
	if !errors.Is(wrapped, sources.ErrUnknownSource) {
		t.Fatalf("errors.Is should detect wrapped ErrUnknownSource")
	}
}
