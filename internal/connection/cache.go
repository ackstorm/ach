// SPDX-License-Identifier: Apache-2.0

package connection

import "sync/atomic"

// Cache stores the latest LiteLLMConnection probe result for lock-free readers.
type Cache struct {
	snapshot atomic.Pointer[Snapshot]
}

func NewCache() *Cache {
	return &Cache{}
}

func (c *Cache) Snapshot() Snapshot {
	if snap := c.snapshot.Load(); snap != nil {
		return *snap
	}
	return Snapshot{}
}

func (c *Cache) Rebuild(snap Snapshot) {
	c.snapshot.Store(&snap)
}

var _ CacheReader = (*Cache)(nil)
