// SPDX-License-Identifier: Apache-2.0

package connection

// CacheReader is the connection-cache surface used by dependents and tests.
type CacheReader interface {
	Snapshot() Snapshot
	Rebuild(Snapshot)
}
