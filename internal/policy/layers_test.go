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

func TestResolve_agentAndToolPolicyLayers(t *testing.T) {
	layers := []Layer{
		{
			Rules: relayv1alpha1.PolicyRules{DeniedDomains: []string{"evil.example"}},
			Mode:  relayv1alpha1.PolicyModeAuditOnly,
			Match: &relayv1alpha1.MatchedPolicyRef{Kind: "AgentPolicy", Name: "net"},
		},
		{
			Rules: relayv1alpha1.PolicyRules{
				AllowedTools: []string{"shell"},
				DeniedTools:  []string{"kubectl"},
			},
			Mode:  relayv1alpha1.PolicyModeEnforced,
			Match: &relayv1alpha1.MatchedPolicyRef{Kind: "ToolPolicy", Name: "tools"},
		},
	}
	inline := relayv1alpha1.PolicyRules{DeniedTools: []string{"deploy"}}
	resolved := Resolve(layers, inline)

	if resolved.Mode != relayv1alpha1.PolicyModeEnforced {
		t.Fatalf("mode = %q", resolved.Mode)
	}
	if len(resolved.Matched) != 2 {
		t.Fatalf("matched = %d", len(resolved.Matched))
	}
	if len(resolved.Rules.DeniedTools) != 2 {
		t.Fatalf("denied tools = %v", resolved.Rules.DeniedTools)
	}
	if len(resolved.Rules.DeniedDomains) != 1 {
		t.Fatalf("denied domains = %v", resolved.Rules.DeniedDomains)
	}
}
