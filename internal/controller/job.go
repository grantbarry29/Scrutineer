/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"fmt"
	"strconv"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/policy"
)

// Avoid an unused import lint when intstr is not needed.
var _ = intstr.FromInt

// jobNameFor returns the deterministic Job name for an AgentSession.
func jobNameFor(session *relayv1alpha1.AgentSession) string {
	return JobNamePrefix + session.Name
}

// commonLabelsFor returns the standard labels applied to objects owned by an AgentSession.
func commonLabelsFor(session *relayv1alpha1.AgentSession) map[string]string {
	return map[string]string{
		LabelAppName:      AppNameRelay,
		LabelAppComponent: ComponentSession,
		LabelSessionRef:   session.Name,
	}
}

// buildJob renders the batch/v1 Job that should run the AgentSession.
//
// Future enforcement hooks (Envoy sidecar, DNS proxy, eBPF agents, Cilium policies, etc.)
// should be added by composing additional containers/volumes/policies onto the spec
// returned here, rather than by special-casing the controller.
func buildJob(session *relayv1alpha1.AgentSession, task *ResolvedTask, pol *policy.Resolved) *batchv1.Job {
	labels := commonLabelsFor(session)
	rt := session.Spec.Runtime

	backoffLimit := int32(0)
	ttl := int32(300)

	container := corev1.Container{
		Name:            AgentContainerName,
		Image:           rt.Image,
		ImagePullPolicy: rt.ImagePullPolicy,
		Command:         rt.Command,
		Args:            rt.Args,
		Resources:       rt.Resources,
		Env:             buildEnv(session, task, pol),
		SecurityContext: defaultContainerSecurityContext(),
	}

	volumes, mounts := buildWorkspaceVolumes(&session.Spec.Workspace)
	container.VolumeMounts = mounts

	podSpec := corev1.PodSpec{
		RestartPolicy:      corev1.RestartPolicyNever,
		ServiceAccountName: rt.ServiceAccountName,
		Containers:         []corev1.Container{container},
		Volumes:            volumes,
		NodeSelector:       rt.NodeSelector,
		Tolerations:        rt.Tolerations,
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobNameFor(session),
			Namespace: session.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: podSpec,
			},
		},
	}

	if rt.TimeoutSeconds != nil && *rt.TimeoutSeconds > 0 {
		t := *rt.TimeoutSeconds
		job.Spec.ActiveDeadlineSeconds = &t
	}

	return job
}

// defaultContainerSecurityContext returns the baseline container hardening Relay applies.
//
// We deliberately do NOT set RunAsNonRoot here. Busybox-style sample images often run as
// root, and forcing non-root in the MVP would break the "successful sample" acceptance
// criterion. A future RuntimeProfile CRD will let operators opt into a stricter profile
// (RunAsNonRoot, readOnlyRootFilesystem, seccomp, AppArmor, etc.) without changing user
// AgentSession specs.
func defaultContainerSecurityContext() *corev1.SecurityContext {
	allowPrivilegeEscalation := false
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: &allowPrivilegeEscalation,
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}
}

// buildWorkspaceVolumes returns the volume/mount pair backing the workspace.
//
// For MVP we only support an in-pod emptyDir. A future WorkspaceSpec field will switch on
// type (emptyDir / PVC / projected) and provision a backing PVC when persistence is needed.
func buildWorkspaceVolumes(ws *relayv1alpha1.WorkspaceSpec) ([]corev1.Volume, []corev1.VolumeMount) {
	if ws == nil || !ws.Ephemeral {
		return nil, nil
	}

	mountPath := ws.MountPath
	if mountPath == "" {
		mountPath = DefaultWorkspaceMountPath
	}

	emptyDir := &corev1.EmptyDirVolumeSource{}
	if ws.Size != "" {
		if q, err := resource.ParseQuantity(ws.Size); err == nil {
			emptyDir.SizeLimit = &q
		}
	}

	v := corev1.Volume{
		Name:         "workspace",
		VolumeSource: corev1.VolumeSource{EmptyDir: emptyDir},
	}
	m := corev1.VolumeMount{
		Name:      "workspace",
		MountPath: mountPath,
	}
	return []corev1.Volume{v}, []corev1.VolumeMount{m}
}

// buildEnv returns the Relay-managed environment variables for an AgentSession,
// followed by any user-supplied envs from spec.runtime.env.
//
// Future enforcement (Envoy sidecar / DNS proxy / tool gateway) will read these env vars
// or a sibling ConfigMap to apply policy at runtime. Until then they exist primarily so
// the agent process inside the container can self-report what policy it believes is active.
func buildEnv(session *relayv1alpha1.AgentSession, task *ResolvedTask, resolved *policy.Resolved) []corev1.EnvVar {
	if task == nil {
		task = &ResolvedTask{}
	}
	rules := session.Spec.Policy.PolicyRules
	mode := relayv1alpha1.PolicyModeAuditOnly
	if resolved != nil {
		rules = resolved.Rules
		mode = resolved.Mode
	}

	env := []corev1.EnvVar{
		{Name: EnvRelaySessionName, Value: session.Name},
		{Name: EnvRelaySessionNamespace, Value: session.Namespace},
		{Name: EnvTaskDescription, Value: task.Description},
		{Name: EnvTaskPrompt, Value: task.Prompt},
		{Name: EnvModelProvider, Value: session.Spec.Model.Provider},
		{Name: EnvModelName, Value: session.Spec.Model.Name},
		{Name: EnvPolicyAllowedDomains, Value: csv(rules.AllowedDomains)},
		{Name: EnvPolicyDeniedDomains, Value: csv(rules.DeniedDomains)},
		{Name: EnvPolicyAllowedCIDRs, Value: csv(rules.AllowedCIDRs)},
		{Name: EnvPolicyDeniedCIDRs, Value: csv(rules.DeniedCIDRs)},
		{Name: EnvPolicyAllowedTools, Value: csv(rules.AllowedTools)},
		{Name: EnvPolicyDeniedTools, Value: csv(rules.DeniedTools)},
		{Name: EnvPolicyRequireApproval, Value: csv(rules.RequireHumanApproval)},
		{Name: EnvPolicyMaxNetReqs, Value: int32PtrToStr(rules.MaxNetworkRequests)},
		{Name: EnvPolicyMaxToolCalls, Value: int32PtrToStr(rules.MaxToolCalls)},
		{Name: EnvPolicyMode, Value: string(mode)},
	}

	env = append(env, session.Spec.Runtime.Env...)
	return env
}

func csv(in []string) string {
	return strings.Join(in, ",")
}

func int32PtrToStr(p *int32) string {
	if p == nil {
		return ""
	}
	return strconv.FormatInt(int64(*p), 10)
}

// jobsEqualIgnoringStatus returns true when two Jobs are equivalent for the purposes of
// reconcile-driven drift detection. It is intentionally narrow for the MVP.
func jobsEqualIgnoringStatus(a, b *batchv1.Job) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Name != b.Name || a.Namespace != b.Namespace {
		return false
	}
	// We intentionally do not deep-diff the spec here; the MVP creates the Job once and
	// treats it as immutable. Drift correction will arrive with RuntimeProfile.
	return true
}

// describeJobPhase produces a short human-readable phase string from a Job's status,
// for use in events and condition messages.
func describeJobPhase(j *batchv1.Job) string {
	if j == nil {
		return "unknown"
	}
	switch {
	case j.Status.Succeeded > 0:
		return "succeeded"
	case j.Status.Failed > 0:
		return fmt.Sprintf("failed (%d retries)", j.Status.Failed)
	case j.Status.Active > 0:
		return "running"
	default:
		return "pending"
	}
}
