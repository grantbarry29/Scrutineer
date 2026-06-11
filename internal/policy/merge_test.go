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

func TestMergeRules_minMaxCallsPerMinute(t *testing.T) {
	a := int32(60)
	b := int32(10)
	got := MergeRules(
		relayv1alpha1.PolicyRules{MaxCallsPerMinute: &a},
		relayv1alpha1.PolicyRules{MaxCallsPerMinute: &b},
	)
	if got.MaxCallsPerMinute == nil || *got.MaxCallsPerMinute != 10 {
		t.Fatalf("MaxCallsPerMinute = %v, want 10", got.MaxCallsPerMinute)
	}
}

func TestMergeRules_unionsPaths(t *testing.T) {
	got := MergeRules(
		relayv1alpha1.PolicyRules{AllowedPaths: []string{"/workspace/**"}},
		relayv1alpha1.PolicyRules{DeniedPaths: []string{"/etc/**"}},
	)
	if len(got.AllowedPaths) != 1 || got.AllowedPaths[0] != "/workspace/**" {
		t.Fatalf("AllowedPaths = %v", got.AllowedPaths)
	}
	if len(got.DeniedPaths) != 1 || got.DeniedPaths[0] != "/etc/**" {
		t.Fatalf("DeniedPaths = %v", got.DeniedPaths)
	}
}

func TestMergeRules_minWorkspaceBytes(t *testing.T) {
	a := int64(1_000_000_000)
	b := int64(500_000_000)
	got := MergeRules(
		relayv1alpha1.PolicyRules{MaxWorkspaceBytes: &a},
		relayv1alpha1.PolicyRules{MaxWorkspaceBytes: &b},
	)
	if got.MaxWorkspaceBytes == nil || *got.MaxWorkspaceBytes != b {
		t.Fatalf("MaxWorkspaceBytes = %v, want 500000000", got.MaxWorkspaceBytes)
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
