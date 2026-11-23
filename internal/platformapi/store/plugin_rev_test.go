// SPDX-License-Identifier: Apache-2.0

package store

import (
	"testing"

	"github.com/ackstorm/ach/internal/db"
)

func TestPluginRev_MarksNative(t *testing.T) {
	cases := map[string]db.PluginRow{
		"abc":        {UpstreamRev: "abc", Delivery: "ach"},
		"abc+native": {UpstreamRev: "abc", Delivery: "native"},
	}
	for want, p := range cases {
		if got := pluginRev(p); got != want {
			t.Errorf("pluginRev(%q, %q) = %q; want %q", p.UpstreamRev, p.Delivery, got, want)
		}
	}
}
