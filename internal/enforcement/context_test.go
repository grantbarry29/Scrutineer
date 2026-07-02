/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package enforcement

import (
	"testing"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewSessionContext_fromEffectivePolicyAndProfile(t *testing.T) {
	enabled := true
	session := &scrutineerv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "demo"},
		Status: scrutineerv1alpha1.AgentSessionStatus{
			PodName: "demo-pod-xyz",
			EffectivePolicy: &scrutineerv1alpha1.EffectivePolicyStatus{
				Mode: scrutineerv1alpha1.PolicyModeEnforced,
				PolicyRules: scrutineerv1alpha1.PolicyRules{
					DeniedDomains: []string{"evil.example"},
				},
			},
		},
	}
	profile := &scrutineerv1alpha1.RuntimeProfile{
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Enforcement: []scrutineerv1alpha1.RuntimeProfileEnforcement{{
				Name:    "egress",
				Type:    "dns-proxy",
				Enabled: &enabled,
			}},
		},
	}

	ctx := NewSessionContext(session, profile, "scrutineer-session-demo")

	if ctx.SessionNamespace != "team-a" || ctx.SessionName != "demo" {
		t.Fatalf("session identity = %+v", ctx)
	}
	if ctx.JobName != "scrutineer-session-demo" || ctx.PodName != "demo-pod-xyz" {
		t.Fatalf("runtime identity = job %q pod %q", ctx.JobName, ctx.PodName)
	}
	if ctx.Mode != scrutineerv1alpha1.PolicyModeEnforced {
		t.Fatalf("mode = %q", ctx.Mode)
	}
	if len(ctx.Policy.DeniedDomains) != 1 || ctx.Policy.DeniedDomains[0] != "evil.example" {
		t.Fatalf("policy = %+v", ctx.Policy)
	}
	if len(ctx.Enforcement) != 1 || ctx.Enforcement[0].Type != "dns-proxy" {
		t.Fatalf("sidecars = %+v", ctx.Enforcement)
	}
}

func TestNewSessionContext_nilSession(t *testing.T) {
	ctx := NewSessionContext(nil, nil, "scrutineer-session-x")
	if ctx.SessionName != "" || ctx.JobName != "scrutineer-session-x" {
		t.Fatalf("got %+v", ctx)
	}
}
