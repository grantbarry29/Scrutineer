/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/enforcement/dnsproxy"
)

func TestApplyEgressProxyRuntimeEvent_populatesViolations(t *testing.T) {
	ts := time.Unix(0, 0)
	session := &relayv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "demo"},
		Status: relayv1alpha1.AgentSessionStatus{
			EffectivePolicy: &relayv1alpha1.EffectivePolicyStatus{
				Mode: relayv1alpha1.PolicyModeEnforced,
				PolicyRules: relayv1alpha1.PolicyRules{
					DeniedDomains: []string{"evil.example"},
				},
			},
		},
	}
	ApplyEgressProxyRuntimeEvent(session, nil, dnsproxy.RuntimeEvent{Host: "evil.example"}, ts)

	if len(session.Status.PolicyDecisions) != 1 {
		t.Fatalf("decisions=%d", len(session.Status.PolicyDecisions))
	}
	if len(session.Status.Violations) != 1 {
		t.Fatalf("violations=%d", len(session.Status.Violations))
	}
	if session.Status.Violations[0].Target != "evil.example" {
		t.Fatalf("violation=%+v", session.Status.Violations[0])
	}
}
