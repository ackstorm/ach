// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	capsTTL     = time.Hour
	capsTimeout = 15 * time.Second
)

// CapsSource is what CapsCache reads with the master key (*RESTClient).
type CapsSource interface {
	ListDeploymentCapabilities(ctx context.Context) (map[string]ModelCaps, error)
	ModelGroupAliases(ctx context.Context) (map[string]string, error)
}

// CapsCache is the process-wide admin view (deployment capabilities + alias
// map), refreshed every capsTTL, stale-while-revalidate: a stale copy is
// served at once while ONE background refresh runs; only a cold start waits,
// bounded by the request. A failed refresh logs a warning and keeps the old
// copy (possibly none). It only DESCRIBES models; which ones a caller may use
// is decided elsewhere. ponytail: per replica; replicas refresh independently.
type CapsCache struct {
	src CapsSource
	log *slog.Logger

	mu         sync.Mutex
	at         time.Time
	refreshing chan struct{}
	deps       map[string]ModelCaps
	aliases    map[string]string
}

// NewCapsCache returns an empty cache over src.
func NewCapsCache(src CapsSource, log *slog.Logger) *CapsCache {
	return &CapsCache{src: src, log: log}
}

// Get returns the current deployment capabilities and alias map; both are nil
// when no refresh has ever succeeded.
func (c *CapsCache) Get(ctx context.Context) (map[string]ModelCaps, map[string]string) {
	c.mu.Lock()
	if time.Since(c.at) > capsTTL && c.refreshing == nil {
		done := make(chan struct{})
		c.refreshing = done
		go c.refresh(done)
	}
	loaded, wait := !c.at.IsZero(), c.refreshing
	c.mu.Unlock()
	if !loaded && wait != nil {
		select {
		case <-wait:
		case <-ctx.Done():
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deps, c.aliases
}

func (c *CapsCache) refresh(done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), capsTimeout)
	defer cancel()
	deps, err := c.src.ListDeploymentCapabilities(ctx)
	var aliases map[string]string
	if err == nil {
		aliases, err = c.src.ModelGroupAliases(ctx)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.log.Warn("litellm: model capability refresh failed", "err", err)
	} else {
		c.deps, c.aliases, c.at = deps, aliases, time.Now()
	}
	c.refreshing = nil
	close(done)
}

// DeploymentCaps returns the capabilities of the deployment serving name,
// following its router_settings.model_group_alias entry when it has one.
// LiteLLM's own /v2/model/info?model= filter does not resolve aliases. An
// alias is one hop: a target that is itself an alias is not followed.
func DeploymentCaps(deps map[string]ModelCaps, aliases map[string]string, name string) ModelCaps {
	if target, ok := aliases[name]; ok {
		name = target
	}
	return deps[name]
}
