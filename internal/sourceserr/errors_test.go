// SPDX-License-Identifier: Apache-2.0

package sourceserr_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/ackstorm/ach/internal/sourceserr"
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
		{"ErrUnauthorized direct", sourceserr.ErrUnauthorized, "Unauthorized"},
		{"ErrUnauthorized wrapped", fmt.Errorf("github: %w", sourceserr.ErrUnauthorized), "Unauthorized"},
		{"ErrNotFound direct", sourceserr.ErrNotFound, "NotFound"},
		{"ErrNotFound wrapped", fmt.Errorf("s3 404: %w", sourceserr.ErrNotFound), "NotFound"},
		{"ErrUpstreamInvalid direct", sourceserr.ErrUpstreamInvalid, "UpstreamInvalid"},
		{"ErrUpstreamInvalid wrapped", fmt.Errorf("parse: %w", sourceserr.ErrUpstreamInvalid), "UpstreamInvalid"},
		{"ErrUnreachable direct", sourceserr.ErrUnreachable, "Unreachable"},
		{"ErrUnreachable wrapped", fmt.Errorf("net: %w", sourceserr.ErrUnreachable), "Unreachable"},
		{"unclassified → Unreachable", errors.New("totally novel error"), "Unreachable"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sourceserr.ReasonOf(tc.err)
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
	err := errors.Join(sourceserr.ErrUnauthorized, sourceserr.ErrNotFound)
	if got := sourceserr.ReasonOf(err); got != "Unauthorized" {
		t.Errorf("dispatch order: want Unauthorized first, got %q", got)
	}

	// NotFound wins over UpstreamInvalid.
	err = errors.Join(sourceserr.ErrNotFound, sourceserr.ErrUpstreamInvalid)
	if got := sourceserr.ReasonOf(err); got != "NotFound" {
		t.Errorf("dispatch order: want NotFound second, got %q", got)
	}

	// UpstreamInvalid wins over Unreachable.
	err = errors.Join(sourceserr.ErrUpstreamInvalid, sourceserr.ErrUnreachable)
	if got := sourceserr.ReasonOf(err); got != "UpstreamInvalid" {
		t.Errorf("dispatch order: want UpstreamInvalid third, got %q", got)
	}
}

// TestUnknownSourceIsSentinel asserts ErrUnknownSource is the value the
// Registry returns for an unrecognized spec.Type — callers compare via
// errors.Is, not value equality.
func TestUnknownSourceIsSentinel(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("dispatch: %w", sourceserr.ErrUnknownSource)
	if !errors.Is(wrapped, sourceserr.ErrUnknownSource) {
		t.Fatalf("errors.Is should detect wrapped ErrUnknownSource")
	}
}
