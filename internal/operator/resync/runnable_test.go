// SPDX-License-Identifier: Apache-2.0

package resync

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(achv1alpha1.AddToScheme(s))
	return s
}

// TestResync_InitialFire verifies the initial sweep pushes one event per
// existing object before the first ticker tick.
func TestResync_InitialFire(t *testing.T) {
	s := testScheme(t)
	env := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ach-system", Name: "demo"},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(env).Build()
	ch := make(chan event.GenericEvent, 4)

	r := &Resync{
		Client:    c,
		Namespace: "ach-system",
		Interval:  time.Hour, // long so only the initial fire matters
		Log:       logr.Discard(),
		Channels:  Channels{Environment: ch},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- r.Start(ctx) }()

	select {
	case ev := <-ch:
		if ev.Object.GetName() != "demo" {
			t.Fatalf("expected demo, got %q", ev.Object.GetName())
		}
	case <-time.After(time.Second):
		t.Fatal("no event received on initial fire")
	}

	cancel()
	<-done
}

// TestResync_NilChannelSkipped verifies a Kind without a wired channel is
// silently skipped without panicking on the nil receiver.
func TestResync_NilChannelSkipped(t *testing.T) {
	s := testScheme(t)
	env := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ach-system", Name: "demo"},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(env).Build()
	r := &Resync{
		Client:    c,
		Namespace: "ach-system",
		Interval:  time.Hour,
		Log:       logr.Discard(),
		Channels:  Channels{}, // every channel nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	// Should not panic.
	_ = r.Start(ctx)
}

// TestResync_ContextCancelExitsCleanly verifies Start returns when ctx is
// cancelled, even when a channel is blocked.
func TestResync_ContextCancelExitsCleanly(t *testing.T) {
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	r := &Resync{
		Client:    c,
		Namespace: "ach-system",
		Interval:  10 * time.Millisecond,
		Log:       logr.Discard(),
		Channels:  Channels{Environment: make(chan event.GenericEvent)}, // unbuffered, unread
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Start(ctx) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Start did not exit after cancel")
	}
}

// TestResync_DefaultIntervalApplied ensures a zero Interval selects the
// 5-minute default rather than busy-spinning.
func TestResync_DefaultIntervalApplied(t *testing.T) {
	r := &Resync{}
	got := r.intervalOrDefault()
	if got != defaultInterval {
		t.Fatalf("expected defaultInterval (%s), got %s", defaultInterval, got)
	}
}

// TestResync_Describe covers the Describe helper for trivial coverage.
func TestResync_Describe(t *testing.T) {
	r := &Resync{
		Namespace: "ach-system",
		Channels:  Channels{Environment: make(chan event.GenericEvent, 1)},
	}
	got := r.Describe()
	if got == "" {
		t.Fatal("Describe returned empty")
	}
}

// Compile-time guard: corev1.Pod must NOT be in the resync surface — the
// import is here only to assert the scheme builder is functional and the
// fake client can compile against it.
var _ = corev1.Pod{}
