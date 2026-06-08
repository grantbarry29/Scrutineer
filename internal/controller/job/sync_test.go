/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package job

import (
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/policy"
)

func TestPolicyEnvDrift(t *testing.T) {
	session := &relayv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "s1"},
		Spec: relayv1alpha1.AgentSessionSpec{
			Model:   relayv1alpha1.ModelSpec{Provider: "openai", Name: "gpt-4"},
			Runtime: relayv1alpha1.RuntimeSpec{Image: "busybox"},
		},
	}
	task := &Task{Description: "d", Prompt: "p"}
	pol := &policy.Resolved{Mode: relayv1alpha1.PolicyModeAuditOnly}

	a := Build(session, task, pol, nil)
	b := Build(session, task, pol, nil)
	if PolicyEnvDrift(a, b) {
		t.Fatal("identical jobs should not drift")
	}

	pol2 := &policy.Resolved{
		Mode:  relayv1alpha1.PolicyModeEnforced,
		Rules: relayv1alpha1.PolicyRules{DeniedTools: []string{"x"}},
	}
	c := Build(session, task, pol2, nil)
	if !PolicyEnvDrift(a, c) {
		t.Fatal("expected drift when policy env differs")
	}
}

func TestReplaceableForSync(t *testing.T) {
	if ReplaceableForSync(nil) {
		t.Fatal("nil job")
	}
	pending := &batchv1.Job{Status: batchv1.JobStatus{}}
	if !ReplaceableForSync(pending) {
		t.Fatal("pending job should be replaceable")
	}
	active := &batchv1.Job{Status: batchv1.JobStatus{Active: 1}}
	if ReplaceableForSync(active) {
		t.Fatal("active job should not be replaceable")
	}
}
