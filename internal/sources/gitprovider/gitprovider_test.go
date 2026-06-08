// SPDX-License-Identifier: Apache-2.0

package gitprovider

import (
	"testing"

	gitsrc "github.com/ackstorm/ach/internal/gitfetch"
)

func TestSchemeForProvider(t *testing.T) {
	cases := map[string]gitsrc.AuthScheme{
		"gitlab":    gitsrc.AuthBasicOAuth2,
		"github":    gitsrc.AuthBearer,
		"bitbucket": gitsrc.AuthBearer,
		"":          gitsrc.AuthBearer,
	}
	for provider, want := range cases {
		if got := schemeForProvider(provider); got != want {
			t.Errorf("schemeForProvider(%q) = %v; want %v", provider, got, want)
		}
	}
}
