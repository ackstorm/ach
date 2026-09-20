// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"net/http"
	"net/url"
)

// rewriteLocation is nginx's proxy_redirect: a redirect whose Location names
// the upstream's own authority (LiteLLM composes `/` → `/ui/` and friends
// from the Host it received, which is its Service name — Director clears the
// client Host on purpose) is pointed back at ACH's public base. Only the
// scheme + authority change; path and query stay. A Location naming anywhere
// else passes as it came.
func rewriteLocation(resp *http.Response, upstream, publicBase *url.URL) {
	if resp.StatusCode < 300 || resp.StatusCode > 399 {
		return
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || loc.Host != upstream.Host {
		return
	}
	loc.Scheme = publicBase.Scheme
	loc.Host = publicBase.Host
	resp.Header.Set("Location", loc.String())
}
