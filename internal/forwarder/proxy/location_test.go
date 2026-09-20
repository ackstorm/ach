// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"net/http"
	"net/url"
	"testing"
)

func TestRewriteLocation(t *testing.T) {
	up, _ := url.Parse("http://litellm.litellm.svc:4000")
	pub, _ := url.Parse("https://ach.example.com")
	cases := map[string]struct {
		status   int
		in, want string
	}{
		"upstream redirect":       {307, "http://litellm.litellm.svc:4000/ui/", "https://ach.example.com/ui/"},
		"query kept":              {302, "http://litellm.litellm.svc:4000/ui/login?next=%2Fui", "https://ach.example.com/ui/login?next=%2Fui"},
		"relative passes":         {307, "/ui/", "/ui/"},
		"other host passes":       {302, "https://accounts.google.com/o/oauth2", "https://accounts.google.com/o/oauth2"},
		"non-redirect untouched":  {200, "http://litellm.litellm.svc:4000/ui/", "http://litellm.litellm.svc:4000/ui/"},
		"no location stays empty": {307, "", ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			resp := &http.Response{StatusCode: c.status, Header: http.Header{}}
			if c.in != "" {
				resp.Header.Set("Location", c.in)
			}
			rewriteLocation(resp, up, pub)
			if got := resp.Header.Get("Location"); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}
