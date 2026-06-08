/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	"testing"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

func TestSessionReferencesRuntimeProfile(t *testing.T) {
	session := &relayv1alpha1.AgentSession{
		Spec: relayv1alpha1.AgentSessionSpec{
			RuntimeProfileRef: &relayv1alpha1.RuntimeProfileRef{Name: "hardened"},
		},
	}
	if !sessionReferencesRuntimeProfile(session, "hardened") {
		t.Fatal("expected match on profile name")
	}
	if sessionReferencesRuntimeProfile(session, "other") {
		t.Fatal("expected no match on different name")
	}
	session.Spec.RuntimeProfileRef = nil
	if sessionReferencesRuntimeProfile(session, "hardened") {
		t.Fatal("expected no match without ref")
	}
}
