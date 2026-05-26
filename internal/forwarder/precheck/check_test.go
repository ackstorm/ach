// SPDX-License-Identifier: Apache-2.0

package precheck

import (
	"context"
	"errors"
	"testing"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testNS = "ach-system"

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(achv1alpha1.AddToScheme(s))
	return s
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

func envFixture(name string, mcps, a2as, authorizedTeams []string, terminating bool) *achv1alpha1.Environment {
	env := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNS,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			Runtime: achv1alpha1.RuntimeBlock{
				MCPServers: mcps,
				A2AAgents:  a2as,
			},
			AuthorizedTeams: authorizedTeams,
		},
	}
	if terminating {
		now := metav1.Now()
		env.ObjectMeta.DeletionTimestamp = &now
		env.ObjectMeta.Finalizers = []string{"ach.ackstorm.ai/test"}
	}
	return env
}

func newFakeClient(t *testing.T, envs ...*achv1alpha1.Environment) client.Client {
	t.Helper()
	objs := make([]client.Object, 0, len(envs))
	for _, e := range envs {
		objs = append(objs, e)
	}
	return fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(objs...).Build()
}

// PC1: invalid key type → ErrInvalidKeyType.
func TestCheckMCP_PC1_InvalidKeyType(t *testing.T) {
	kc := middleware.KeyContext{KeyType: keys.BearerPrefix(""), OwnerEmail: "u@e", Environment: "demo"}
	deps := Deps{K8sClient: newFakeClient(t), TeamsResolver: &mockTeamsResolver{}, Namespace: testNS}
	if err := CheckMCP(context.Background(), kc, "foo", deps); !errors.Is(err, ErrInvalidKeyType) {
		t.Errorf("err = %v; want ErrInvalidKeyType", err)
	}
}

func TestCheckMCP_PC2_EkAuthorized(t *testing.T) {
	env := envFixture("demo", []string{"foo", "bar"}, nil, nil, false)
	kc := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "demo"}
	deps := Deps{K8sClient: newFakeClient(t, env), TeamsResolver: &mockTeamsResolver{}, Namespace: testNS}
	if err := CheckMCP(context.Background(), kc, "foo", deps); err != nil {
		t.Errorf("err = %v; want nil", err)
	}
}

func TestCheckMCP_PC3_EkNameMissing(t *testing.T) {
	env := envFixture("demo", []string{"foo", "bar"}, nil, nil, false)
	kc := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "demo"}
	deps := Deps{K8sClient: newFakeClient(t, env), TeamsResolver: &mockTeamsResolver{}, Namespace: testNS}
	if err := CheckMCP(context.Background(), kc, "baz", deps); !errors.Is(err, ErrUnauthorizedResource) {
		t.Errorf("err = %v; want ErrUnauthorizedResource", err)
	}
}

func TestCheckMCP_PC4_EkEnvNotFound(t *testing.T) {
	kc := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "missing"}
	deps := Deps{K8sClient: newFakeClient(t), TeamsResolver: &mockTeamsResolver{}, Namespace: testNS}
	err := CheckMCP(context.Background(), kc, "foo", deps)
	if !errors.Is(err, ErrUnauthorizedResource) {
		t.Errorf("err = %v; want ErrUnauthorizedResource (D-15 narrow)", err)
	}
	if errors.Is(err, ErrEnvironmentNotFound) {
		t.Error("ErrEnvironmentNotFound is reserved; missing-env must narrow to ErrUnauthorizedResource")
	}
}

func TestCheckMCP_PC5_EkTerminating(t *testing.T) {
	env := envFixture("demo", []string{"foo"}, nil, nil, true)
	kc := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "demo"}
	deps := Deps{K8sClient: newFakeClient(t, env), TeamsResolver: &mockTeamsResolver{}, Namespace: testNS}
	if err := CheckMCP(context.Background(), kc, "foo", deps); !errors.Is(err, ErrUnauthorizedResource) {
		t.Errorf("err = %v; want ErrUnauthorizedResource (terminating)", err)
	}
}

func TestCheckMCP_PC7_PkAuthorized(t *testing.T) {
	env := envFixture("env1", []string{"foo"}, nil, []string{"team-a"}, false)
	kc := middleware.KeyContext{KeyType: keys.PrefixPk, OwnerEmail: "u@e"}
	deps := Deps{
		K8sClient:     newFakeClient(t, env),
		TeamsResolver: &mockTeamsResolver{teams: []string{"team-a"}},
		Namespace:     testNS,
	}
	if err := CheckMCP(context.Background(), kc, "foo", deps); err != nil {
		t.Errorf("err = %v; want nil", err)
	}
}

func TestCheckMCP_PC8_PkNoIntersection(t *testing.T) {
	env := envFixture("env1", []string{"foo"}, nil, []string{"team-a"}, false)
	kc := middleware.KeyContext{KeyType: keys.PrefixPk, OwnerEmail: "u@e"}
	deps := Deps{
		K8sClient:     newFakeClient(t, env),
		TeamsResolver: &mockTeamsResolver{teams: []string{"team-x"}},
		Namespace:     testNS,
	}
	if err := CheckMCP(context.Background(), kc, "foo", deps); !errors.Is(err, ErrUnauthorizedTeam) {
		t.Errorf("err = %v; want ErrUnauthorizedTeam", err)
	}
}

func TestCheckMCP_PC9_PkLiteLLMUnreachable(t *testing.T) {
	env := envFixture("env1", []string{"foo"}, nil, []string{"team-a"}, false)
	kc := middleware.KeyContext{KeyType: keys.PrefixPk, OwnerEmail: "u@e"}
	deps := Deps{
		K8sClient:     newFakeClient(t, env),
		TeamsResolver: &mockTeamsResolver{err: errors.New("connection refused")},
		Namespace:     testNS,
	}
	if err := CheckMCP(context.Background(), kc, "foo", deps); !errors.Is(err, ErrLiteLLMUnreachable) {
		t.Errorf("err = %v; want ErrLiteLLMUnreachable", err)
	}
}

func TestCheckMCP_PC10_PkEmptyCallerTeams(t *testing.T) {
	env := envFixture("env1", []string{"foo"}, nil, []string{"team-a"}, false)
	kc := middleware.KeyContext{KeyType: keys.PrefixPk, OwnerEmail: "u@e"}
	deps := Deps{
		K8sClient:     newFakeClient(t, env),
		TeamsResolver: &mockTeamsResolver{teams: []string{}},
		Namespace:     testNS,
	}
	if err := CheckMCP(context.Background(), kc, "foo", deps); !errors.Is(err, ErrUnauthorizedTeam) {
		t.Errorf("err = %v; want ErrUnauthorizedTeam", err)
	}
}

func TestCheckMCP_PC11_PkUnionSemantics(t *testing.T) {
	env1 := envFixture("env1", []string{"foo"}, nil, []string{"team-x"}, false)
	env2 := envFixture("env2", []string{"foo"}, nil, []string{"team-a"}, false)
	kc := middleware.KeyContext{KeyType: keys.PrefixPk, OwnerEmail: "u@e"}
	deps := Deps{
		K8sClient:     newFakeClient(t, env1, env2),
		TeamsResolver: &mockTeamsResolver{teams: []string{"team-a"}},
		Namespace:     testNS,
	}
	if err := CheckMCP(context.Background(), kc, "foo", deps); err != nil {
		t.Errorf("err = %v; want nil (env2 matches)", err)
	}
}

func TestCheckMCP_PC12_PkPicksCorrectEnv(t *testing.T) {
	env1 := envFixture("env1", []string{"foo"}, nil, []string{"team-a"}, false)
	env2 := envFixture("env2", []string{"bar"}, nil, []string{"team-a"}, false)
	kc := middleware.KeyContext{KeyType: keys.PrefixPk, OwnerEmail: "u@e"}
	deps := Deps{
		K8sClient:     newFakeClient(t, env1, env2),
		TeamsResolver: &mockTeamsResolver{teams: []string{"team-a"}},
		Namespace:     testNS,
	}
	if err := CheckMCP(context.Background(), kc, "bar", deps); err != nil {
		t.Errorf("err = %v; want nil (env2 hosts bar)", err)
	}
}

func TestCheckA2A_PC13_EkPath(t *testing.T) {
	env := envFixture("demo", nil, []string{"agent-foo", "agent-bar"}, nil, false)
	kc := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "demo"}
	deps := Deps{K8sClient: newFakeClient(t, env), TeamsResolver: &mockTeamsResolver{}, Namespace: testNS}

	if err := CheckA2A(context.Background(), kc, "agent-foo", deps); err != nil {
		t.Errorf("PC13-authorized: err = %v; want nil", err)
	}
	if err := CheckA2A(context.Background(), kc, "agent-missing", deps); !errors.Is(err, ErrUnauthorizedResource) {
		t.Errorf("PC13-missing-name: err = %v; want ErrUnauthorizedResource", err)
	}
	env2 := envFixture("demo2", []string{"server-x"}, nil, nil, false)
	kc2 := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "demo2"}
	deps2 := Deps{K8sClient: newFakeClient(t, env2), TeamsResolver: &mockTeamsResolver{}, Namespace: testNS}
	if err := CheckA2A(context.Background(), kc2, "server-x", deps2); !errors.Is(err, ErrUnauthorizedResource) {
		t.Errorf("PC13-isolation: a2a route should not see mcp list; err = %v", err)
	}
}

func TestCheckMCP_PC14_PkTerminatingExcluded(t *testing.T) {
	envTerminating := envFixture("env-old", []string{"foo"}, nil, []string{"team-a"}, true)
	kc := middleware.KeyContext{KeyType: keys.PrefixPk, OwnerEmail: "u@e"}
	deps := Deps{
		K8sClient:     newFakeClient(t, envTerminating),
		TeamsResolver: &mockTeamsResolver{teams: []string{"team-a"}},
		Namespace:     testNS,
	}
	if err := CheckMCP(context.Background(), kc, "foo", deps); !errors.Is(err, ErrUnauthorizedTeam) {
		t.Errorf("err = %v; want ErrUnauthorizedTeam (terminating env should be skipped)", err)
	}
	envActive := envFixture("env-new", []string{"foo"}, nil, []string{"team-a"}, false)
	deps2 := Deps{
		K8sClient:     newFakeClient(t, envTerminating, envActive),
		TeamsResolver: &mockTeamsResolver{teams: []string{"team-a"}},
		Namespace:     testNS,
	}
	if err := CheckMCP(context.Background(), kc, "foo", deps2); err != nil {
		t.Errorf("err = %v; want nil (active env grants access)", err)
	}
}
