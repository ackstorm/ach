// SPDX-License-Identifier: Apache-2.0

package contentservice

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// NewK8sPromptLookup returns a PromptContentTypeLookup that resolves
// the Content-Type via a controller-runtime client. The client is
// expected to be backed by a cache (informer) so each GET is local —
// no API-server round-trip per content request.
//
// Lookup strategy: Get Prompt by metadata.name within namespace `ns`.
// v1alpha1 ships everything in `ach-system`; future namespacing would
// require resolving from the {name} URL param at the platform-api
// level — out of scope for this plan.
//
// Missing prompts return ("", nil): the cache file may exist even
// when the CR was deleted (Operator deletion-drain races); we serve
// the file and let the §8 default content-type apply.
func NewK8sPromptLookup(c client.Client, ns string) PromptContentTypeLookup {
	return func(ctx context.Context, name string) (string, error) {
		var p achv1alpha1.Prompt
		err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &p)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return "", nil
			}
			return "", fmt.Errorf("get prompt %s/%s: %w", ns, name, err)
		}
		return p.Spec.ContentType, nil
	}
}
