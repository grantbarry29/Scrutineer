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

	corev1 "k8s.io/api/core/v1"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/policy"
)

func TestMergeContainerSecurityContext(t *testing.T) {
	base := defaultContainerSecurityContext()
	runAsNonRoot := true
	profile := &relayv1alpha1.RuntimeProfile{
		Spec: relayv1alpha1.RuntimeProfileSpec{
			Container: &relayv1alpha1.RuntimeProfileContainerSpec{
				RunAsNonRoot: &runAsNonRoot,
			},
		},
	}

	merged := mergeContainerSecurityContext(base, profile)
	if merged.RunAsNonRoot == nil || !*merged.RunAsNonRoot {
		t.Fatalf("expected runAsNonRoot true from profile")
	}
	if merged.Capabilities == nil || len(merged.Capabilities.Drop) == 0 {
		t.Fatalf("expected baseline capability drops to remain")
	}

	if got := mergeContainerSecurityContext(base, nil); got == nil || got.RunAsNonRoot != nil {
		t.Fatalf("expected nil profile to return baseline without profile overrides")
	}
}

func TestApplyRuntimeProfileToPodSpec(t *testing.T) {
	spec := &corev1.PodSpec{}
	profile := &relayv1alpha1.RuntimeProfile{
		Spec: relayv1alpha1.RuntimeProfileSpec{
			Pod: &relayv1alpha1.RuntimeProfilePodSpec{
				RuntimeClassName: "gvisor",
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
			},
		},
	}

	applyRuntimeProfileToPodSpec(spec, profile)
	if spec.RuntimeClassName == nil || *spec.RuntimeClassName != "gvisor" {
		t.Fatalf("runtimeClassName = %v, want gvisor", spec.RuntimeClassName)
	}
	if spec.SecurityContext == nil || spec.SecurityContext.SeccompProfile == nil {
		t.Fatalf("expected seccomp profile on pod spec")
	}
}

func TestBuild_ephemeralWorkspace(t *testing.T) {
	session := minimalSession()
	session.Spec.Workspace = relayv1alpha1.WorkspaceSpec{
		Ephemeral: true,
		Size:      "1Gi",
		MountPath: "/data",
	}
	job := Build(session, &Task{}, nil, nil)
	spec := job.Spec.Template.Spec
	if len(spec.Volumes) != 1 || spec.Volumes[0].Name != "workspace" {
		t.Fatalf("volumes = %+v", spec.Volumes)
	}
	if spec.Volumes[0].EmptyDir == nil || spec.Volumes[0].EmptyDir.SizeLimit == nil {
		t.Fatal("expected emptyDir size limit")
	}
	agent := spec.Containers[0]
	if len(agent.VolumeMounts) != 1 || agent.VolumeMounts[0].MountPath != "/data" {
		t.Fatalf("mounts = %+v", agent.VolumeMounts)
	}
}

func TestBuild_disablesServiceAccountTokenAutomount(t *testing.T) {
	spec := Build(minimalSession(), &Task{}, nil, nil).Spec.Template.Spec
	if spec.AutomountServiceAccountToken == nil {
		t.Fatal("expected automountServiceAccountToken to be set explicitly")
	}
	if *spec.AutomountServiceAccountToken {
		t.Fatal("expected automountServiceAccountToken=false on the agent pod")
	}
}

func TestBuild_modelBaseURLEnv(t *testing.T) {
	session := minimalSession()
	session.Spec.Model.BaseURL = "https://openrouter.ai/api/v1"
	job := Build(session, &Task{}, nil, nil)
	env := envVarsToMap(job.Spec.Template.Spec.Containers[0].Env)
	if env[EnvModelBaseURL] != "https://openrouter.ai/api/v1" {
		t.Fatalf("AGENT_MODEL_BASE_URL = %q, want OpenRouter base URL", env[EnvModelBaseURL])
	}

	// Unset baseURL propagates as empty (provider default endpoint).
	empty := envVarsToMap(Build(minimalSession(), &Task{}, nil, nil).Spec.Template.Spec.Containers[0].Env)
	if empty[EnvModelBaseURL] != "" {
		t.Fatalf("AGENT_MODEL_BASE_URL = %q, want empty when unset", empty[EnvModelBaseURL])
	}
}

func TestPolicyEnvDrift_modelBaseURL(t *testing.T) {
	base := minimalSession()
	withURL := minimalSession()
	withURL.Spec.Model.BaseURL = "https://openrouter.ai/api/v1"

	a := Build(base, &Task{}, nil, nil)
	b := Build(withURL, &Task{}, nil, nil)
	if !PolicyEnvDrift(a, b) {
		t.Fatal("expected drift when model.baseURL differs")
	}
}

func TestBuild_policyCapEnv(t *testing.T) {
	maxNet := int32(50)
	maxTool := int32(10)
	maxPerMin := int32(5)
	maxBytes := int64(1_000_000)
	pol := &policy.Resolved{
		Mode: relayv1alpha1.PolicyModeEnforced,
		Rules: relayv1alpha1.PolicyRules{
			MaxNetworkRequests: &maxNet,
			MaxToolCalls:       &maxTool,
			MaxCallsPerMinute:  &maxPerMin,
			MaxWorkspaceBytes:  &maxBytes,
		},
	}
	job := Build(minimalSession(), &Task{}, pol, nil)
	env := envVarsToMap(job.Spec.Template.Spec.Containers[0].Env)
	if env[EnvPolicyMaxNetReqs] != "50" || env[EnvPolicyMaxToolCalls] != "10" {
		t.Fatalf("cap env = %+v", env)
	}
	if env[EnvPolicyMaxToolCallsPerMinute] != "5" || env[EnvPolicyMaxWorkspaceBytes] != "1000000" {
		t.Fatalf("cap env = %+v", env)
	}
}
