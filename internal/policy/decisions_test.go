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
