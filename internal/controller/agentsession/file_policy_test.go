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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/workspace"
)

func TestApplyFilePolicyRuntimeEvent_populatesViolations(t *testing.T) {
	ts := time.Unix(1_700_000_000, 0)
	session := &scrutineerv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"},
		Status: scrutineerv1alpha1.AgentSessionStatus{
			EffectivePolicy: &scrutineerv1alpha1.EffectivePolicyStatus{
				Mode: scrutineerv1alpha1.PolicyModeEnforced,
				PolicyRules: scrutineerv1alpha1.PolicyRules{
					DeniedPaths: []string{"/etc/**"},
				},
			},
		},
	}

	ApplyFilePolicyRuntimeEvent(session, nil, workspace.FileRequest{
		Path:      "/etc/passwd",
		Operation: "read",
	}, ts)

	if len(session.Status.Violations) != 1 {
		t.Fatalf("violations = %d", len(session.Status.Violations))
	}
	if session.Status.Violations[0].Type != "file" {
		t.Fatalf("type = %q", session.Status.Violations[0].Type)
	}
	if len(session.Status.PolicyDecisions) != 1 {
		t.Fatalf("decisions = %d", len(session.Status.PolicyDecisions))
	}
	if session.Status.PolicyDecisions[0].Type != "file" {
		t.Fatalf("decision type = %q", session.Status.PolicyDecisions[0].Type)
	}
}
