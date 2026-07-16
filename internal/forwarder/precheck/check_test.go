// SPDX-License-Identifier: Apache-2.0

package precheck

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

const testNS = "ach-system"

// fakeEnvProvider satisfies EnvProvider with an in-memory map keyed by
// name. Tests construct it with newEnvProvider(rows...).
type fakeEnvProvider struct {
	byName map[string]db.EnvironmentRow
}

func (f *fakeEnvProvider) Get(name string) (*db.EnvironmentRow, bool) {
	r, ok := f.byName[name]
	if !ok {
		return nil, false
	}
	return &r, true
}

func (f *fakeEnvProvider) List() []db.EnvironmentRow {
	out := make([]db.EnvironmentRow, 0, len(f.byName))
	for _, r := range f.byName {
		out = append(out, r)
	}
	return out
}

func newEnvProvider(rows ...db.EnvironmentRow) *fakeEnvProvider {
	f := &fakeEnvProvider{byName: map[string]db.EnvironmentRow{}}
	for _, r := range rows {
		f.byName[r.Name] = r
	}
	return f
}

type mockTeamsResolver struct {
	teams []string
	err   error
	calls int
}

func (m *mockTeamsResolver) Resolve(_ context.Context, _ string) ([]string, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return m.teams, nil
}

func envRow(name string, mcps, a2as, authorizedTeams []string, terminating bool) db.EnvironmentRow {
	row := db.EnvironmentRow{
		Namespace:         testNS,
		Name:              name,
		AuthorizedTeams:   authorizedTeams,
		RuntimeMCPServers: mcps,
		RuntimeA2AAgents:  a2as,
	}
	if terminating {
		now := time.Now()
		row.DeletionTimestamp = &now
	}
	return row
}

// PC1: invalid key type → ErrInvalidKeyType.
func TestCheckMCP_PC1_InvalidKeyType(t *testing.T) {
	kc := middleware.KeyContext{KeyType: keys.BearerPrefix(""), OwnerEmail: "u@e", Environment: "demo"}
	deps := Deps{EnvProvider: newEnvProvider(), TeamsResolver: &mockTeamsResolver{}}
	if err := CheckMCP(context.Background(), kc, "foo", deps); !errors.Is(err, ErrInvalidKeyType) {
		t.Errorf("err = %v; want ErrInvalidKeyType", err)
	}
}

func TestCheckMCP_PC2_EkAuthorized(t *testing.T) {
	env := envRow("demo", []string{"foo", "bar"}, nil, nil, false)
	kc := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "demo"}
	deps := Deps{EnvProvider: newEnvProvider(env), TeamsResolver: &mockTeamsResolver{}}
	if err := CheckMCP(context.Background(), kc, "foo", deps); err != nil {
		t.Errorf("err = %v; want nil", err)
	}
}

func TestCheckMCP_PC3_EkNameMissing(t *testing.T) {
	env := envRow("demo", []string{"foo", "bar"}, nil, nil, false)
	kc := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "demo"}
	deps := Deps{EnvProvider: newEnvProvider(env), TeamsResolver: &mockTeamsResolver{}}
	if err := CheckMCP(context.Background(), kc, "baz", deps); !errors.Is(err, ErrUnauthorizedResource) {
		t.Errorf("err = %v; want ErrUnauthorizedResource", err)
	}
}

func TestCheckMCP_PC4_EkEnvNotFound(t *testing.T) {
	kc := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "missing"}
	deps := Deps{EnvProvider: newEnvProvider(), TeamsResolver: &mockTeamsResolver{}}
	err := CheckMCP(context.Background(), kc, "foo", deps)
	if !errors.Is(err, ErrUnauthorizedResource) {
		t.Errorf("err = %v; want ErrUnauthorizedResource (D-15 narrow)", err)
	}
}

func TestCheckMCP_PC5_EkTerminating(t *testing.T) {
	env := envRow("demo", []string{"foo"}, nil, nil, true)
	kc := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "demo"}
	deps := Deps{EnvProvider: newEnvProvider(env), TeamsResolver: &mockTeamsResolver{}}
	if err := CheckMCP(context.Background(), kc, "foo", deps); !errors.Is(err, ErrUnauthorizedResource) {
		t.Errorf("err = %v; want ErrUnauthorizedResource (terminating)", err)
	}
}

func TestCheckMCP_PC7_PkAuthorized(t *testing.T) {
	env := envRow("env1", []string{"foo"}, nil, []string{"team-a"}, false)
	kc := middleware.KeyContext{KeyType: keys.PrefixPk, OwnerEmail: "u@e"}
	deps := Deps{
		EnvProvider:   newEnvProvider(env),
		TeamsResolver: &mockTeamsResolver{teams: []string{"team-a"}},
	}
	if err := CheckMCP(context.Background(), kc, "foo", deps); err != nil {
		t.Errorf("err = %v; want nil", err)
	}
}

func TestCheckMCP_PC8_PkNoIntersection(t *testing.T) {
	env := envRow("env1", []string{"foo"}, nil, []string{"team-a"}, false)
	kc := middleware.KeyContext{KeyType: keys.PrefixPk, OwnerEmail: "u@e"}
	deps := Deps{
		EnvProvider:   newEnvProvider(env),
		TeamsResolver: &mockTeamsResolver{teams: []string{"team-x"}},
	}
	if err := CheckMCP(context.Background(), kc, "foo", deps); !errors.Is(err, ErrUnauthorizedTeam) {
		t.Errorf("err = %v; want ErrUnauthorizedTeam", err)
	}
}

func TestCheckMCP_PC9_PkLiteLLMUnreachable(t *testing.T) {
	env := envRow("env1", []string{"foo"}, nil, []string{"team-a"}, false)
	kc := middleware.KeyContext{KeyType: keys.PrefixPk, OwnerEmail: "u@e"}
	deps := Deps{
		EnvProvider:   newEnvProvider(env),
		TeamsResolver: &mockTeamsResolver{err: errors.New("connection refused")},
	}
	if err := CheckMCP(context.Background(), kc, "foo", deps); !errors.Is(err, ErrLiteLLMUnreachable) {
		t.Errorf("err = %v; want ErrLiteLLMUnreachable", err)
	}
}

func TestCheckMCP_PC10_PkEmptyCallerTeams(t *testing.T) {
	env := envRow("env1", []string{"foo"}, nil, []string{"team-a"}, false)
	kc := middleware.KeyContext{KeyType: keys.PrefixPk, OwnerEmail: "u@e"}
	deps := Deps{
		EnvProvider:   newEnvProvider(env),
		TeamsResolver: &mockTeamsResolver{teams: []string{}},
	}
	if err := CheckMCP(context.Background(), kc, "foo", deps); !errors.Is(err, ErrUnauthorizedTeam) {
		t.Errorf("err = %v; want ErrUnauthorizedTeam", err)
	}
}

func TestCheckMCP_PC11_PkUnionSemantics(t *testing.T) {
	env1 := envRow("env1", []string{"foo"}, nil, []string{"team-x"}, false)
	env2 := envRow("env2", []string{"foo"}, nil, []string{"team-a"}, false)
	kc := middleware.KeyContext{KeyType: keys.PrefixPk, OwnerEmail: "u@e"}
	deps := Deps{
		EnvProvider:   newEnvProvider(env1, env2),
		TeamsResolver: &mockTeamsResolver{teams: []string{"team-a"}},
	}
	if err := CheckMCP(context.Background(), kc, "foo", deps); err != nil {
		t.Errorf("err = %v; want nil (env2 matches)", err)
	}
}

func TestCheckMCP_PC12_PkPicksCorrectEnv(t *testing.T) {
	env1 := envRow("env1", []string{"foo"}, nil, []string{"team-a"}, false)
	env2 := envRow("env2", []string{"bar"}, nil, []string{"team-a"}, false)
	kc := middleware.KeyContext{KeyType: keys.PrefixPk, OwnerEmail: "u@e"}
	deps := Deps{
		EnvProvider:   newEnvProvider(env1, env2),
		TeamsResolver: &mockTeamsResolver{teams: []string{"team-a"}},
	}
	if err := CheckMCP(context.Background(), kc, "bar", deps); err != nil {
		t.Errorf("err = %v; want nil (env2 hosts bar)", err)
	}
}

func TestCheckA2A_PC13_EkPath(t *testing.T) {
	env := envRow("demo", nil, []string{"agent-foo", "agent-bar"}, nil, false)
	kc := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "demo"}
	deps := Deps{EnvProvider: newEnvProvider(env), TeamsResolver: &mockTeamsResolver{}}

	if err := CheckA2A(context.Background(), kc, "agent-foo", deps); err != nil {
		t.Errorf("PC13-authorized: err = %v; want nil", err)
	}
	if err := CheckA2A(context.Background(), kc, "agent-missing", deps); !errors.Is(err, ErrUnauthorizedResource) {
		t.Errorf("PC13-missing-name: err = %v; want ErrUnauthorizedResource", err)
	}
	env2 := envRow("demo2", []string{"server-x"}, nil, nil, false)
	kc2 := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "demo2"}
	deps2 := Deps{EnvProvider: newEnvProvider(env2), TeamsResolver: &mockTeamsResolver{}}
	if err := CheckA2A(context.Background(), kc2, "server-x", deps2); !errors.Is(err, ErrUnauthorizedResource) {
		t.Errorf("PC13-isolation: a2a route should not see mcp list; err = %v", err)
	}
}

func TestCheckMCP_PC14_PkTerminatingExcluded(t *testing.T) {
	envTerminating := envRow("env-old", []string{"foo"}, nil, []string{"team-a"}, true)
	kc := middleware.KeyContext{KeyType: keys.PrefixPk, OwnerEmail: "u@e"}
	deps := Deps{
		EnvProvider:   newEnvProvider(envTerminating),
		TeamsResolver: &mockTeamsResolver{teams: []string{"team-a"}},
	}
	if err := CheckMCP(context.Background(), kc, "foo", deps); !errors.Is(err, ErrUnauthorizedTeam) {
		t.Errorf("err = %v; want ErrUnauthorizedTeam (terminating env should be skipped)", err)
	}
	envActive := envRow("env-new", []string{"foo"}, nil, []string{"team-a"}, false)
	deps2 := Deps{
		EnvProvider:   newEnvProvider(envTerminating, envActive),
		TeamsResolver: &mockTeamsResolver{teams: []string{"team-a"}},
	}
	if err := CheckMCP(context.Background(), kc, "foo", deps2); err != nil {
		t.Errorf("err = %v; want nil (active env grants access)", err)
	}
}
