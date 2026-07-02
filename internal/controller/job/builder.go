/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package job

import (
	"strconv"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/policy"
)

// Avoid an unused import lint when intstr is not needed.
var _ = intstr.FromInt

// NameFor returns the deterministic Job name for an AgentSession.
func NameFor(session *scrutineerv1alpha1.AgentSession) string {
	return NamePrefix + session.Name
}

func labelsFor(session *scrutineerv1alpha1.AgentSession) map[string]string {
	return map[string]string{
		LabelAppName:      AppNameScrutineer,
		LabelAppComponent: ComponentSession,
		LabelSessionRef:   session.Name,
	}
}

// Build renders the batch/v1 Job that should run the AgentSession. The agent Pod
// template is produced by BuildPodTemplateSpec (shared across runtime backends);
// Build only adds the Job-specific wrapper (name, backoff/TTL, deadline).
func Build(session *scrutineerv1alpha1.AgentSession, task *Task, pol *policy.Resolved, profile *scrutineerv1alpha1.RuntimeProfile) *batchv1.Job {
	rt := session.Spec.Runtime

	backoffLimit := int32(0)
	ttl := int32(300)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      NameFor(session),
			Namespace: session.Namespace,
			Labels:    labelsFor(session),
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttl,
			Template:                BuildPodTemplateSpec(session, task, pol, profile),
		},
	}

	if rt.TimeoutSeconds != nil && *rt.TimeoutSeconds > 0 {
		t := *rt.TimeoutSeconds
		job.Spec.ActiveDeadlineSeconds = &t
	}

	return job
}

func defaultContainerSecurityContext() *corev1.SecurityContext {
	allowPrivilegeEscalation := false
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: &allowPrivilegeEscalation,
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}
}

func mergeContainerSecurityContext(base *corev1.SecurityContext, profile *scrutineerv1alpha1.RuntimeProfile) *corev1.SecurityContext {
	if base == nil {
		base = defaultContainerSecurityContext()
	}
	out := base.DeepCopy()
	if profile == nil || profile.Spec.Container == nil {
		return out
	}
	c := profile.Spec.Container
	if c.RunAsNonRoot != nil {
		out.RunAsNonRoot = c.RunAsNonRoot
	}
	if c.ReadOnlyRootFilesystem != nil {
		out.ReadOnlyRootFilesystem = c.ReadOnlyRootFilesystem
	}
	if c.AllowPrivilegeEscalation != nil {
		out.AllowPrivilegeEscalation = c.AllowPrivilegeEscalation
	}
	if c.Capabilities != nil {
		out.Capabilities = c.Capabilities.DeepCopy()
	}
	return out
}

func applyRuntimeProfileToPodSpec(spec *corev1.PodSpec, profile *scrutineerv1alpha1.RuntimeProfile) {
	if spec == nil || profile == nil || profile.Spec.Pod == nil {
		return
	}
	p := profile.Spec.Pod
	if p.RuntimeClassName != "" {
		name := p.RuntimeClassName
		spec.RuntimeClassName = &name
	}
	if p.SeccompProfile != nil {
		if spec.SecurityContext == nil {
			spec.SecurityContext = &corev1.PodSecurityContext{}
		}
		spec.SecurityContext.SeccompProfile = p.SeccompProfile.DeepCopy()
	}
	// SA-token automount opt-in (Slice D, #63): only an explicit true relaxes the
	// hardened automount=false default set by BuildPodTemplateSpec.
	if p.AutomountServiceAccountToken != nil && *p.AutomountServiceAccountToken {
		yes := true
		spec.AutomountServiceAccountToken = &yes
	}
}

func buildWorkspaceVolumes(ws *scrutineerv1alpha1.WorkspaceSpec) ([]corev1.Volume, []corev1.VolumeMount) {
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

func buildEnv(session *scrutineerv1alpha1.AgentSession, task *Task, resolved *policy.Resolved) []corev1.EnvVar {
	if task == nil {
		task = &Task{}
	}
	rules := session.Spec.Policy.PolicyRules
	mode := scrutineerv1alpha1.PolicyModeAuditOnly
	if resolved != nil {
		rules = resolved.Rules
		mode = resolved.Mode
	}

	env := []corev1.EnvVar{
		{Name: EnvScrutineerSessionName, Value: session.Name},
		{Name: EnvScrutineerSessionNamespace, Value: session.Namespace},
		{Name: EnvTaskDescription, Value: task.Description},
		{Name: EnvTaskPrompt, Value: task.Prompt},
		{Name: EnvModelProvider, Value: session.Spec.Model.Provider},
		{Name: EnvModelName, Value: session.Spec.Model.Name},
		{Name: EnvModelBaseURL, Value: session.Spec.Model.BaseURL},
		{Name: EnvPolicyAllowedDomains, Value: csv(rules.AllowedDomains)},
		{Name: EnvPolicyDeniedDomains, Value: csv(rules.DeniedDomains)},
		{Name: EnvPolicyAllowedCIDRs, Value: csv(rules.AllowedCIDRs)},
		{Name: EnvPolicyDeniedCIDRs, Value: csv(rules.DeniedCIDRs)},
		{Name: EnvPolicyAllowedTools, Value: csv(rules.AllowedTools)},
		{Name: EnvPolicyDeniedTools, Value: csv(rules.DeniedTools)},
		{Name: EnvPolicyRequireApproval, Value: csv(rules.RequireHumanApproval)},
		{Name: EnvPolicyMaxNetReqs, Value: int32PtrToStr(rules.MaxNetworkRequests)},
		{Name: EnvPolicyMaxToolCalls, Value: int32PtrToStr(rules.MaxToolCalls)},
		{Name: EnvPolicyMaxToolCallsPerMinute, Value: int32PtrToStr(rules.MaxCallsPerMinute)},
		{Name: EnvPolicyAllowedPaths, Value: csv(rules.AllowedPaths)},
		{Name: EnvPolicyDeniedPaths, Value: csv(rules.DeniedPaths)},
		{Name: EnvPolicyMaxWorkspaceBytes, Value: int64PtrToStr(rules.MaxWorkspaceBytes)},
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

func int64PtrToStr(p *int64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatInt(*p, 10)
}
