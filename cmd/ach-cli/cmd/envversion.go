// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/ackstorm/ach/internal/cli/httpclient"
	"github.com/ackstorm/ach/internal/cli/hydrate"
	"github.com/ackstorm/ach/internal/cli/state"
)

// fetchEnvVersion asks the server for an Environment's version and items.
func fetchEnvVersion(ctx context.Context, hc *httpclient.Client, env string) (*state.EnvVersion, error) {
	var resp struct {
		Version string `json:"version"`
		Items   []struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
			Rev  string `json:"rev"`
		} `json:"items"`
	}
	body := map[string]string{"environment": env}
	if err := hc.Do(ctx, http.MethodPost, "/platform/hydrate/version", body, &resp); err != nil {
		return nil, err
	}
	v := &state.EnvVersion{Version: resp.Version, Items: make(map[string]string, len(resp.Items))}
	for _, it := range resp.Items {
		v.Items[it.Kind+"/"+it.Name] = it.Rev
	}
	return v, nil
}

// versionChanges lists what a hydrate would change, sorted by kind then name:
// + an item added to the Environment, - one removed, ~ one whose revision moved.
func versionChanges(recorded, current *state.EnvVersion) []hydrate.ItemChange {
	var out []hydrate.ItemChange
	add := func(op hydrate.Op, key string) {
		kind, name, _ := strings.Cut(key, "/")
		out = append(out, hydrate.ItemChange{Op: op, Kind: kind, Name: name})
	}
	for k, rev := range current.Items {
		old, had := recorded.Items[k]
		switch {
		case !had:
			add(hydrate.OpAdd, k)
		case old != rev:
			add(hydrate.OpUpdate, k)
		}
	}
	for k := range recorded.Items {
		if _, ok := current.Items[k]; !ok {
			add(hydrate.OpRemove, k)
		}
	}
	sortChanges(out)
	return out
}

// sortChanges orders changes by kind, then name.
func sortChanges(cs []hydrate.ItemChange) {
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].Kind != cs[j].Kind {
			return cs[i].Kind < cs[j].Kind
		}
		return cs[i].Name < cs[j].Name
	})
}
