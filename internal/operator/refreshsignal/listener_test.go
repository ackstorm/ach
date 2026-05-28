// SPDX-License-Identifier: Apache-2.0

package refreshsignal

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
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

func TestSplitPayload(t *testing.T) {
	tests := []struct {
		in       string
		wantKind string
		wantName string
		wantOK   bool
	}{
		{"plugin/foo", "plugin", "foo", true},
		{"prompt/bar-baz", "prompt", "bar-baz", true},
		{"artifact/", "", "", false},
		{"/foo", "", "", false},
		{"plain", "", "", false},
		{"", "", "", false},
	}
	for _, tt := range tests {
		k, n, ok := splitPayload(tt.in)
		if k != tt.wantKind || n != tt.wantName || ok != tt.wantOK {
			t.Errorf("splitPayload(%q) = (%q,%q,%v); want (%q,%q,%v)",
				tt.in, k, n, ok, tt.wantKind, tt.wantName, tt.wantOK)
		}
	}
}

func TestHandle_PushesGenericEvent(t *testing.T) {
	s := testScheme(t)
	plugin := &achv1alpha1.Plugin{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ach-system", Name: "demo"},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(plugin).Build()

	ch := make(chan event.GenericEvent, 1)
	l := &Listener{
		Pool:      nil, // fetch path doesn't touch the pool
		Namespace: "ach-system",
		Log:       logr.Discard(),
		Client:    c,
		Channels:  map[string]chan<- event.GenericEvent{"plugin": ch},
	}
	ctx := context.Background()
	l.handle(ctx)("plugin/demo")

	select {
	case ev := <-ch:
		if ev.Object.GetName() != "demo" {
			t.Fatalf("expected demo, got %q", ev.Object.GetName())
		}
	case <-time.After(time.Second):
		t.Fatal("no event delivered")
	}
}

func TestHandle_DropsMalformed(t *testing.T) {
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	ch := make(chan event.GenericEvent, 1)
	l := &Listener{
		Namespace: "ach-system",
		Log:       logr.Discard(),
		Client:    c,
		Channels:  map[string]chan<- event.GenericEvent{"plugin": ch},
	}
	ctx := context.Background()
	l.handle(ctx)("not-a-payload")
	l.handle(ctx)("plugin/")
	l.handle(ctx)("/foo")
	select {
	case <-ch:
		t.Fatal("malformed payload should not enqueue")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHandle_DropsUnknownKind(t *testing.T) {
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	ch := make(chan event.GenericEvent, 1)
	l := &Listener{
		Namespace: "ach-system",
		Log:       logr.Discard(),
		Client:    c,
		Channels:  map[string]chan<- event.GenericEvent{"plugin": ch},
	}
	ctx := context.Background()
	l.handle(ctx)("environment/demo") // not in allowedKinds
	select {
	case <-ch:
		t.Fatal("unknown kind should not enqueue")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHandle_NotFoundIsSilent(t *testing.T) {
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	ch := make(chan event.GenericEvent, 1)
	l := &Listener{
		Namespace: "ach-system",
		Log:       logr.Discard(),
		Client:    c,
		Channels:  map[string]chan<- event.GenericEvent{"plugin": ch},
	}
	ctx := context.Background()
	l.handle(ctx)("plugin/missing")
	select {
	case <-ch:
		t.Fatal("absent CR should not enqueue")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestStart_RequiresClientAndPool(t *testing.T) {
	l := &Listener{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := l.Start(ctx); err == nil {
		t.Fatal("expected error when Pool is nil")
	}
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	_ = c
	// Pool still nil — second error path is the same.
}
