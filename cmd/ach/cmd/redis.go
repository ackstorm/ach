// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"crypto/tls"

	"github.com/redis/go-redis/v9"
)

// newRedisClient builds the Valkey/Redis client every mode uses; TLS is
// opt-in via ACH_REDIS_TLS with a TLS 1.2 floor.
func newRedisClient(addr, password string, db int, useTLS bool) *redis.Client {
	opts := &redis.Options{Addr: addr, Password: password, DB: db}
	if useTLS {
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12} //nolint:gosec
	}
	return redis.NewClient(opts)
}
