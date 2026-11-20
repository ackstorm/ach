// SPDX-License-Identifier: Apache-2.0

package sourceserr_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/ackstorm/ach/internal/sourceserr"
)

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
