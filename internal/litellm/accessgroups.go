// Copyright 2026 ACKstorm
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package litellm

import "context"

// DeleteAccessGroup issues DELETE /access-groups/<name>. Called from
// EnvironmentReconciler at Hub §6.5 step 2 — the runtime barrier. Once
// the LiteLLM access group named <environment> is deleted, every ek_
// still bound to the Environment fails forwarding at LiteLLM regardless
// of ACH cache state, which is the property the finalizer drain relies
// on.
//
// §7.7 idempotent-delete contract: makeRequest treats DELETE 404 as
// success, so a re-reconcile after a partially-completed §6.5 sequence
// does NOT generate a spurious error.
func (c *RESTClient) DeleteAccessGroup(ctx context.Context, name string) error {
	_, err := c.makeRequest(ctx, "DELETE", "/access-groups/"+name, nil)
	return err
}

// DeleteTag issues DELETE /tags/<name>. Called from EnvironmentReconciler
// at Hub §6.5 step 3 — clears the budget tag LiteLLM uses for spend
// attribution against the deleted Environment.
//
// Same §7.7 idempotent-delete contract as DeleteAccessGroup.
func (c *RESTClient) DeleteTag(ctx context.Context, name string) error {
	_, err := c.makeRequest(ctx, "DELETE", "/tags/"+name, nil)
	return err
}
