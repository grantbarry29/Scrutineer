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

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

func TestBuildMergeDecisions_matchedPolicyAndDenyTools(t *testing.T) {
	resolved := Resolved{
		Mode: relayv1alpha1.PolicyModeDryRun,
		Rules: relayv1alpha1.PolicyRules{
			DeniedTools: []string{"kubectl-prod"},
		},
		Matched: []relayv1alpha1.MatchedPolicyRef{{
			Kind: "AgentPolicy",
			Name: "baseline",
		}},
	}
	decisions := BuildMergeDecisions(resolved, time.Unix(0, 0))

	var mode, matched, denied *relayv1alpha1.PolicyDecision
	for i := range decisions {
		d := &decisions[i]
		switch {
		case d.Type == "mode":
			mode = d
		case d.Reason == "PolicyMatched":
			matched = d
		case d.Target == "kubectl-prod":
			denied = d
		}
	}
	if mode == nil || mode.Action != relayv1alpha1.PolicyDecisionDryRun {
		t.Fatalf("mode decision = %v", mode)
	}
	if matched == nil || matched.PolicyRef == nil || matched.PolicyRef.Name != "baseline" {
		t.Fatalf("matched decision = %v", matched)
	}
	if denied == nil || denied.Action != relayv1alpha1.PolicyDecisionDryRun || denied.Rule != "deniedTools" {
		t.Fatalf("denied tool decision = %v", denied)
	}
}

func TestBuildMergeDecisions_truncates(t *testing.T) {
	var targets []string
	for i := 0; i < MaxMergePolicyDecisions; i++ {
		targets = append(targets, "tool-"+string(rune('a'+i%26)))
	}
	resolved := Resolved{
		Mode:  relayv1alpha1.PolicyModeAuditOnly,
		Rules: relayv1alpha1.PolicyRules{DeniedTools: targets},
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

func TestBuildMergeDecisions_capsAndNetworkLists(t *testing.T) {
	maxNet := int32(100)
	maxTool := int32(50)
	maxPerMin := int32(10)
	resolved := Resolved{
		Mode: relayv1alpha1.PolicyModeEnforced,
		Rules: relayv1alpha1.PolicyRules{
			AllowedDomains:       []string{"github.com"},
			DeniedCIDRs:          []string{"10.0.0.0/8"},
			RequireHumanApproval: []string{"deploy"},
			MaxNetworkRequests:   &maxNet,
			MaxToolCalls:         &maxTool,
			MaxCallsPerMinute:    &maxPerMin,
		},
	}
	decisions := BuildMergeDecisions(resolved, time.Unix(0, 0))

	var capCount int
	for _, d := range decisions {
		if d.Type == "cap" {
			capCount++
		}
		if d.Target == "github.com" && d.Action != relayv1alpha1.PolicyDecisionAllow {
			t.Fatalf("allowed domain decision = %+v", d)
		}
		if d.Target == "10.0.0.0/8" && d.Action != relayv1alpha1.PolicyDecisionDeny {
			t.Fatalf("denied cidr decision = %+v", d)
		}
	}
	if capCount != 3 {
		t.Fatalf("cap decisions = %d", capCount)
	}
}

func TestBuildMergeDecisions_argumentRulesSummary(t *testing.T) {
	resolved := Resolved{
		Mode: relayv1alpha1.PolicyModeEnforced,
		Rules: relayv1alpha1.PolicyRules{
			ArgumentRules: []relayv1alpha1.ToolArgumentRule{
				{Tools: []string{"read_file"}, Constraints: []relayv1alpha1.ArgumentConstraint{{Arg: "path", Op: relayv1alpha1.ArgOpHasPrefix, Values: []string{"/workspace/"}, Effect: relayv1alpha1.ConstraintEffectAllow}}},
				{Tools: []string{"kubectl"}, Constraints: []relayv1alpha1.ArgumentConstraint{{Arg: "args[0]", Op: relayv1alpha1.ArgOpIn, Values: []string{"delete"}}}},
			},
		},
	}
	decisions := BuildMergeDecisions(resolved, time.Unix(0, 0))

	var summary *relayv1alpha1.PolicyDecision
	for i := range decisions {
		if decisions[i].Reason == "ArgumentRulesDeclared" {
			summary = &decisions[i]
		}
	}
	if summary == nil {
		t.Fatal("expected ArgumentRulesDeclared decision")
	}
	if summary.Rule != "argumentRules" || summary.Target != "2" || summary.Type != "tool" {
		t.Fatalf("argument-rules decision = %+v", summary)
	}
	if summary.AssuranceLevel != relayv1alpha1.EvidenceControllerComputed {
		t.Fatalf("assurance = %q, want controller", summary.AssuranceLevel)
	}
}

func TestNormalizeMode_emptyDefaultsAuditOnly(t *testing.T) {
	if NormalizeMode("") != relayv1alpha1.PolicyModeAuditOnly {
		t.Fatalf("mode = %q", NormalizeMode(""))
	}
}

func TestBuildMergeDecisions_stampsControllerAssurance(t *testing.T) {
	maxTool := int32(5)
	resolved := Resolved{
		Mode: relayv1alpha1.PolicyModeEnforced,
		Rules: relayv1alpha1.PolicyRules{
			DeniedTools:  []string{"kubectl-prod"},
			MaxToolCalls: &maxTool,
		},
		Matched: []relayv1alpha1.MatchedPolicyRef{{Kind: "AgentPolicy", Name: "baseline"}},
	}
	decisions := BuildMergeDecisions(resolved, time.Unix(0, 0))
	if len(decisions) == 0 {
		t.Fatal("expected merge decisions")
	}
	for i, d := range decisions {
		if d.AssuranceLevel != relayv1alpha1.EvidenceControllerComputed {
			t.Fatalf("decisions[%d] assurance = %q, want controller", i, d.AssuranceLevel)
		}
	}
}
