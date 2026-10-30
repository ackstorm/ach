// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"testing"
	"time"

	authnv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type blockingReview struct{}

func (blockingReview) Create(
	ctx context.Context, _ *authnv1.TokenReview, _ metav1.CreateOptions,
) (*authnv1.TokenReview, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestTokenReviewFunc_TimesOut(t *testing.T) {
	start := time.Now()
	_, err := tokenReviewFunc(blockingReview{}, 50*time.Millisecond)(context.Background(), "tok", "aud")
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %s; want a prompt timeout error", err, time.Since(start))
	}
}
