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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

func TestApplyRuntimePolicyReport_enforcedViolationFromDecision(t *testing.T) {
	ts := metav1.NewTime(time.Unix(0, 0))
	session := &scrutineerv1alpha1.AgentSession{}
	ApplyRuntimePolicyReport(session, enforcement.RuntimeReport{
		Decisions: []scrutineerv1alpha1.PolicyDecision{{
			Time:    ts,
			Phase:   scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
			Type:    "network",
			Action:  scrutineerv1alpha1.PolicyDecisionDeny,
			Reason:  "DeniedCIDRs",
			Target:  "10.0.0.0/8",
			Message: "egress blocked by NetworkPolicy",
		}},
	})

	if len(session.Status.PolicyDecisions) != 1 {
		t.Fatalf("decisions = %d", len(session.Status.PolicyDecisions))
	}
	if len(session.Status.Violations) != 1 {
		t.Fatalf("violations = %d", len(session.Status.Violations))
	}
	if session.Status.Violations[0].Target != "10.0.0.0/8" {
		t.Fatalf("violation = %+v", session.Status.Violations[0])
	}
}

func TestApplyRuntimePolicyReport_dryRunViolation(t *testing.T) {
	ts := metav1.NewTime(time.Unix(0, 0))
	session := &scrutineerv1alpha1.AgentSession{}
	ApplyRuntimePolicyReport(session, enforcement.RuntimeReport{
		Decisions: []scrutineerv1alpha1.PolicyDecision{{
			Time:   ts,
			Phase:  scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
			Type:   "tool",
			Action: scrutineerv1alpha1.PolicyDecisionDryRun,
			Target: "kubectl",
		}},
	})
	if len(session.Status.Violations) != 1 {
		t.Fatalf("violations = %d", len(session.Status.Violations))
	}
}

func TestApplyRuntimePolicyReport_auditNoViolation(t *testing.T) {
	ts := metav1.NewTime(time.Unix(0, 0))
	session := &scrutineerv1alpha1.AgentSession{}
	ApplyRuntimePolicyReport(session, enforcement.RuntimeReport{
		Decisions: []scrutineerv1alpha1.PolicyDecision{{
			Time:   ts,
			Type:   "network",
			Action: scrutineerv1alpha1.PolicyDecisionAudit,
			Target: "evil.example",
		}},
	})
	if len(session.Status.Violations) != 0 {
		t.Fatalf("violations = %+v", session.Status.Violations)
	}
}

func TestApplyRuntimePolicyReport_explicitViolationsDeduped(t *testing.T) {
	ts := metav1.NewTime(time.Unix(0, 0))
	v := scrutineerv1alpha1.PolicyViolation{
		Time: ts, Type: "network", Target: "10.0.0.1", Message: "blocked",
	}
	session := &scrutineerv1alpha1.AgentSession{}
	ApplyRuntimePolicyReport(session, enforcement.RuntimeReport{
		Decisions: []scrutineerv1alpha1.PolicyDecision{{
			Time: ts, Type: "network", Action: scrutineerv1alpha1.PolicyDecisionDeny,
			Target: "10.0.0.1", Message: "blocked",
		}},
		Violations: []scrutineerv1alpha1.PolicyViolation{v},
	})
	if len(session.Status.Violations) != 1 {
		t.Fatalf("violations = %d, want deduped single entry", len(session.Status.Violations))
	}
}

// A runtime report carrying a tool decision + violation must merge into session status.
// (The tool decision is constructed directly: the cooperative in-pod tier was removed in
// the untamperable pivot (#71); a future out-of-pod tools chokepoint will re-emit these.)
func TestApplyRuntimePolicyReport_toolDecision(t *testing.T) {
	ts := metav1.NewTime(time.Unix(0, 0))
	// The enforced deny decision auto-derives its violation; no explicit violation needed.
	report := enforcement.RuntimeReport{
		Decisions: []scrutineerv1alpha1.PolicyDecision{{
			Time:   ts,
			Type:   "tool",
			Action: scrutineerv1alpha1.PolicyDecisionDeny,
			Reason: "DeniedTools",
			Target: "kubectl",
		}},
	}

	session := &scrutineerv1alpha1.AgentSession{}
	ApplyRuntimePolicyReport(session, report)

	if len(session.Status.PolicyDecisions) != 1 || len(session.Status.Violations) != 1 {
		t.Fatalf("decisions=%d violations=%d", len(session.Status.PolicyDecisions), len(session.Status.Violations))
	}
	if session.Status.PolicyDecisions[0].Type != "tool" {
		t.Fatalf("type=%q", session.Status.PolicyDecisions[0].Type)
	}
}

func TestMergeViolationsInPlace_doesNotDuplicate(t *testing.T) {
	ts := metav1.NewTime(time.Unix(0, 0))
	v := scrutineerv1alpha1.PolicyViolation{Time: ts, Type: "network", Target: "x", Message: "blocked"}
	dst := []scrutineerv1alpha1.PolicyViolation{v}
	mergeViolationsInPlace(&dst, []scrutineerv1alpha1.PolicyViolation{v})
	if len(dst) != 1 {
		t.Fatalf("len = %d", len(dst))
	}
}
