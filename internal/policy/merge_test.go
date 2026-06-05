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

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

func TestMergeRules_unionsLists(t *testing.T) {
	base := relayv1alpha1.PolicyRules{
		AllowedDomains: []string{"a.com"},
		DeniedTools:    []string{"kubectl"},
	}
	overlay := relayv1alpha1.PolicyRules{
		AllowedDomains: []string{"b.com", "a.com"},
		DeniedTools:    []string{"deploy"},
	}
	got := MergeRules(base, overlay)
	if len(got.AllowedDomains) != 2 || got.AllowedDomains[0] != "a.com" || got.AllowedDomains[1] != "b.com" {
		t.Fatalf("AllowedDomains = %v, want [a.com b.com]", got.AllowedDomains)
	}
	if len(got.DeniedTools) != 2 {
		t.Fatalf("DeniedTools = %v", got.DeniedTools)
	}
}

func TestMergeRules_minCaps(t *testing.T) {
	a := int32(100)
	b := int32(25)
	got := MergeRules(
		relayv1alpha1.PolicyRules{MaxToolCalls: &a},
		relayv1alpha1.PolicyRules{MaxToolCalls: &b},
	)
	if got.MaxToolCalls == nil || *got.MaxToolCalls != 25 {
		t.Fatalf("MaxToolCalls = %v, want 25", got.MaxToolCalls)
	}
}

func TestStrictestMode(t *testing.T) {
	got := StrictestMode(
		relayv1alpha1.PolicyModeAuditOnly,
		relayv1alpha1.PolicyModeDryRun,
		relayv1alpha1.PolicyModeEnforced,
	)
	if got != relayv1alpha1.PolicyModeEnforced {
		t.Fatalf("mode = %q, want enforced", got)
	}
}

func TestResolve_inlineOverrides(t *testing.T) {
	layers := []Layer{{
		Rules: relayv1alpha1.PolicyRules{DeniedTools: []string{"kubectl"}},
		Mode:  relayv1alpha1.PolicyModeAuditOnly,
		Match: &relayv1alpha1.MatchedPolicyRef{Kind: "AgentPolicy", Name: "baseline"},
	}}
	inline := relayv1alpha1.PolicyRules{DeniedTools: []string{"deploy"}}
	resolved := Resolve(layers, inline)
	if len(resolved.Rules.DeniedTools) != 2 {
		t.Fatalf("DeniedTools = %v", resolved.Rules.DeniedTools)
	}
	if resolved.Mode != relayv1alpha1.PolicyModeAuditOnly {
		t.Fatalf("mode = %q", resolved.Mode)
	}
	if len(resolved.Matched) != 1 || resolved.Matched[0].Name != "baseline" {
		t.Fatalf("matched = %v", resolved.Matched)
	}
}
