/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	scrutineerjob "github.com/grantbarry29/scrutineer/internal/controller/job"
	"github.com/grantbarry29/scrutineer/internal/enforcement/lockverify"
)

type stubVerdict struct{ v lockverify.Verdict }

func (s stubVerdict) Current() lockverify.State { return lockverify.State{Verdict: s.v} }

func gateSession(mode scrutineerv1alpha1.PolicyMode) *scrutineerv1alpha1.AgentSession {
	return &scrutineerv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Name: "s1", Namespace: "team-a"},
		Status: scrutineerv1alpha1.AgentSessionStatus{
			EffectivePolicy: &scrutineerv1alpha1.EffectivePolicyStatus{Mode: mode},
		},
	}
}

func gateEnvoyProfile() *scrutineerv1alpha1.RuntimeProfile {
	return &scrutineerv1alpha1.RuntimeProfile{
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Enforcement: []scrutineerv1alpha1.RuntimeProfileEnforcement{{
				Type: scrutineerjob.EnforcementTypeEnvoy,
			}},
		},
	}
}

func gateReconciler(v LockVerdictSource) *AgentSessionReconciler {
	return &AgentSessionReconciler{
		LockVerifier: v,
		Recorder:     record.NewFakeRecorder(16),
	}
}

func lockCond(s *scrutineerv1alpha1.AgentSession) *metav1.Condition {
	return meta.FindStatusCondition(s.Status.Conditions, ConditionEgressLockVerified)
}

func TestEgressLockGate_holdsEnforcedOnRefused(t *testing.T) {
	r := gateReconciler(stubVerdict{lockverify.VerdictRefused})
	s := gateSession(scrutineerv1alpha1.PolicyModeEnforced)

	if hold := r.egressLockGate(s, gateEnvoyProfile()); !hold {
		t.Fatal("enforced session on a refused substrate must be held")
	}
	c := lockCond(s)
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != ReasonCNINotEnforcing {
		t.Fatalf("condition = %+v, want False/%s", c, ReasonCNINotEnforcing)
	}
}

func TestEgressLockGate_holdsEnforcedOnUnknown(t *testing.T) {
	// Fail closed: no conclusive probe yet is not a pass.
	r := gateReconciler(stubVerdict{lockverify.VerdictUnknown})
	s := gateSession(scrutineerv1alpha1.PolicyModeEnforced)

	if hold := r.egressLockGate(s, gateEnvoyProfile()); !hold {
		t.Fatal("enforced session with no conclusive probe must be held (fail closed)")
	}
	if c := lockCond(s); c == nil || c.Reason != ReasonLockProbeInconclusive {
		t.Fatalf("condition = %+v, want reason %s", c, ReasonLockProbeInconclusive)
	}
}

func TestEgressLockGate_runsEnforcedOnVerified(t *testing.T) {
	r := gateReconciler(stubVerdict{lockverify.VerdictVerified})
	s := gateSession(scrutineerv1alpha1.PolicyModeEnforced)

	if hold := r.egressLockGate(s, gateEnvoyProfile()); hold {
		t.Fatal("verified substrate must not hold the session")
	}
	if c := lockCond(s); c == nil || c.Status != metav1.ConditionTrue || c.Reason != ReasonLockVerified {
		t.Fatalf("condition = %+v, want True/%s", c, ReasonLockVerified)
	}
}

func TestEgressLockGate_auditGetsConditionButRuns(t *testing.T) {
	r := gateReconciler(stubVerdict{lockverify.VerdictRefused})
	s := gateSession(scrutineerv1alpha1.PolicyModeAuditOnly)

	if hold := r.egressLockGate(s, gateEnvoyProfile()); hold {
		t.Fatal("audit-mode session must not be held")
	}
	// But its observation-strength caveat must be visible.
	if c := lockCond(s); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("condition = %+v, want False (honest observation-strength label)", c)
	}
}

func TestEgressLockGate_noSubstrateNoCondition(t *testing.T) {
	// No envoy enforcement, no CIDR rules ⇒ no NetworkPolicy substrate ⇒ out of scope.
	r := gateReconciler(stubVerdict{lockverify.VerdictRefused})
	s := gateSession(scrutineerv1alpha1.PolicyModeEnforced)

	if hold := r.egressLockGate(s, nil); hold {
		t.Fatal("session without a NetworkPolicy substrate must not be held")
	}
	if c := lockCond(s); c != nil {
		t.Fatalf("condition must not be set for out-of-scope sessions, got %+v", c)
	}
}

func TestEgressLockGate_nilVerifierInert(t *testing.T) {
	r := gateReconciler(nil)
	s := gateSession(scrutineerv1alpha1.PolicyModeEnforced)

	if hold := r.egressLockGate(s, gateEnvoyProfile()); hold {
		t.Fatal("gate must be inert when no verifier is wired")
	}
	if c := lockCond(s); c != nil {
		t.Fatalf("no condition expected without a verifier, got %+v", c)
	}
}
