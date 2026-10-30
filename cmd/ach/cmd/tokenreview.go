// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"

	authnv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	authnv1client "k8s.io/client-go/kubernetes/typed/authentication/v1"
	"k8s.io/client-go/rest"

	"github.com/ackstorm/ach/internal/platformapi/envkeys"
)

// inClusterTokenReview returns the TokenReview call POST /platform/keys/exchange
// makes as the platform-api ServiceAccount (chart: system:auth-delegator). An
// error means no in-cluster config, and the route stays unmounted.
func inClusterTokenReview() (envkeys.TokenReviewFunc, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	c, err := authnv1client.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, token, audience string) (authnv1.TokenReviewStatus, error) {
		tr, err := c.TokenReviews().Create(ctx, &authnv1.TokenReview{
			Spec: authnv1.TokenReviewSpec{Token: token, Audiences: []string{audience}},
		}, metav1.CreateOptions{})
		if err != nil {
			return authnv1.TokenReviewStatus{}, err
		}
		return tr.Status, nil
	}, nil
}
