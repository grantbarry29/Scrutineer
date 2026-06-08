/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package enforcement

import (
	"testing"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewSessionContext_fromEffectivePolicyAndProfile(t *testing.T) {
	enabled := true
	session := &relayv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "demo"},
		Status: relayv1alpha1.AgentSessionStatus{
			PodName: "demo-pod-xyz",
			EffectivePolicy: &relayv1alpha1.EffectivePolicyStatus{
				Mode: relayv1alpha1.PolicyModeEnforced,
				PolicyRules: relayv1alpha1.PolicyRules{
					DeniedDomains: []string{"evil.example"},
				},
			},
		},
	}
	profile := &relayv1alpha1.RuntimeProfile{
		Spec: relayv1alpha1.RuntimeProfileSpec{
			Sidecars: []relayv1alpha1.RuntimeProfileSidecar{{
				Name:    "egress",
				Type:    "dns-proxy",
				Enabled: &enabled,
			}},
		},
	}

	ctx := NewSessionContext(session, profile, "relay-session-demo")

	if ctx.SessionNamespace != "team-a" || ctx.SessionName != "demo" {
		t.Fatalf("session identity = %+v", ctx)
	}
	if ctx.JobName != "relay-session-demo" || ctx.PodName != "demo-pod-xyz" {
		t.Fatalf("runtime identity = job %q pod %q", ctx.JobName, ctx.PodName)
	}
	if ctx.Mode != relayv1alpha1.PolicyModeEnforced {
		t.Fatalf("mode = %q", ctx.Mode)
	}
	if len(ctx.Policy.DeniedDomains) != 1 || ctx.Policy.DeniedDomains[0] != "evil.example" {
		t.Fatalf("policy = %+v", ctx.Policy)
	}
	if len(ctx.Sidecars) != 1 || ctx.Sidecars[0].Type != "dns-proxy" {
		t.Fatalf("sidecars = %+v", ctx.Sidecars)
	}
}

func TestNewSessionContext_nilSession(t *testing.T) {
	ctx := NewSessionContext(nil, nil, "relay-session-x")
	if ctx.SessionName != "" || ctx.JobName != "relay-session-x" {
		t.Fatalf("got %+v", ctx)
	}
}
