// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type aliasClient struct {
	*NoopClient
	teams map[string]string // alias -> team_id; an alias absent from it names no team
	err   error
}

func (c aliasClient) ListTeamsByAlias(_ context.Context, alias string) ([]TeamListEntry, error) {
	if c.err != nil {
		return nil, c.err
	}
	if id, ok := c.teams[alias]; ok {
		return []TeamListEntry{{TeamID: id, TeamAlias: alias}}, nil
	}
	return nil, nil
}

func TestResolveTeamIDs(t *testing.T) {
	c := aliasClient{NoopClient: &NoopClient{}, teams: map[string]string{"default": "u1", "base": "u2"}}

	ids, err := ResolveTeamIDs(context.Background(), c, []string{"base", "default"})
	if err != nil || !reflect.DeepEqual(ids, []string{"u2", "u1"}) {
		t.Fatalf("ids=%v err=%v, want [u2 u1] in alias order", ids, err)
	}
	if _, err := ResolveTeamIDs(context.Background(), c, []string{"default", "nope"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing alias: err=%v, want ErrNotFound", err)
	}
	boom := errors.New("litellm down")
	if _, err := ResolveTeamIDs(context.Background(), aliasClient{NoopClient: &NoopClient{}, err: boom}, []string{"default"}); !errors.Is(err, boom) || errors.Is(err, ErrNotFound) {
		t.Fatalf("transport error must pass through unchanged, got %v", err)
	}
}
