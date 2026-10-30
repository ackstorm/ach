// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	authnv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	authnv1client "k8s.io/client-go/kubernetes/typed/authentication/v1"
	"k8s.io/client-go/rest"

	"github.com/ackstorm/ach/internal/platformapi/envkeys"
)

// tokenReviewTimeout bounds one TokenReview so a hung API server cannot pin
// exchange requests.
const tokenReviewTimeout = 5 * time.Second

// tokenReviewCreator is the TokenReview create call of client-go.
type tokenReviewCreator interface {
	Create(ctx context.Context, tr *authnv1.TokenReview, opts metav1.CreateOptions) (*authnv1.TokenReview, error)
}

func tokenReviewFunc(c tokenReviewCreator, timeout time.Duration) envkeys.TokenReviewFunc {
	return func(ctx context.Context, token, audience string) (authnv1.TokenReviewStatus, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		tr, err := c.Create(ctx, &authnv1.TokenReview{
			Spec: authnv1.TokenReviewSpec{Token: token, Audiences: []string{audience}},
		}, metav1.CreateOptions{})
		if err != nil {
			return authnv1.TokenReviewStatus{}, err
		}
		return tr.Status, nil
	}
}

// inClusterExchangeAuth returns the TokenReview call and the ServiceAccount
// token verifier POST /platform/keys/exchange uses, both as the platform-api
// ServiceAccount: TokenReview needs system:auth-delegator; the issuer and
// JWKS reads need the default system:service-account-issuer-discovery
// binding. An error means no in-cluster config, and the route stays
// unmounted.
func inClusterExchangeAuth() (envkeys.TokenReviewFunc, func(context.Context, string) error, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, nil, err
	}
	c, err := authnv1client.NewForConfig(cfg)
	if err != nil {
		return nil, nil, err
	}
	d, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, nil, err
	}
	return tokenReviewFunc(c.TokenReviews(), tokenReviewTimeout),
		envkeys.NewSATokenVerifier(oidcFetch(d.RESTClient())).Verify, nil
}

func oidcFetch(c rest.Interface) envkeys.OIDCFetchFunc {
	return func(ctx context.Context) (string, []byte, error) {
		cfg, err := c.Get().AbsPath("/.well-known/openid-configuration").DoRaw(ctx)
		if err != nil {
			return "", nil, err
		}
		var doc struct {
			Issuer string `json:"issuer"`
		}
		if err := json.Unmarshal(cfg, &doc); err != nil || doc.Issuer == "" {
			return "", nil, fmt.Errorf("openid-configuration: no issuer")
		}
		jwks, err := c.Get().AbsPath("/openid/v1/jwks").DoRaw(ctx)
		return doc.Issuer, jwks, err
	}
}
