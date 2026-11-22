// SPDX-License-Identifier: Apache-2.0

// envtest coverage for the prompt content-present gate on
// ExecutionResourcesResolved: an Environment whose spec.context.prompts
// references a Prompt with last_successful_refresh IS NULL holds
// ExecutionResourcesResolved=False until the content is synced.
//
// Reuses setupPluginContentPresentDB, buildPluginTestReconciler and
// capturingEnvStatusClient from environment_plugin_contentpresent_test.go.
// Skipped unless Docker is available.

package ach

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	achdb "github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/db/dbtest"
)

// reconcilePromptEnv creates an Environment referencing refs and runs the
// DB-wired reconciler once, returning the status it wrote.
func reconcilePromptEnv(t *testing.T, pool *pgxpool.Pool, crName string, refs []string) *achv1alpha1.Environment {
	t.Helper()
	ctx := context.Background()
	const ns = WatchNamespace
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: ns},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Runtime:         achv1alpha1.RuntimeBlock{},
			Context:         achv1alpha1.ContextBlock{Prompts: refs},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment CR: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	if !Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		for _, f := range got.Finalizers {
			if f == environmentFinalizer {
				return true
			}
		}
		return false
	}, 10*time.Second, 250*time.Millisecond) {
		t.Fatal("finalizer never added within 10s")
	}

	r := buildPluginTestReconciler(t, ns, pool)
	spy, written := capturingEnvStatusClient(t)
	r.Client = spy
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cr)}); err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}
	final := written()
	if final == nil {
		t.Fatal("DB-wired reconciler issued no successful status update")
	}
	return final
}

// TestEnvPromptContentPresent_NotSynced: an unrefreshed Prompt blocks
// the Environment; once it refreshes, the list empties and the condition is True.
func TestEnvPromptContentPresent_NotSynced(t *testing.T) {
	pool, cleanup := setupPluginContentPresentDB(t)
	defer cleanup()
	ctx := context.Background()

	row := achdb.PromptRow{
		Namespace:           WatchNamespace,
		Name:                "my-prompt",
		MaxStalenessSeconds: 86400,
		ResourceVersion:     "1",
	}
	if err := dbtest.UpsertPrompt(ctx, pool, row); err != nil {
		t.Fatalf("seed prompts row: %v", err)
	}

	final := reconcilePromptEnv(t, pool, "test-env-prompt-not-synced", []string{"my-prompt"})
	cond := executionResolved(t, final)
	if cond.Status != metav1.ConditionFalse {
		t.Errorf("ExecutionResourcesResolved.Status = %q; want False. message=%q", cond.Status, cond.Message)
	}
	if !strings.Contains(cond.Message, "prompts not content-present") {
		t.Errorf("message = %q; want it to contain \"prompts not content-present\"", cond.Message)
	}
	if got := final.Status.UnresolvedContextPrompts; len(got) != 1 || got[0] != "my-prompt" {
		t.Errorf("UnresolvedContextPrompts = %v; want [my-prompt]", got)
	}

	now := time.Now().UTC()
	row.LastSuccessfulRefresh = &now
	row.StorageLocation = "/cache/prompt/my-prompt"
	if err := dbtest.UpsertPrompt(ctx, pool, row); err != nil {
		t.Fatalf("mark prompts row synced: %v", err)
	}

	final = reconcilePromptEnv(t, pool, "test-env-prompt-synced", []string{"my-prompt"})
	cond = executionResolved(t, final)
	if cond.Status != metav1.ConditionTrue {
		t.Errorf("ExecutionResourcesResolved.Status = %q; want True. message=%q", cond.Status, cond.Message)
	}
	if len(final.Status.UnresolvedContextPrompts) != 0 {
		t.Errorf("UnresolvedContextPrompts = %v; want []", final.Status.UnresolvedContextPrompts)
	}
}
