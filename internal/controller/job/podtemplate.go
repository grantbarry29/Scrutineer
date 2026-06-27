/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package job

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/policy"
)

// BuildPodTemplateSpec renders the agent Pod template (agent container + env +
// security context + workspace volumes + RuntimeProfile pod settings + injected
// enforcement sidecars + reporter token wiring) for an AgentSession.
//
// It is the single source of truth for the agent pod across runtime backends: a
// Job backend wraps this template in a batch/v1 Job, and other backends (e.g. a
// bare Pod) can reuse it verbatim so the data-plane wiring is identical. The
// returned template carries the session labels and never auto-mounts the
// namespace-default ServiceAccount token (see below).
func BuildPodTemplateSpec(session *relayv1alpha1.AgentSession, task *Task, pol *policy.Resolved, profile *relayv1alpha1.RuntimeProfile) corev1.PodTemplateSpec {
	labels := labelsFor(session)
	rt := session.Spec.Runtime

	container := corev1.Container{
		Name:            AgentContainerName,
		Image:           rt.Image,
		ImagePullPolicy: rt.ImagePullPolicy,
		Command:         rt.Command,
		Args:            rt.Args,
		Resources:       rt.Resources,
		Env:             applyAgentSidecarEnv(buildEnv(session, task, pol), profile),
		SecurityContext: mergeContainerSecurityContext(defaultContainerSecurityContext(), profile),
	}

	volumes, mounts := buildWorkspaceVolumes(&session.Spec.Workspace)
	container.VolumeMounts = mounts

	// Least privilege: never auto-mount the namespace-default ServiceAccount token
	// into the agent pod. A compromised/prompt-injected agent must not get an
	// apiserver-audience token for free. Enforcement sidecars that legitimately
	// report evidence carry their own narrowly-scoped projected reporter token
	// (audience relay-reporter), which is unaffected by this setting.
	automountSAToken := false
	podSpec := corev1.PodSpec{
		RestartPolicy:                corev1.RestartPolicyNever,
		ServiceAccountName:           rt.ServiceAccountName,
		AutomountServiceAccountToken: &automountSAToken,
		Containers:                   []corev1.Container{container},
		Volumes:                      volumes,
		NodeSelector:                 rt.NodeSelector,
		Tolerations:                  rt.Tolerations,
	}
	applyRuntimeProfileToPodSpec(&podSpec, profile)
	injectSidecars(&podSpec, session, pol, profile)

	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: labels,
		},
		Spec: podSpec,
	}
}
