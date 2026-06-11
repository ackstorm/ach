// SPDX-License-Identifier: Apache-2.0

package store

import (
	"testing"

	"github.com/ackstorm/ach/internal/db"
)

// TestRowToView_NoticePassThrough asserts the env detail view surfaces the
// row's notice verbatim.
func TestRowToView_NoticePassThrough(t *testing.T) {
	v := RowToView(db.EnvironmentRow{
		Namespace: "ach-system",
		Name:      "demo",
		Notice:    "works best with openai-* models",
	})
	if v.Notice != "works best with openai-* models" {
		t.Errorf("Notice = %q, want pass-through", v.Notice)
	}
}
