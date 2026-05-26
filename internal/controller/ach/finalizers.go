// SPDX-License-Identifier: Apache-2.0

package ach

// CRD-06: Every ACH custom resource carries a finalizer of the form
// "<kindPlural>" + group suffix from creation. The constants
// below are the canonical names referenced by each kind's reconciler
// when calling controllerutil.AddFinalizer / RemoveFinalizer; no
// reconciler should inline the literal string.
//
// Sister-project convention (ach_litellm/internal/controller/
// litellmconnection_controller.go line 55): the constant lives next to
// its consuming reconciler. ACH lifts the six names to a shared file
// because six reconcilers share the pattern and a single source of
// truth keeps grep-gates honest (one declaration per name; six
// references each).
const (
	environmentFinalizer           = "environments.ach.ackstorm.ai/finalizer"
	pluginFinalizer                = "plugins.ach.ackstorm.ai/finalizer"
	pluginMarketplaceFinalizer     = "pluginmarketplaces.ach.ackstorm.ai/finalizer"
	artifactFinalizer              = "artifacts.ach.ackstorm.ai/finalizer"
	promptFinalizer                = "prompts.ach.ackstorm.ai/finalizer"
	backendIdentityPolicyFinalizer = "backendidentitypolicies.ach.ackstorm.ai/finalizer"
)
