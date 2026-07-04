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

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

func TestBuildMergeDecisions_matchedPolicyAndDenyDomains(t *testing.T) {
	resolved := Resolved{
		Mode: scrutineerv1alpha1.PolicyModeDryRun,
		Rules: scrutineerv1alpha1.PolicyRules{
			DeniedDomains: []string{"evil.example"},
		},
		Matched: []scrutineerv1alpha1.MatchedPolicyRef{{
			Kind: "AgentPolicy",
			Name: "baseline",
		}},
	}
	decisions := BuildMergeDecisions(resolved, time.Unix(0, 0))

	var mode, matched, denied *scrutineerv1alpha1.PolicyDecision
	for i := range decisions {
		d := &decisions[i]
		switch {
		case d.Type == "mode":
			mode = d
		case d.Reason == "PolicyMatched":
			matched = d
		case d.Target == "evil.example":
			denied = d
		}
	}
	if mode == nil || mode.Action != scrutineerv1alpha1.PolicyDecisionDryRun {
		t.Fatalf("mode decision = %v", mode)
	}
	if matched == nil || matched.PolicyRef == nil || matched.PolicyRef.Name != "baseline" {
		t.Fatalf("matched decision = %v", matched)
	}
	if denied == nil || denied.Action != scrutineerv1alpha1.PolicyDecisionDryRun || denied.Rule != "deniedDomains" {
		t.Fatalf("denied domain decision = %v", denied)
	}
}

func TestBuildMergeDecisions_truncates(t *testing.T) {
	var targets []string
	for i := 0; i < MaxMergePolicyDecisions; i++ {
		targets = append(targets, "host-"+string(rune('a'+i%26))+".example")
	}
	resolved := Resolved{
		Mode:  scrutineerv1alpha1.PolicyModeAuditOnly,
		Rules: scrutineerv1alpha1.PolicyRules{DeniedDomains: targets},
	}
	decisions := BuildMergeDecisions(resolved, time.Unix(0, 0))
	if len(decisions) != MaxMergePolicyDecisions {
		t.Fatalf("len = %d, want %d", len(decisions), MaxMergePolicyDecisions)
	}
	last := decisions[len(decisions)-1]
	if last.Reason != "DecisionsTruncated" {
		t.Fatalf("last reason = %q", last.Reason)
	}
}

func TestBuildMergeDecisions_networkAndApprovalLists(t *testing.T) {
	resolved := Resolved{
		Mode: scrutineerv1alpha1.PolicyModeEnforced,
		Rules: scrutineerv1alpha1.PolicyRules{
			AllowedDomains:       []string{"github.com"},
			DeniedCIDRs:          []string{"10.0.0.0/8"},
			RequireHumanApproval: []string{"deploy"},
		},
	}
	decisions := BuildMergeDecisions(resolved, time.Unix(0, 0))

	var approval *scrutineerv1alpha1.PolicyDecision
	for i := range decisions {
		d := &decisions[i]
		if d.Target == "github.com" && d.Action != scrutineerv1alpha1.PolicyDecisionAllow {
			t.Fatalf("allowed domain decision = %+v", d)
		}
		if d.Target == "10.0.0.0/8" && d.Action != scrutineerv1alpha1.PolicyDecisionDeny {
			t.Fatalf("denied cidr decision = %+v", d)
		}
		if d.Target == "deploy" {
			approval = d
		}
	}
	if approval == nil || approval.Rule != "requireHumanApproval" || approval.Action != scrutineerv1alpha1.PolicyDecisionAudit {
		t.Fatalf("approval decision = %+v", approval)
	}
}

func TestNormalizeMode_emptyDefaultsAuditOnly(t *testing.T) {
	if NormalizeMode("") != scrutineerv1alpha1.PolicyModeAuditOnly {
		t.Fatalf("mode = %q", NormalizeMode(""))
	}
}

func TestBuildMergeDecisions_stampsControllerAssurance(t *testing.T) {
	resolved := Resolved{
		Mode: scrutineerv1alpha1.PolicyModeEnforced,
		Rules: scrutineerv1alpha1.PolicyRules{
			DeniedDomains: []string{"evil.example"},
		},
		Matched: []scrutineerv1alpha1.MatchedPolicyRef{{Kind: "AgentPolicy", Name: "baseline"}},
	}
	decisions := BuildMergeDecisions(resolved, time.Unix(0, 0))
	if len(decisions) == 0 {
		t.Fatal("expected merge decisions")
	}
	for i, d := range decisions {
		if d.AssuranceLevel != scrutineerv1alpha1.EvidenceControllerComputed {
			t.Fatalf("decisions[%d] assurance = %q, want controller", i, d.AssuranceLevel)
		}
	}
}
