// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"sync"
	"time"
)

// readCache is a short-TTL cache of master-key GET bodies. The operator
// enables it (EnableReadCache) because every Environment requeues on the
// same 5-minute tick and each one re-lists the same LiteLLM catalogs:
// measured in prod (2026-10-20, 31 Environments) ~215 identical calls per
// tick — /v1/mcp/server, /v1/agents, /v2/team/list, /v1/access_group once
// per Environment and ~74 /team/info for ~12 distinct teams. With the cache
// one tick costs each distinct GET once.
//
// Any write through the client drops the whole cache when it returns, so a
// reconcile's post-write read-back (e.g. the access-group mirror verify) is
// always live. gen guards the race where a GET in flight during a write
// would store a pre-write body after the drop.
type readCache struct {
	ttl time.Duration
	mu  sync.Mutex
	gen uint64
	m   map[string]cachedBody
}

type cachedBody struct {
	body []byte
	at   time.Time
}

// readOnlyPOST lists the POST endpoints that only read. They are never
// cached, but they do not drop the cache either — /tag/info runs on every
// budgeted Environment reconcile and would otherwise empty it each time.
var readOnlyPOST = map[string]bool{"/tag/info": true}

// EnableReadCache turns on the GET cache for this client. Call it once,
// right after NewRESTClient and before the client is shared.
func (c *RESTClient) EnableReadCache(ttl time.Duration) {
	c.cache = &readCache{ttl: ttl, m: map[string]cachedBody{}}
}

func (rc *readCache) get(path string) ([]byte, uint64, bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	e, ok := rc.m[path]
	return e.body, rc.gen, ok && time.Since(e.at) < rc.ttl
}

func (rc *readCache) put(path string, gen uint64, body []byte) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if gen == rc.gen {
		rc.m[path] = cachedBody{body: body, at: time.Now()}
	}
}

func (rc *readCache) drop() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.gen++
	clear(rc.m)
}
