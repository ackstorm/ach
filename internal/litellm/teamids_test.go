// SPDX-License-Identifier: Apache-2.0

package litellm_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/litellm/litellmtest"
)

type aliasClient struct {
	*litellmtest.NoopClient
	teams map[string]string // alias -> team_id; an alias absent from it names no team
	err   error
}

func (c aliasClient) ListTeamsByAlias(_ context.Context, alias string) ([]litellm.TeamListEntry, error) {
	if c.err != nil {
		return nil, c.err
	}
	if id, ok := c.teams[alias]; ok {
		return []litellm.TeamListEntry{{TeamID: id, TeamAlias: alias}}, nil
	}
	return nil, nil
}

func TestResolveTeamIDs(t *testing.T) {
	c := aliasClient{NoopClient: &litellmtest.NoopClient{}, teams: map[string]string{"default": "u1", "base": "u2"}}

	ids, err := litellm.ResolveTeamIDs(context.Background(), c, []string{"base", "default"})
	if err != nil || !reflect.DeepEqual(ids, []string{"u2", "u1"}) {
		t.Fatalf("ids=%v err=%v, want [u2 u1] in alias order", ids, err)
	}
	if _, err := litellm.ResolveTeamIDs(context.Background(), c, []string{"default", "nope"}); !errors.Is(err, litellm.ErrNotFound) {
		t.Fatalf("missing alias: err=%v, want litellm.ErrNotFound", err)
	}
	boom := errors.New("litellm down")
	if _, err := litellm.ResolveTeamIDs(context.Background(), aliasClient{NoopClient: &litellmtest.NoopClient{}, err: boom}, []string{"default"}); !errors.Is(err, boom) || errors.Is(err, litellm.ErrNotFound) {
		t.Fatalf("transport error must pass through unchanged, got %v", err)
	}
}
