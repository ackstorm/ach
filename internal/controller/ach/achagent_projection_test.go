// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

func TestAgentProjectionRow_WebhookAndCoords(t *testing.T) {
	a := &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ach-system", Name: "gh", ResourceVersion: "42"},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef: achv1alpha1.LocalObjectRef{Name: "prof"},
			Channels: []achv1alpha1.ChannelSpec{
				{Name: "github-review", Type: "webhook", Source: "github"},
				{Name: "nightly", Type: "cron"},
			},
		},
	}
	row := agentProjectionRow(a, true)
	if !row.HasWebhook {
		t.Fatal("HasWebhook should be true")
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

func TestAgentProjectionRow_CronOnly_NoServiceCoords(t *testing.T) {
	a := &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "c"},
		Spec:       achv1alpha1.ACHAgentSpec{Channels: []achv1alpha1.ChannelSpec{{Name: "n", Type: "cron"}}},
	}
	row := agentProjectionRow(a, false)
	if row.HasWebhook {
		t.Fatal("cron-only agent must not be a webhook")
	}
	if row.ServiceName != "" || row.ServicePort != 0 {
		t.Fatalf("cron-only agent must have no Service coords, got %q %d", row.ServiceName, row.ServicePort)
	}
}

func TestAgentWebhookURL(t *testing.T) {
	withWebhook := &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ach-system", Name: "gh"},
		Spec:       achv1alpha1.ACHAgentSpec{Channels: []achv1alpha1.ChannelSpec{{Name: "gh", Type: "webhook"}}},
	}
	cronOnly := &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "c"},
		Spec:       achv1alpha1.ACHAgentSpec{Channels: []achv1alpha1.ChannelSpec{{Name: "n", Type: "cron"}}},
	}

	if got := agentWebhookURL(withWebhook, ""); got != "/hook/ach-system/gh" {
		t.Fatalf("path-only = %q, want /hook/ach-system/gh", got)
	}
	if got := agentWebhookURL(withWebhook, "https://ach.example.com"); got != "https://ach.example.com/hook/ach-system/gh" {
		t.Fatalf("full URL = %q, want https://ach.example.com/hook/ach-system/gh", got)
	}
	if got := agentWebhookURL(withWebhook, "https://ach.example.com/"); got != "https://ach.example.com/hook/ach-system/gh" {
		t.Fatalf("trailing-slash baseURL not trimmed: %q", got)
	}
	if got := agentWebhookURL(cronOnly, "https://ach.example.com"); got != "" {
		t.Fatalf("cron-only agent must have no webhook URL, got %q", got)
	}
}
