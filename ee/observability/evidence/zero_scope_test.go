// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package evidence

import (
	"context"
	"errors"
	"testing"

	core "github.com/ackstorm/ach/internal/observability/evidence"
)

func TestZeroScopeIsRejectedBeforeAnyNetworkCall(t *testing.T) {
	ch, calls := scriptedCH(t)
	r := NewReader(core.NewReader(ch, []string{"p1"}))
	if _, err := r.List(context.Background(), core.Scope{}, validQuery()); !errors.Is(err, core.ErrNoScope) {
		t.Fatalf("List err=%v", err)
	}
	if _, err := r.Detail(context.Background(), core.Scope{}, "t1", "", testWindow()); !errors.Is(err, core.ErrNoScope) {
		t.Fatalf("Detail err=%v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("made %d calls", len(*calls))
	}
}
