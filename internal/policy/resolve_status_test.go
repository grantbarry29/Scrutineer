/*
Copyright 2026 The Scrutineer Authors.

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

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

func TestApplyStatus_writesEffectivePolicy(t *testing.T) {
	session := &scrutineerv1alpha1.AgentSession{}
	resolved := Resolved{
		Mode: scrutineerv1alpha1.PolicyModeEnforced,
		Rules: scrutineerv1alpha1.PolicyRules{
			DeniedDomains: []string{"evil.example"},
		},
		Matched: []scrutineerv1alpha1.MatchedPolicyRef{{
			Kind: "AgentPolicy",
			Name: "baseline",
		}},
	}
	now := time.Unix(100, 0)
	ApplyStatusAt(session, resolved, now)

	if len(session.Status.MatchedPolicies) != 1 || session.Status.MatchedPolicies[0].Name != "baseline" {
		t.Fatalf("matched = %+v", session.Status.MatchedPolicies)
	}
	if session.Status.EffectivePolicy == nil || session.Status.EffectivePolicy.Mode != scrutineerv1alpha1.PolicyModeEnforced {
		t.Fatalf("effective = %+v", session.Status.EffectivePolicy)
	}
	if len(session.Status.PolicyDecisions) == 0 {
		t.Fatal("expected merge decisions")
	}
	if session.Status.PolicyDecisions[0].Time != metav1.NewTime(now) {
		t.Fatalf("time = %v", session.Status.PolicyDecisions[0].Time)
	}

	ApplyStatus(session, resolved)
	if session.Status.EffectivePolicy.Mode != scrutineerv1alpha1.PolicyModeEnforced {
		t.Fatal("ApplyStatus should delegate to ApplyStatusAt")
	}
}

func TestApplyStatusAt_keepsFirstDecisionTimesWhenPolicyUnchanged(t *testing.T) {
	session := &scrutineerv1alpha1.AgentSession{}
	resolved := Resolved{
		Mode: scrutineerv1alpha1.PolicyModeEnforced,
		Rules: scrutineerv1alpha1.PolicyRules{
			AllowedDomains: []string{"example.com"},
			DeniedDomains:  []string{"evil.example"},
		},
		Matched: []scrutineerv1alpha1.MatchedPolicyRef{{
			Kind: "AgentPolicy",
			Name: "baseline",
		}},
	}
	first := time.Unix(100, 0)
	ApplyStatusAt(session, resolved, first)

	ApplyStatusAt(session, resolved, time.Unix(200, 0))

	want := metav1.NewTime(first)
	if len(session.Status.PolicyDecisions) == 0 {
		t.Fatal("expected merge decisions")
	}
	for i, d := range session.Status.PolicyDecisions {
		if d.Time != want {
			t.Fatalf("decision %d (%s %q) re-stamped: time = %v, want %v", i, d.Reason, d.Target, d.Time, want)
		}
	}
}

func TestApplyStatusAt_restampsOnlyChangedDecisions(t *testing.T) {
	session := &scrutineerv1alpha1.AgentSession{}
	resolved := Resolved{
		Mode:  scrutineerv1alpha1.PolicyModeEnforced,
		Rules: scrutineerv1alpha1.PolicyRules{AllowedDomains: []string{"example.com"}},
	}
	first := time.Unix(100, 0)
	ApplyStatusAt(session, resolved, first)

	changed := resolved
	changed.Rules.DeniedDomains = []string{"evil.example"}
	later := time.Unix(200, 0)
	ApplyStatusAt(session, changed, later)

	var sawKept, sawNew bool
	for _, d := range session.Status.PolicyDecisions {
		switch d.Target {
		case "example.com":
			sawKept = true
			if d.Time != metav1.NewTime(first) {
				t.Fatalf("unchanged decision re-stamped: time = %v, want %v", d.Time, metav1.NewTime(first))
			}
		case "evil.example":
			sawNew = true
			if d.Time != metav1.NewTime(later) {
				t.Fatalf("new decision time = %v, want %v", d.Time, metav1.NewTime(later))
			}
		}
	}
	if !sawKept || !sawNew {
		t.Fatalf("kept=%v new=%v, want both targets present", sawKept, sawNew)
	}
}
