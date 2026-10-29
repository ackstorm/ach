// SPDX-License-Identifier: Apache-2.0

package store

import (
	"testing"

	"github.com/ackstorm/ach/internal/db"
)

// TestRowToView_DescriptionPassThrough asserts the env detail view surfaces the
// row's description verbatim (notice is no longer on the list/describe wire).
func TestRowToView_DescriptionPassThrough(t *testing.T) {
	v := RowToView(db.EnvironmentRow{
		Namespace:   "ach-system",
		Name:        "demo",
		Description: "production env for the data team",
	})
	if v.Description != "production env for the data team" {
		t.Errorf("Description = %q, want pass-through", v.Description)
	}
}
