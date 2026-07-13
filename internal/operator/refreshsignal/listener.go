// SPDX-License-Identifier: Apache-2.0

package refreshsignal

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/db"
)

// refreshChannel is the Postgres NOTIFY channel the platform-api's
// /admin/refresh handler fires on. Payload format: "<kind>/<name>".
// Allowed kinds: plugin, prompt, artifact, pluginmarketplace.
const refreshChannel = "ach_refresh"

// allowed kinds and their canonical names. The listener resolves the
// payload kind verbatim against this map.
var allowedKinds = map[string]struct{}{
	"plugin":            {},
	"prompt":            {},
	"artifact":          {},
	"pluginmarketplace": {},
}

// Listener implements manager.Runnable. Run() subscribes to ach_refresh,
// parses each payload as "<kind>/<name>", fetches the CR from the cached
// client, and pushes event.GenericEvent{Object: fetched} into the
// matching channel from Channels.
//
// Why not enqueue directly via a workqueue reference? controller-runtime
// does not expose the per-controller workqueue as a public API; the
// supported indirection is source.Channel (see issue #34 revision note).
type Listener struct {
	// Pool drives the underlying db.NewListener — the listener holds its
	// own pgx.Conn (NOT a pool acquire), so the pool is only used to read
	// the connection string.
	Pool *pgxpool.Pool

	// Namespace is the namespace every Get is scoped to. Operator
	// reconcilers are namespace-scoped (MULTI-01); the listener mirrors
	// that.
	Namespace string

	// Log is the structured logger.
	Log logr.Logger

	// Client is the controller-runtime cached client used to fetch the
	// CR before pushing the GenericEvent. Reads from the informer cache
	// in steady state.
	Client client.Client

	// Channels maps each allowed kind (lowercase) to its send-side
	// chan<- event.GenericEvent. Nil-tolerant per-kind: a missing
	// channel silently drops the signal (still logged at V(1)).
	Channels map[string]chan<- event.GenericEvent
}

// Start implements manager.Runnable. It blocks until ctx is cancelled and
// returns nil on shutdown. Internal listen-session disconnects are
// transparently retried by db.Listener.
func (l *Listener) Start(ctx context.Context) error {
	if l.Pool == nil {
		return fmt.Errorf("refreshsignal.Listener: Pool is required")
	}
	if l.Client == nil {
		return fmt.Errorf("refreshsignal.Listener: Client is required")
	}
	listener := db.NewListener(l.Pool, l.Log)
	listener.Subscribe(refreshChannel, l.handle(ctx))
	l.Log.Info("refresh-signal listener starting",
		"channel", refreshChannel, "namespace", l.Namespace)
	return listener.Run(ctx)
}

// handle returns the per-notification callback. The closure captures the
// Start ctx so a cancelled Start cancels the in-flight Get/push.
//
// Per db.Listener contract, the handler MUST NOT block — it runs in the
// listener's goroutine and a slow handler stalls every other channel
// subscription. Get is cache-served (sub-ms) and the channel push is
// non-blocking via select on ctx.Done. The contract is upheld.
func (l *Listener) handle(parentCtx context.Context) db.Handler {
	return func(payload string) {
		kind, name, ok := splitPayload(payload)
		if !ok {
			l.Log.V(1).Info("refresh-signal: malformed payload (no '/')",
				"payload", payload)
			return
		}
		if _, allowed := allowedKinds[kind]; !allowed {
			l.Log.V(1).Info("refresh-signal: unknown kind",
				"kind", kind, "payload", payload)
			return
		}
		ch, ok := l.Channels[kind]
		if !ok || ch == nil {
			l.Log.V(1).Info("refresh-signal: no channel wired for kind",
				"kind", kind, "name", name)
			return
		}
		obj, err := l.fetch(parentCtx, kind, name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				l.Log.V(1).Info("refresh-signal: CR not found (likely deleted)",
					"kind", kind, "name", name)
				return
			}
			l.Log.Error(err, "refresh-signal: fetch failed",
				"kind", kind, "name", name)
			return
		}
		select {
		case ch <- event.GenericEvent{Object: obj}:
		case <-parentCtx.Done():
		default:
			l.Log.Info("refresh-signal: channel full; dropping signal",
				"kind", kind, "name", name)
		}
	}
}

// fetch loads the matching CR from the controller-runtime cached client.
// The kind switch is exhaustive over allowedKinds; defensive default
// returns an explicit error rather than silently dropping.
func (l *Listener) fetch(ctx context.Context, kind, name string) (client.Object, error) {
	key := types.NamespacedName{Namespace: l.Namespace, Name: name}
	switch kind {
	case "plugin":
		var cr achv1alpha1.Plugin
		if err := l.Client.Get(ctx, key, &cr); err != nil {
			return nil, err
		}
		return &cr, nil
	case "prompt":
		var cr achv1alpha1.Prompt
		if err := l.Client.Get(ctx, key, &cr); err != nil {
			return nil, err
		}
		return &cr, nil
	case "artifact":
		var cr achv1alpha1.Artifact
		if err := l.Client.Get(ctx, key, &cr); err != nil {
			return nil, err
		}
		return &cr, nil
	case "pluginmarketplace":
		var cr achv1alpha1.PluginMarketplace
		if err := l.Client.Get(ctx, key, &cr); err != nil {
			return nil, err
		}
		return &cr, nil
	default:
		return nil, fmt.Errorf("refresh-signal: unknown kind %q", kind)
	}
}

// splitPayload splits "<kind>/<name>" into (kind, name, true). Returns
// ("","",false) on missing '/' or empty parts. Note: <name> may contain
// '/' segments per Kubernetes resource-name rules (no '/' allowed in
// metadata.name), so strings.Cut handles the simple two-part case.
func splitPayload(payload string) (kind, name string, ok bool) {
	kind, name, ok = strings.Cut(payload, "/")
	if !ok || kind == "" || name == "" {
		return "", "", false
	}
	return kind, name, true
}
