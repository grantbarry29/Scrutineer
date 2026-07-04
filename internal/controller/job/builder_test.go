/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package job

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/policy"
)

func TestBuildPodTemplateSpec(t *testing.T) {
	enabled := true
	session := &scrutineerv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Name: "sess", Namespace: "ns"},
		Spec: scrutineerv1alpha1.AgentSessionSpec{
			Runtime: scrutineerv1alpha1.RuntimeSpec{
				Orchestrator:       "kubernetes-job",
				Image:              "busybox:latest",
				ServiceAccountName: "default",
			},
			Workspace: scrutineerv1alpha1.WorkspaceSpec{Ephemeral: true, MountPath: "/workspace"},
		},
	}
	profile := &scrutineerv1alpha1.RuntimeProfile{
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Enforcement: []scrutineerv1alpha1.RuntimeProfileEnforcement{
				{Name: "envoy", Type: EnforcementTypeEnvoy, Enabled: &enabled},
			},
		},
	}

	tmpl := BuildPodTemplateSpec(session, &Task{Prompt: "p"}, nil, profile)

	if tmpl.Labels[LabelSessionRef] != "sess" {
		t.Fatalf("expected session label on template, got %v", tmpl.Labels)
	}
	if tmpl.Spec.AutomountServiceAccountToken == nil || *tmpl.Spec.AutomountServiceAccountToken {
		t.Fatalf("expected AutomountServiceAccountToken=false on agent pod template")
	}

	byName := map[string]corev1.Container{}
	for _, c := range tmpl.Spec.Containers {
		byName[c.Name] = c
	}
	if _, ok := byName[AgentContainerName]; !ok {
		t.Fatalf("expected agent container %q in template", AgentContainerName)
	}
	// The Envoy egress proxy is out-of-pod: the agent pod carries no injected sidecar.
	if len(tmpl.Spec.Containers) != 1 {
		t.Fatalf("expected agent-only pod template, got containers %+v", tmpl.Spec.Containers)
	}

	// Build must wrap the identical template (no behavior change from the extraction).
	job := Build(session, &Task{Prompt: "p"}, nil, profile)
	if !reflect.DeepEqual(job.Spec.Template, tmpl) {
		t.Fatalf("Build template diverged from BuildPodTemplateSpec")
	}
}

func TestMergeContainerSecurityContext(t *testing.T) {
	base := defaultContainerSecurityContext()
	runAsNonRoot := true
	profile := &scrutineerv1alpha1.RuntimeProfile{
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Container: &scrutineerv1alpha1.RuntimeProfileContainerSpec{
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
	profile := &scrutineerv1alpha1.RuntimeProfile{
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Pod: &scrutineerv1alpha1.RuntimeProfilePodSpec{
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
	session.Spec.Workspace = scrutineerv1alpha1.WorkspaceSpec{
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

// The RuntimeProfile automount opt-in (Slice D, #63) re-enables the SA token for agents
// that legitimately need the Kubernetes API. Only an explicit true flips it; nil and
// false both keep the hardened default.
func TestBuild_profileAutomountOptIn(t *testing.T) {
	tr := true
	fa := false
	cases := []struct {
		name string
		val  *bool
		want bool
	}{
		{"opt-in true", &tr, true},
		{"explicit false", &fa, false},
		{"nil keeps default", nil, false},
	}
	for _, tc := range cases {
		profile := &scrutineerv1alpha1.RuntimeProfile{
			Spec: scrutineerv1alpha1.RuntimeProfileSpec{
				Pod: &scrutineerv1alpha1.RuntimeProfilePodSpec{
					AutomountServiceAccountToken: tc.val,
				},
			},
		}
		spec := Build(minimalSession(), &Task{}, nil, profile).Spec.Template.Spec
		if spec.AutomountServiceAccountToken == nil || *spec.AutomountServiceAccountToken != tc.want {
			t.Fatalf("%s: automount = %v, want %v", tc.name, spec.AutomountServiceAccountToken, tc.want)
		}
	}
	// No Pod section at all keeps the hardened default.
	spec := Build(minimalSession(), &Task{}, nil, &scrutineerv1alpha1.RuntimeProfile{}).Spec.Template.Spec
	if spec.AutomountServiceAccountToken == nil || *spec.AutomountServiceAccountToken {
		t.Fatal("empty profile must keep automount=false")
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

func TestBuild_policyApprovalEnv(t *testing.T) {
	pol := &policy.Resolved{
		Mode: scrutineerv1alpha1.PolicyModeEnforced,
		Rules: scrutineerv1alpha1.PolicyRules{
			RequireHumanApproval: []string{"deploy", "wire-transfer"},
		},
	}
	job := Build(minimalSession(), &Task{}, pol, nil)
	env := envVarsToMap(job.Spec.Template.Spec.Containers[0].Env)
	if env[EnvPolicyRequireApproval] != "deploy,wire-transfer" {
		t.Fatalf("approval env = %+v", env)
	}
	if env[EnvPolicyMode] != "enforced" {
		t.Fatalf("mode env = %+v", env)
	}
}
