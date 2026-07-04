// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

func TestAgentProjectionRow_ExposedAndCoords(t *testing.T) {
	a := &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ach-system", Name: "gh", ResourceVersion: "42"},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef: achv1alpha1.LocalObjectRef{Name: "prof"},
			Expose:     &achv1alpha1.ExposeSpec{Service: true, Gateway: true},
			Channels: []achv1alpha1.ChannelSpec{
				{Name: "github-review", Type: "webhook", Source: "github"},
				{Name: "nightly", Type: "cron"},
			},
		},
	}
	row := agentProjectionRow(a, true)
	if !row.Exposed {
		t.Fatal("Exposed should be true (expose.gateway=true)")
	}
	if row.ServiceName != "achagent-gh" {
		t.Fatalf("ServiceName = %q, want achagent-gh", row.ServiceName)
	}
	if row.ServicePort != 8080 {
		t.Fatalf("ServicePort = %d, want 8080", row.ServicePort)
	}
	if !row.Ready {
		t.Fatal("Ready should be true")
	}
	if row.ProfileRef != "prof" {
		t.Fatalf("ProfileRef = %q, want prof", row.ProfileRef)
	}
	if row.ResourceVersion != "42" {
		t.Fatalf("ResourceVersion = %q, want 42", row.ResourceVersion)
	}
	if len(row.Channels) != 2 || row.Channels[0].Source != "github" || row.Channels[1].Type != "cron" {
		t.Fatalf("Channels = %+v", row.Channels)
	}
}

// A private-in-cluster agent (a2a peer): Service exists so peers can reach it,
// but it is NOT exposed on the shared gateway.
func TestAgentProjectionRow_ServiceOnly_NotExposed(t *testing.T) {
	a := &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "peer"},
		Spec: achv1alpha1.ACHAgentSpec{
			Expose:   &achv1alpha1.ExposeSpec{Service: true},
			Channels: []achv1alpha1.ChannelSpec{{Name: "a2a-in", Type: "a2a"}},
		},
	}
	row := agentProjectionRow(a, true)
	if row.Exposed {
		t.Fatal("service-only agent must not be gateway-exposed")
	}
	if row.ServiceName != "achagent-peer" || row.ServicePort != 8080 {
		t.Fatalf("service-only agent must have Service coords, got %q %d", row.ServiceName, row.ServicePort)
	}
}

// A fully private agent (no expose block): no Service, not exposed.
func TestAgentProjectionRow_Private_NoServiceCoords(t *testing.T) {
	a := &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "c"},
		Spec:       achv1alpha1.ACHAgentSpec{Channels: []achv1alpha1.ChannelSpec{{Name: "n", Type: "cron"}}},
	}
	row := agentProjectionRow(a, false)
	if row.Exposed {
		t.Fatal("private agent must not be exposed")
	}
	if row.ServiceName != "" || row.ServicePort != 0 {
		t.Fatalf("private agent must have no Service coords, got %q %d", row.ServiceName, row.ServicePort)
	}
}

func TestAgentGatewayURL(t *testing.T) {
	exposed := &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ach-system", Name: "gh"},
		Spec: achv1alpha1.ACHAgentSpec{
			Expose:   &achv1alpha1.ExposeSpec{Service: true, Gateway: true},
			Channels: []achv1alpha1.ChannelSpec{{Name: "gh", Type: "webhook"}},
		},
	}
	// Service but no gateway opt-in → private, no published URL.
	serviceOnly := &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "peer"},
		Spec: achv1alpha1.ACHAgentSpec{
			Expose:   &achv1alpha1.ExposeSpec{Service: true},
			Channels: []achv1alpha1.ChannelSpec{{Name: "a2a-in", Type: "a2a"}},
		},
	}
	// No expose block → fully private.
	private := &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "c"},
		Spec:       achv1alpha1.ACHAgentSpec{Channels: []achv1alpha1.ChannelSpec{{Name: "n", Type: "cron"}}},
	}

	if got := agentGatewayURL(exposed, ""); got != "/agents/ach-system/achagent-gh" {
		t.Fatalf("path-only = %q, want /agents/ach-system/achagent-gh", got)
	}
	if got := agentGatewayURL(exposed, "https://ach.example.com"); got != "https://ach.example.com/agents/ach-system/achagent-gh" {
		t.Fatalf("full URL = %q, want https://ach.example.com/agents/ach-system/achagent-gh", got)
	}
	if got := agentGatewayURL(exposed, "https://ach.example.com/"); got != "https://ach.example.com/agents/ach-system/achagent-gh" {
		t.Fatalf("trailing-slash baseURL not trimmed: %q", got)
	}
	if got := agentGatewayURL(serviceOnly, "https://ach.example.com"); got != "" {
		t.Fatalf("service-only agent must have no gateway URL, got %q", got)
	}
	if got := agentGatewayURL(private, "https://ach.example.com"); got != "" {
		t.Fatalf("private agent must have no gateway URL, got %q", got)
	}
}
