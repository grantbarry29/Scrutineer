/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package policy

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

func TestApplyStatus_writesEffectivePolicy(t *testing.T) {
	session := &relayv1alpha1.AgentSession{}
	resolved := Resolved{
		Mode: relayv1alpha1.PolicyModeEnforced,
		Rules: relayv1alpha1.PolicyRules{
			DeniedDomains: []string{"evil.example"},
		},
		Matched: []relayv1alpha1.MatchedPolicyRef{{
			Kind: "AgentPolicy",
			Name: "baseline",
		}},
	}
	now := time.Unix(100, 0)
	ApplyStatusAt(session, resolved, now)

	if len(session.Status.MatchedPolicies) != 1 || session.Status.MatchedPolicies[0].Name != "baseline" {
		t.Fatalf("matched = %+v", session.Status.MatchedPolicies)
	}
	if session.Status.EffectivePolicy == nil || session.Status.EffectivePolicy.Mode != relayv1alpha1.PolicyModeEnforced {
		t.Fatalf("effective = %+v", session.Status.EffectivePolicy)
	}
	if len(session.Status.PolicyDecisions) == 0 {
		t.Fatal("expected merge decisions")
	}
	if session.Status.PolicyDecisions[0].Time != metav1.NewTime(now) {
		t.Fatalf("time = %v", session.Status.PolicyDecisions[0].Time)
	}

	ApplyStatus(session, resolved)
	if session.Status.EffectivePolicy.Mode != relayv1alpha1.PolicyModeEnforced {
		t.Fatal("ApplyStatus should delegate to ApplyStatusAt")
	}
}
