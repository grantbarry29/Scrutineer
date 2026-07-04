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

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

func TestMergeRules_unionsLists(t *testing.T) {
	base := scrutineerv1alpha1.PolicyRules{
		AllowedDomains:       []string{"a.com"},
		RequireHumanApproval: []string{"deploy"},
	}
	overlay := scrutineerv1alpha1.PolicyRules{
		AllowedDomains:       []string{"b.com", "a.com"},
		RequireHumanApproval: []string{"wire-transfer"},
	}
	got := MergeRules(base, overlay)
	if len(got.AllowedDomains) != 2 || got.AllowedDomains[0] != "a.com" || got.AllowedDomains[1] != "b.com" {
		t.Fatalf("AllowedDomains = %v, want [a.com b.com]", got.AllowedDomains)
	}
	if len(got.RequireHumanApproval) != 2 {
		t.Fatalf("RequireHumanApproval = %v", got.RequireHumanApproval)
	}
}

// Regression for #40: unionStrings must never mutate its inputs. `a` is given
// spare capacity so the old `range append(a, b...)` would have written b's first
// element into a's backing array at index len(a).
func TestUnionStrings_doesNotMutateInputs(t *testing.T) {
	backing := make([]string, 1, 4)
	backing[0] = "a1"
	a := backing[:1]
	b := []string{"b1", "b2"}

	got := unionStrings(a, b)

	if len(a) != 1 || a[0] != "a1" {
		t.Fatalf("a mutated: %v", a)
	}
	// The shared backing array beyond len(a) must remain zero (old code wrote "b1").
	if full := backing[:cap(backing)]; full[1] != "" {
		t.Fatalf("append wrote into a's backing array: %v", full)
	}
	if len(b) != 2 || b[0] != "b1" || b[1] != "b2" {
		t.Fatalf("b mutated: %v", b)
	}

	want := []string{"a1", "b1", "b2"}
	if len(got) != len(want) {
		t.Fatalf("union = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("union = %v, want %v", got, want)
		}
	}
}

func TestStrictestMode(t *testing.T) {
	got := StrictestMode(
		scrutineerv1alpha1.PolicyModeAuditOnly,
		scrutineerv1alpha1.PolicyModeDryRun,
		scrutineerv1alpha1.PolicyModeEnforced,
	)
	if got != scrutineerv1alpha1.PolicyModeEnforced {
		t.Fatalf("mode = %q, want enforced", got)
	}
}

func TestResolve_inlineOverrides(t *testing.T) {
	layers := []Layer{{
		Rules: scrutineerv1alpha1.PolicyRules{DeniedDomains: []string{"evil.example"}},
		Mode:  scrutineerv1alpha1.PolicyModeAuditOnly,
		Match: &scrutineerv1alpha1.MatchedPolicyRef{Kind: "AgentPolicy", Name: "baseline"},
	}}
	inline := scrutineerv1alpha1.PolicyRules{DeniedDomains: []string{"tracker.example"}}
	resolved := Resolve(layers, inline)
	if len(resolved.Rules.DeniedDomains) != 2 {
		t.Fatalf("DeniedDomains = %v", resolved.Rules.DeniedDomains)
	}
	if resolved.Mode != scrutineerv1alpha1.PolicyModeAuditOnly {
		t.Fatalf("mode = %q", resolved.Mode)
	}
	if len(resolved.Matched) != 1 || resolved.Matched[0].Name != "baseline" {
		t.Fatalf("matched = %v", resolved.Matched)
	}
}
