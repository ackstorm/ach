// SPDX-License-Identifier: Apache-2.0

package ach

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// setupWithResync registers a For(obj)-only controller that also drains the
// operator resync channel when one is wired (issue #34 A10/A11).
func setupWithResync(mgr ctrl.Manager, r reconcile.Reconciler, obj client.Object, name string, resync chan event.GenericEvent) error {
	b := ctrl.NewControllerManagedBy(mgr).For(obj).Named(name)
	if resync != nil {
		b = b.WatchesRawSource(source.Channel(resync, &handler.EnqueueRequestForObject{}))
	}
	return b.Complete(r)
}
