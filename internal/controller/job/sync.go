/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package job

import (
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

var managedEnvKeys = []string{
	EnvScrutineerSessionName,
	EnvScrutineerSessionNamespace,
	EnvTaskDescription,
	EnvTaskPrompt,
	EnvModelProvider,
	EnvModelName,
	EnvModelBaseURL,
	EnvPolicyAllowedDomains,
	EnvPolicyDeniedDomains,
	EnvPolicyAllowedCIDRs,
	EnvPolicyDeniedCIDRs,
	EnvPolicyAllowedTools,
	EnvPolicyDeniedTools,
	EnvPolicyRequireApproval,
	EnvPolicyMaxNetReqs,
	EnvPolicyMaxToolCalls,
	EnvPolicyMaxToolCallsPerMinute,
	EnvPolicyAllowedPaths,
	EnvPolicyDeniedPaths,
	EnvPolicyMaxWorkspaceBytes,
	EnvPolicyMode,
}

// PolicyEnvDrift reports whether Scrutineer-managed env on the Job differs from desired.
func PolicyEnvDrift(existing, desired *batchv1.Job) bool {
	cur := agentContainerEnv(existing)
	want := agentContainerEnv(desired)
	if cur == nil || want == nil {
		return existing != nil && desired != nil
	}
	for _, key := range managedEnvKeys {
		if cur[key] != want[key] {
			return true
		}
	}
	return false
}

// ReplaceableForSync is true when the Job has not yet started executing pods.
func ReplaceableForSync(j *batchv1.Job) bool {
	if j == nil {
		return false
	}
	return j.Status.Active == 0 && j.Status.Succeeded == 0 && j.Status.Failed == 0
}

// PolicyEnvDriftMessage explains stale env on an active Job.
func PolicyEnvDriftMessage() string {
	return "Effective policy changed but the owned Job pod template is immutable while pods are active; " +
		"status.effectivePolicy is current; AGENT_POLICY_* env inside the running pod may be stale until the Job is replaced"
}

// RuntimeProfileDrift reports whether RuntimeProfile-derived pod template fields differ.
func RuntimeProfileDrift(existing, desired *batchv1.Job) bool {
	if existing == nil || desired == nil {
		return existing != desired
	}
	ex := existing.Spec.Template.Spec
	want := desired.Spec.Template.Spec
	if !ptrStringEqual(ex.RuntimeClassName, want.RuntimeClassName) {
		return true
	}
	if !seccompProfilesEqual(podSeccompProfile(&ex), podSeccompProfile(&want)) {
		return true
	}
	exAgent := agentContainerFromPodSpec(&ex)
	wantAgent := agentContainerFromPodSpec(&want)
	if exAgent == nil || wantAgent == nil {
		return exAgent != wantAgent
	}
	if !securityContextsEqual(exAgent.SecurityContext, wantAgent.SecurityContext) {
		return true
	}
	return !sidecarContainersEqual(sidecarContainers(&ex), sidecarContainers(&want))
}

func agentContainerEnv(j *batchv1.Job) map[string]string {
	if j == nil {
		return nil
	}
	for _, c := range j.Spec.Template.Spec.Containers {
		if c.Name == AgentContainerName {
			return envVarsToMap(c.Env)
		}
	}
	return nil
}

func envVarsToMap(vars []corev1.EnvVar) map[string]string {
	out := make(map[string]string, len(vars))
	for _, e := range vars {
		out[e.Name] = e.Value
	}
	return out
}

func agentContainerFromPodSpec(spec *corev1.PodSpec) *corev1.Container {
	if spec == nil {
		return nil
	}
	for i := range spec.Containers {
		if spec.Containers[i].Name == AgentContainerName {
			return &spec.Containers[i]
		}
	}
	return nil
}

func podSeccompProfile(spec *corev1.PodSpec) *corev1.SeccompProfile {
	if spec == nil || spec.SecurityContext == nil {
		return nil
	}
	return spec.SecurityContext.SeccompProfile
}

func ptrStringEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func seccompProfilesEqual(a, b *corev1.SeccompProfile) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Type == b.Type && a.LocalhostProfile == b.LocalhostProfile
}

func securityContextsEqual(a, b *corev1.SecurityContext) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if !boolPtrEqual(a.RunAsNonRoot, b.RunAsNonRoot) ||
		!boolPtrEqual(a.ReadOnlyRootFilesystem, b.ReadOnlyRootFilesystem) ||
		!boolPtrEqual(a.AllowPrivilegeEscalation, b.AllowPrivilegeEscalation) {
		return false
	}
	return capabilitiesEqual(a.Capabilities, b.Capabilities)
}

func boolPtrEqual(a, b *bool) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func capabilitiesEqual(a, b *corev1.Capabilities) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if len(a.Add) != len(b.Add) || len(a.Drop) != len(b.Drop) {
		return false
	}
	for i := range a.Add {
		if a.Add[i] != b.Add[i] {
			return false
		}
	}
	for i := range a.Drop {
		if a.Drop[i] != b.Drop[i] {
			return false
		}
	}
	return true
}
