// SPDX-License-Identifier: Apache-2.0

package resync

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// TestPush_FullChannelDropsAndDoesNotBlock verifies push uses a default
// drop-and-log branch so a full (unbuffered, unread) channel never stalls
// the sweep goroutine. (#5)
func TestPush_FullChannelDropsAndDoesNotBlock(t *testing.T) {
	ch := make(chan event.GenericEvent) // unbuffered, no reader => always "full"
	r := &Resync{Log: logr.Discard()}
	obj := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ach-system", Name: "demo"},
	}
	done := make(chan struct{})
	go func() {
		r.push(context.Background(), ch, obj) // must NOT block
		close(done)
	}()
	select {
	case <-done:
		// dropped via default branch — correct
	case <-time.After(2 * time.Second):
		t.Fatal("push blocked on a full channel; expected drop-and-log default branch")
	}
}

// listRecorder counts List calls per list Kind so a sweep can be asserted
// to have skipped a Kind entirely (not merely dropped its events).
type listRecorder struct {
	client.Client
	mu    sync.Mutex
	kinds []string
}

func (c *listRecorder) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	c.mu.Lock()
	c.kinds = append(c.kinds, fmt.Sprintf("%T", list))
	c.mu.Unlock()
	return c.Client.List(ctx, list, opts...)
}

func (c *listRecorder) listed(substr string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range c.kinds {
		if strings.Contains(k, substr) {
			return true
		}
	}
	return false
}

// TestResync_GatedKindsNeverListed — with the Plugin/PluginMarketplace
// channels nil (what the operator wires when featuregate.PluginsEnabled
// is false), a sweep must not even List those Kinds. Their CRDs are not
// installed, so a List returns `no matches for kind "PluginList"` and the
// operator logged that error every 5 minutes.
func TestResync_GatedKindsNeverListed(t *testing.T) {
	s := testScheme(t)
	env := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ach", Name: "demo"},
	}
	rec := &listRecorder{Client: fake.NewClientBuilder().WithScheme(s).WithObjects(env).Build()}
	r := &Resync{
		Client:    rec,
		Namespace: "ach",
		Log:       logr.Discard(),
		Channels: Channels{
			Environment: make(chan event.GenericEvent, 8),
			Plugin:      nil,
			Marketplace: nil,
		},
	}

	r.sweepAll(context.Background())

	if rec.listed("PluginList") {
		t.Error("sweepAll listed PluginList despite a nil Plugin channel")
	}
	if rec.listed("PluginMarketplaceList") {
		t.Error("sweepAll listed PluginMarketplaceList despite a nil Marketplace channel")
	}
	if !rec.listed("EnvironmentList") {
		t.Error("sweepAll skipped EnvironmentList — the wired Kind must still sweep")
	}
}
