// SPDX-License-Identifier: Apache-2.0

package resync

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// defaultInterval is the steady-state full-resync cadence. 5 minutes is a
// safety-net cadence — the primary event paths are controller-runtime
// informer Watches + the refreshsignal Listener (issue #34 A11). A shorter
// interval would burn apiserver list calls; longer leaves missed-event
// recovery windows wider than the §6.4 staleness expectation.
const defaultInterval = 5 * time.Minute

// Channels bundles one chan<- event.GenericEvent per ACH CR Kind. Each
// reconciler that wires a source.Channel into its SetupWithManager passes
// the receive end; this package owns the send side. Nil entries are
// tolerated — a Kind without a matching channel is skipped each tick.
type Channels struct {
	Environment       chan<- event.GenericEvent
	Plugin            chan<- event.GenericEvent
	Prompt            chan<- event.GenericEvent
	Artifact          chan<- event.GenericEvent
	Marketplace       chan<- event.GenericEvent
	BIP               chan<- event.GenericEvent
	LiteLLMConnection chan<- event.GenericEvent
}

// Resync implements manager.Runnable: every Interval it re-Lists each ACH
// CR Kind in Namespace and pushes a GenericEvent per item into the
// matching channel. Acts as a missed-event safety net for operator
// restart drift, Postgres NOTIFY delivery failure, and Listener
// reconnect gaps.
type Resync struct {
	// Client is the controller-runtime cached client; List() reads from
	// the informer cache in steady state — no apiserver hit when the
	// informer is warm.
	Client client.Client
	// Namespace is the WATCH_NAMESPACE; List() is scoped here.
	Namespace string
	// Interval is the resync cadence. Zero or negative selects defaultInterval.
	Interval time.Duration
	// Log is the structured logger; ticker-progress noise stays at V(1).
	Log logr.Logger
	// Channels carries the per-Kind send ends. Nil-tolerant per-Kind.
	Channels Channels
}

// Start implements manager.Runnable. It blocks until ctx is cancelled,
// firing the resync sweep once immediately (so a freshly-started operator
// drains its informer cache through every reconciler without waiting a
// full Interval) and again on every ticker tick.
func (r *Resync) Start(ctx context.Context) error {
	interval := r.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	r.Log.Info("resync runnable starting", "interval", interval, "namespace", r.Namespace)

	// Initial fire so missed events from the pre-Start gap are picked up
	// without waiting the first Interval. The sweep is idempotent — every
	// receiver enqueues a Reconcile.Request keyed by NamespacedName,
	// which is collapsed by the controller-runtime workqueue.
	r.sweepAll(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			r.Log.Info("resync runnable stopped", "reason", ctx.Err())
			return nil
		case <-ticker.C:
			r.sweepAll(ctx)
		}
	}
}

// sweepAll runs one resync pass across every Kind. Errors per Kind are
// logged but do not abort the sweep — a transient apiserver hiccup on
// one Kind should not block the others.
func (r *Resync) sweepAll(ctx context.Context) {
	r.sweepEnvironments(ctx)
	r.sweepPlugins(ctx)
	r.sweepPrompts(ctx)
	r.sweepArtifacts(ctx)
	r.sweepMarketplaces(ctx)
	r.sweepBIPs(ctx)
	r.sweepLiteLLMConnections(ctx)
}

func (r *Resync) sweepEnvironments(ctx context.Context) {
	if r.Channels.Environment == nil {
		return
	}
	var list achv1alpha1.EnvironmentList
	if err := r.Client.List(ctx, &list, client.InNamespace(r.Namespace)); err != nil {
		r.Log.Error(err, "list environments")
		return
	}
	for i := range list.Items {
		r.push(ctx, r.Channels.Environment, &list.Items[i])
	}
}

func (r *Resync) sweepPlugins(ctx context.Context) {
	if r.Channels.Plugin == nil {
		return
	}
	var list achv1alpha1.PluginList
	if err := r.Client.List(ctx, &list, client.InNamespace(r.Namespace)); err != nil {
		r.Log.Error(err, "list plugins")
		return
	}
	for i := range list.Items {
		r.push(ctx, r.Channels.Plugin, &list.Items[i])
	}
}

func (r *Resync) sweepPrompts(ctx context.Context) {
	if r.Channels.Prompt == nil {
		return
	}
	var list achv1alpha1.PromptList
	if err := r.Client.List(ctx, &list, client.InNamespace(r.Namespace)); err != nil {
		r.Log.Error(err, "list prompts")
		return
	}
	for i := range list.Items {
		r.push(ctx, r.Channels.Prompt, &list.Items[i])
	}
}

func (r *Resync) sweepArtifacts(ctx context.Context) {
	if r.Channels.Artifact == nil {
		return
	}
	var list achv1alpha1.ArtifactList
	if err := r.Client.List(ctx, &list, client.InNamespace(r.Namespace)); err != nil {
		r.Log.Error(err, "list artifacts")
		return
	}
	for i := range list.Items {
		r.push(ctx, r.Channels.Artifact, &list.Items[i])
	}
}

func (r *Resync) sweepMarketplaces(ctx context.Context) {
	if r.Channels.Marketplace == nil {
		return
	}
	var list achv1alpha1.PluginMarketplaceList
	if err := r.Client.List(ctx, &list, client.InNamespace(r.Namespace)); err != nil {
		r.Log.Error(err, "list pluginmarketplaces")
		return
	}
	for i := range list.Items {
		r.push(ctx, r.Channels.Marketplace, &list.Items[i])
	}
}

func (r *Resync) sweepBIPs(ctx context.Context) {
	if r.Channels.BIP == nil {
		return
	}
	var list achv1alpha1.BackendIdentityPolicyList
	if err := r.Client.List(ctx, &list, client.InNamespace(r.Namespace)); err != nil {
		r.Log.Error(err, "list backendidentitypolicies")
		return
	}
	for i := range list.Items {
		r.push(ctx, r.Channels.BIP, &list.Items[i])
	}
}

func (r *Resync) sweepLiteLLMConnections(ctx context.Context) {
	if r.Channels.LiteLLMConnection == nil {
		return
	}
	var list achv1alpha1.LiteLLMConnectionList
	if err := r.Client.List(ctx, &list, client.InNamespace(r.Namespace)); err != nil {
		r.Log.Error(err, "list litellmconnections")
		return
	}
	for i := range list.Items {
		r.push(ctx, r.Channels.LiteLLMConnection, &list.Items[i])
	}
}

// push sends a GenericEvent into ch, returning early if ctx is cancelled
// to avoid blocking shutdown when the receiver has stopped draining.
func (r *Resync) push(ctx context.Context, ch chan<- event.GenericEvent, obj client.Object) {
	select {
	case ch <- event.GenericEvent{Object: obj}:
	case <-ctx.Done():
	}
}

// assert manager.Runnable contract at compile time. The interface is
// satisfied by Start(ctx) error — repeated here to surface the contract
// nominal in the package.
var _ runnable = (*Resync)(nil)

type runnable interface {
	Start(context.Context) error
}

// Describe returns a human-readable summary of which channels are wired.
// Currently unused at runtime but handy in tests.
func (r *Resync) Describe() string {
	return fmt.Sprintf("resync interval=%s namespace=%s env=%t plugin=%t prompt=%t artifact=%t mp=%t bip=%t llmconn=%t",
		r.intervalOrDefault(), r.Namespace,
		r.Channels.Environment != nil, r.Channels.Plugin != nil,
		r.Channels.Prompt != nil, r.Channels.Artifact != nil,
		r.Channels.Marketplace != nil, r.Channels.BIP != nil,
		r.Channels.LiteLLMConnection != nil,
	)
}

func (r *Resync) intervalOrDefault() time.Duration {
	if r.Interval <= 0 {
		return defaultInterval
	}
	return r.Interval
}
