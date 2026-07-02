/*
Copyright 2026 The Scrutineer Authors.

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

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/policy"
)

func TestPolicyEnvDrift(t *testing.T) {
	session := &scrutineerv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "s1"},
		Spec: scrutineerv1alpha1.AgentSessionSpec{
			Model:   scrutineerv1alpha1.ModelSpec{Provider: "openai", Name: "gpt-4"},
			Runtime: scrutineerv1alpha1.RuntimeSpec{Image: "busybox"},
		},
	}
	task := &Task{Description: "d", Prompt: "p"}
	pol := &policy.Resolved{Mode: scrutineerv1alpha1.PolicyModeAuditOnly}

	a := Build(session, task, pol, nil)
	b := Build(session, task, pol, nil)
	if PolicyEnvDrift(a, b) {
		t.Fatal("identical jobs should not drift")
	}

	pol2 := &policy.Resolved{
		Mode:  scrutineerv1alpha1.PolicyModeEnforced,
		Rules: scrutineerv1alpha1.PolicyRules{DeniedTools: []string{"x"}},
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

func TestPolicyEnvDriftMessage(t *testing.T) {
	if PolicyEnvDriftMessage() == "" {
		t.Fatal("expected non-empty drift message")
	}
}

func TestRuntimeProfileDrift_runtimeClassAndSecurity(t *testing.T) {
	base := Build(minimalSession(), &Task{}, nil, nil)
	gvisor := "gvisor"
	withClass := Build(minimalSession(), &Task{}, nil, &scrutineerv1alpha1.RuntimeProfile{
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Pod: &scrutineerv1alpha1.RuntimeProfilePodSpec{RuntimeClassName: gvisor},
		},
	})
	if !RuntimeProfileDrift(base, withClass) {
		t.Fatal("expected drift for runtime class")
	}

	runAsNonRoot := true
	withSec := Build(minimalSession(), &Task{}, nil, &scrutineerv1alpha1.RuntimeProfile{
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Container: &scrutineerv1alpha1.RuntimeProfileContainerSpec{RunAsNonRoot: &runAsNonRoot},
		},
	})
	if !RuntimeProfileDrift(base, withSec) {
		t.Fatal("expected drift for container security context")
	}
}

func TestRuntimeProfileDrift_sidecarEnv(t *testing.T) {
	dns := Build(minimalSession(), &Task{}, &policy.Resolved{
		Mode:  scrutineerv1alpha1.PolicyModeEnforced,
		Rules: scrutineerv1alpha1.PolicyRules{DeniedDomains: []string{"evil.example"}},
	}, profileWithSidecar(EnforcementTypeDNSProxy))
	dns2 := Build(minimalSession(), &Task{}, &policy.Resolved{
		Mode:  scrutineerv1alpha1.PolicyModeEnforced,
		Rules: scrutineerv1alpha1.PolicyRules{DeniedDomains: []string{"other.example"}},
	}, profileWithSidecar(EnforcementTypeDNSProxy))
	if !RuntimeProfileDrift(dns, dns2) {
		t.Fatal("expected drift when sidecar policy env differs")
	}
}
