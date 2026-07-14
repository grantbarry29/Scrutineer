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
	"github.com/grantbarry29/scrutineer/internal/policy"
)

func TestApplyPolicyStatus_preservesRuntimeDecisions(t *testing.T) {
	ts := metav1.NewTime(time.Unix(100, 0))
	runtimeTS := metav1.NewTime(time.Unix(200, 0))
	prior := []scrutineerv1alpha1.PolicyDecision{{
		Time:   runtimeTS,
		Phase:  scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
		Type:   "network",
		Action: scrutineerv1alpha1.PolicyDecisionDeny,
		Reason: "DeniedDomains",
		Target: "evil.example",
	}}

	session := &scrutineerv1alpha1.AgentSession{}
	resolved := policy.Resolved{Mode: scrutineerv1alpha1.PolicyModeEnforced}
	ApplyPolicyStatusAt(session, resolved, prior, ts.Time)

	if len(session.Status.PolicyDecisions) < 2 {
		t.Fatalf("decisions = %d, want merge + runtime", len(session.Status.PolicyDecisions))
	}
	if session.Status.PolicyDecisions[0].Phase != scrutineerv1alpha1.PolicyDecisionPhaseMerge {
		t.Fatalf("first phase = %q", session.Status.PolicyDecisions[0].Phase)
	}
	last := session.Status.PolicyDecisions[len(session.Status.PolicyDecisions)-1]
	if last.Phase != scrutineerv1alpha1.PolicyDecisionPhaseRuntime || last.Target != "evil.example" {
		t.Fatalf("runtime decision = %+v", last)
	}
}

func TestAppendRuntimePolicyDecisions_setsPhase(t *testing.T) {
	ts := metav1.NewTime(time.Unix(0, 0))
	session := &scrutineerv1alpha1.AgentSession{
		Status: scrutineerv1alpha1.AgentSessionStatus{
			PolicyDecisions: []scrutineerv1alpha1.PolicyDecision{{
				Time:   ts,
				Phase:  scrutineerv1alpha1.PolicyDecisionPhaseMerge,
				Type:   "mode",
				Action: scrutineerv1alpha1.PolicyDecisionAudit,
				Reason: "StrictestMode",
			}},
		},
	}
	AppendRuntimePolicyDecisions(session, []scrutineerv1alpha1.PolicyDecision{{
		Time:   ts,
		Type:   "tool",
		Action: scrutineerv1alpha1.PolicyDecisionDeny,
		Reason: "DeniedTools",
		Target: "kubectl",
	}})

	if len(session.Status.PolicyDecisions) != 2 {
		t.Fatalf("len = %d", len(session.Status.PolicyDecisions))
	}
	if session.Status.PolicyDecisions[1].Phase != scrutineerv1alpha1.PolicyDecisionPhaseRuntime {
		t.Fatalf("phase = %q", session.Status.PolicyDecisions[1].Phase)
	}
}

func TestAppendRuntimePolicyDecisions_truncatesAtCap(t *testing.T) {
	ts := metav1.NewTime(time.Unix(0, 0))
	merge := make([]scrutineerv1alpha1.PolicyDecision, enforcement.MaxPolicyDecisions)
	for i := range merge {
		merge[i] = scrutineerv1alpha1.PolicyDecision{
			Time:   ts,
			Phase:  scrutineerv1alpha1.PolicyDecisionPhaseMerge,
			Type:   "tool",
			Action: scrutineerv1alpha1.PolicyDecisionAudit,
			Reason: "DeniedTools",
		}
	}
	session := &scrutineerv1alpha1.AgentSession{Status: scrutineerv1alpha1.AgentSessionStatus{PolicyDecisions: merge}}
	AppendRuntimePolicyDecisions(session, []scrutineerv1alpha1.PolicyDecision{{
		Time:   ts,
		Type:   "network",
		Action: scrutineerv1alpha1.PolicyDecisionDeny,
		Reason: "DeniedDomains",
	}})

	if len(session.Status.PolicyDecisions) != enforcement.MaxPolicyDecisions {
		t.Fatalf("len = %d, want cap %d", len(session.Status.PolicyDecisions), enforcement.MaxPolicyDecisions)
	}
	last := session.Status.PolicyDecisions[len(session.Status.PolicyDecisions)-1]
	if last.Reason != "DecisionsTruncated" {
		t.Fatalf("last reason = %q", last.Reason)
	}
}

func TestMergeRuntimePolicyDecisionsInPlace_doesNotDuplicate(t *testing.T) {
	ts := metav1.NewTime(time.Unix(0, 0))
	runtime := scrutineerv1alpha1.PolicyDecision{
		Time:   ts,
		Phase:  scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
		Type:   "network",
		Action: scrutineerv1alpha1.PolicyDecisionDeny,
		Reason: "DeniedDomains",
		Target: "evil.example",
	}
	dst := []scrutineerv1alpha1.PolicyDecision{runtime}
	mergeRuntimePolicyDecisionsInPlace(&dst, []scrutineerv1alpha1.PolicyDecision{runtime})
	if len(dst) != 1 {
		t.Fatalf("len = %d, want no duplicate", len(dst))
	}
}

func TestApplyPolicyStatus_mergeTimestampsStableAcrossReconciles(t *testing.T) {
	resolved := policy.Resolved{
		Mode:  scrutineerv1alpha1.PolicyModeEnforced,
		Rules: scrutineerv1alpha1.PolicyRules{AllowedDomains: []string{"example.com"}},
	}
	session := &scrutineerv1alpha1.AgentSession{}
	first := time.Unix(100, 0)
	ApplyPolicyStatusAt(session, resolved, nil, first)

	// Second reconcile: same resolved policy, later clock. prior is the persisted
	// status, now also carrying a runtime decision that must survive untouched.
	runtimeTS := metav1.NewTime(time.Unix(150, 0))
	prior := append([]scrutineerv1alpha1.PolicyDecision(nil), session.Status.PolicyDecisions...)
	prior = append(prior, scrutineerv1alpha1.PolicyDecision{
		Time:   runtimeTS,
		Phase:  scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
		Type:   "network",
		Action: scrutineerv1alpha1.PolicyDecisionDeny,
		Reason: "DeniedDomains",
		Target: "evil.example",
	})
	session.Status.PolicyDecisions = prior
	ApplyPolicyStatusAt(session, resolved, prior, time.Unix(200, 0))

	want := metav1.NewTime(first)
	var runtimeSeen bool
	for i, d := range session.Status.PolicyDecisions {
		switch d.Phase {
		case scrutineerv1alpha1.PolicyDecisionPhaseMerge:
			if d.Time != want {
				t.Fatalf("merge decision %d (%s %q) re-stamped: time = %v, want %v", i, d.Reason, d.Target, d.Time, want)
			}
		case scrutineerv1alpha1.PolicyDecisionPhaseRuntime:
			runtimeSeen = true
			if d.Time != runtimeTS {
				t.Fatalf("runtime decision time = %v, want %v", d.Time, runtimeTS)
			}
		}
	}
	if !runtimeSeen {
		t.Fatal("runtime decision dropped")
	}
}
