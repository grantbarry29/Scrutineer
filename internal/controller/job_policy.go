/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

// relayManagedEnvKeys are env vars Relay owns for policy/task propagation.
var relayManagedEnvKeys = []string{
	EnvRelaySessionName,
	EnvRelaySessionNamespace,
	EnvTaskDescription,
	EnvTaskPrompt,
	EnvModelProvider,
	EnvModelName,
	EnvPolicyAllowedDomains,
	EnvPolicyDeniedDomains,
	EnvPolicyAllowedCIDRs,
	EnvPolicyDeniedCIDRs,
	EnvPolicyAllowedTools,
	EnvPolicyDeniedTools,
	EnvPolicyRequireApproval,
	EnvPolicyMaxNetReqs,
	EnvPolicyMaxToolCalls,
	EnvPolicyMode,
}

func agentContainerEnv(job *batchv1.Job) map[string]string {
	if job == nil {
		return nil
	}
	for _, c := range job.Spec.Template.Spec.Containers {
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

// relayPolicyEnvDrift reports whether Relay-managed env on the Job differs from desired.
func relayPolicyEnvDrift(existing, desired *batchv1.Job) bool {
	cur := agentContainerEnv(existing)
	want := agentContainerEnv(desired)
	if cur == nil || want == nil {
		return existing != nil && desired != nil
	}
	for _, key := range relayManagedEnvKeys {
		if cur[key] != want[key] {
			return true
		}
	}
	return false
}

// jobReplaceableForPolicySync is true when the Job has not yet started executing pods.
// Kubernetes Job pod templates are immutable once pods exist; replace only while pending.
func jobReplaceableForPolicySync(job *batchv1.Job) bool {
	if job == nil {
		return false
	}
	return job.Status.Active == 0 && job.Status.Succeeded == 0 && job.Status.Failed == 0
}

// policyEnvDriftMessage explains stale env on an active Job.
func policyEnvDriftMessage(session *relayv1alpha1.AgentSession) string {
	return "Effective policy changed but the owned Job pod template is immutable while pods are active; " +
		"status.effectivePolicy is current; AGENT_POLICY_* env inside the running pod may be stale until the Job is replaced"
}
