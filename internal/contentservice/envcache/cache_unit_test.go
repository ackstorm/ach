// SPDX-License-Identifier: Apache-2.0

package envcache

import "testing"

// newForTest returns a *Cache with an empty snapshot and no DB/pool — the
// snapshot is populated directly via store() so Get can be exercised without
// Postgres.
func newForTest() *Cache {
	c := &Cache{}
	empty := map[string]EnvRow{}
	c.rows.Store(&empty)
	return c
}

// store swaps the in-memory snapshot. White-box test seam only.
func (c *Cache) store(m map[string]EnvRow) {
	c.rows.Store(&m)
}

func TestGet_HitMissAndKeying(t *testing.T) {
	c := newForTest()
	c.store(map[string]EnvRow{"demo": {AuthorizedTeams: []string{"t1"}}})
	if row, ok := c.Get("ach-system", "demo"); !ok || len(row.AuthorizedTeams) != 1 {
		t.Fatalf("Get(demo) = %+v, %v; want hit", row, ok)
	}
	if _, ok := c.Get("ach-system", "absent"); ok {
		t.Fatalf("Get(absent) = ok; want miss")
	}
}
