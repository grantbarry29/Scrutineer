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

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

func TestSessionReferencesAgentPolicy(t *testing.T) {
	session := &scrutineerv1alpha1.AgentSession{
		Spec: scrutineerv1alpha1.AgentSessionSpec{
			PolicyRefs: []scrutineerv1alpha1.PolicyRef{
				{Kind: "AgentPolicy", Name: "baseline"},
				{Kind: "AgentPolicy", Name: "other"},
			},
		},
	}
	if !sessionReferencesAgentPolicy(session, "baseline") {
		t.Fatal("expected baseline ref")
	}
	if sessionReferencesAgentPolicy(session, "missing") {
		t.Fatal("unexpected match")
	}
	session.Spec.PolicyRefs[0].Kind = "ToolPolicy"
	if sessionReferencesAgentPolicy(session, "baseline") {
		t.Fatal("ToolPolicy kind should not match AgentPolicy watch")
	}
	if !sessionReferencesPolicy(session, "ToolPolicy", "baseline") {
		t.Fatal("expected ToolPolicy ref match")
	}
}
