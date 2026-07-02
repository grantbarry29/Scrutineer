/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package job

import (
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
	"github.com/grantbarry29/scrutineer/internal/enforcement/dnsproxy"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
	"github.com/grantbarry29/scrutineer/internal/enforcement/toolgateway"
	"github.com/grantbarry29/scrutineer/internal/enforcement/workspace"
	"github.com/grantbarry29/scrutineer/internal/policy"
)

// Known RuntimeProfile spec.enforcement entry types.
//
// EnforcementTypeEnvoy is deliberately NOT an in-pod sidecar: the per-session Envoy egress
// proxy runs as a separate, controller-provisioned pod (evidence-integrity design, #8/#60)
// so a compromised agent cannot tamper with the enforcement point it shares a pod with.
// Enabling it in a RuntimeProfile provisions that out-of-pod proxy and points the agent at
// it via explicit-proxy env; see internal/controller/agentsession/egress_envoy.go.
const (
	EnforcementTypeDNSProxy    = "dns-proxy"
	EnforcementTypeToolGateway = "tool-gateway"
	EnforcementTypeFSGateway   = "fs-gateway"
	EnforcementTypeEnvoy       = "envoy"
)

const (
	EnvScrutineerSidecarType       = "SCRUTINEER_SIDECAR_TYPE"
	EnvScrutineerToolGatewayURL    = "SCRUTINEER_TOOL_GATEWAY_URL"
	EnvScrutineerFSGatewayURL      = "SCRUTINEER_FS_GATEWAY_URL"
	EnvScrutineerReporterURL       = "SCRUTINEER_REPORTER_URL"
	EnvScrutineerReporterTokenPath = "SCRUTINEER_REPORTER_TOKEN_PATH"
	EnvHTTPProxy                   = "HTTP_PROXY"
	EnvHTTPSProxy                  = "HTTPS_PROXY"
	EnvNoProxy                     = "NO_PROXY"
	// Lowercase proxy variants: many common tools (notably BusyBox wget, curl, and
	// Go's net/http) read the lowercase names, and BusyBox wget reads ONLY the
	// lowercase form. Setting both ensures agent egress is actually routed through
	// the dns-proxy instead of silently bypassing enforcement.
	EnvHTTPProxyLower       = "http_proxy"
	EnvHTTPSProxyLower      = "https_proxy"
	EnvNoProxyLower         = "no_proxy"
	placeholderSidecarSleep = "infinity"
)

// DefaultReporterURL is the in-cluster base URL for POST /v1/report (no path suffix).
// Matches the scrutineer-controller-reporter Service in config/manager/reporter_service.yaml.
const DefaultReporterURL = "http://scrutineer-controller-reporter.scrutineer-system.svc:8088"

const (
	ReporterTokenVolumeName    = "scrutineer-reporter-token"
	ReporterTokenMountPath     = "/var/run/secrets/scrutineer/reporter-token"
	ReporterTokenFileName      = "token"
	reporterTokenExpirationSec = int64(600)
)

// inPodSidecarTypes are the types injected as in-pod containers. Envoy is intentionally
// excluded — it is an out-of-pod egress proxy provisioned by the controller, not a sidecar.
var inPodSidecarTypes = map[string]struct{}{
	EnforcementTypeDNSProxy:    {},
	EnforcementTypeToolGateway: {},
	EnforcementTypeFSGateway:   {},
}

// injectSidecars appends enabled, known sidecar containers from a RuntimeProfile.
// Unknown or disabled entries are skipped.
func injectSidecars(spec *corev1.PodSpec, session *scrutineerv1alpha1.AgentSession, pol *policy.Resolved, profile *scrutineerv1alpha1.RuntimeProfile) {
	if spec == nil || profile == nil || len(profile.Spec.Enforcement) == 0 {
		return
	}
	wireReporterAccess(spec, profile)
	for _, sc := range profile.Spec.Enforcement {
		if !enforcementEnabled(sc) {
			continue
		}
		if _, ok := inPodSidecarTypes[sc.Type]; !ok {
			continue
		}
		spec.Containers = append(spec.Containers, buildSidecarContainer(sc, session, pol, profile))
	}
}

func hasEnabledReportingSidecar(profile *scrutineerv1alpha1.RuntimeProfile) bool {
	if profile == nil {
		return false
	}
	for _, sc := range profile.Spec.Enforcement {
		if enforcementEnabled(sc) {
			if _, ok := inPodSidecarTypes[sc.Type]; ok {
				return true
			}
		}
	}
	return false
}

func wireReporterAccess(spec *corev1.PodSpec, profile *scrutineerv1alpha1.RuntimeProfile) {
	if spec == nil || !hasEnabledReportingSidecar(profile) {
		return
	}
	for _, v := range spec.Volumes {
		if v.Name == ReporterTokenVolumeName {
			return
		}
	}
	exp := reporterTokenExpirationSec
	spec.Volumes = append(spec.Volumes, corev1.Volume{
		Name: ReporterTokenVolumeName,
		VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{
				Sources: []corev1.VolumeProjection{{
					ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
						Audience:          ReporterTokenAudience,
						ExpirationSeconds: &exp,
						Path:              ReporterTokenFileName,
					},
				}},
			},
		},
	})
}

func reporterSidecarEnv() []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: EnvScrutineerReporterURL, Value: DefaultReporterURL},
		{Name: EnvScrutineerReporterTokenPath, Value: ReporterTokenMountPath + "/" + ReporterTokenFileName},
	}
}

func reporterVolumeMount() corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      ReporterTokenVolumeName,
		MountPath: ReporterTokenMountPath,
		ReadOnly:  true,
	}
}

func enforcementEnabled(sc scrutineerv1alpha1.RuntimeProfileEnforcement) bool {
	return sc.Enabled == nil || *sc.Enabled
}

func buildSidecarContainer(sc scrutineerv1alpha1.RuntimeProfileEnforcement, session *scrutineerv1alpha1.AgentSession, pol *policy.Resolved, profile *scrutineerv1alpha1.RuntimeProfile) corev1.Container {
	name := strings.TrimSpace(sc.Name)
	if name == "" {
		name = sc.Type
	}

	env := sidecarBaseEnv(sc.Type, session, pol, profile)
	image := placeholderImageForType(sc.Type)

	return corev1.Container{
		Name:            name,
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         sidecarCommand(sc.Type),
		Env:             env,
		VolumeMounts:    sidecarVolumeMounts(profile),
		SecurityContext: defaultContainerSecurityContext(),
	}
}

func sidecarCommand(sidecarType string) []string {
	switch sidecarType {
	case EnforcementTypeDNSProxy, EnforcementTypeToolGateway, EnforcementTypeFSGateway:
		return nil
	default:
		return []string{"sleep", placeholderSidecarSleep}
	}
}

func sidecarVolumeMounts(profile *scrutineerv1alpha1.RuntimeProfile) []corev1.VolumeMount {
	if !hasEnabledReportingSidecar(profile) {
		return nil
	}
	return []corev1.VolumeMount{reporterVolumeMount()}
}

func placeholderImageForType(sidecarType string) string {
	switch sidecarType {
	case EnforcementTypeDNSProxy:
		return dnsproxy.DefaultDNSProxyImage
	case EnforcementTypeToolGateway:
		return toolgateway.DefaultToolGatewayImage
	case EnforcementTypeFSGateway:
		return workspace.DefaultFSGatewayImage
	default:
		return dnsproxy.DefaultDNSProxyImage
	}
}

func sidecarBaseEnv(sidecarType string, session *scrutineerv1alpha1.AgentSession, pol *policy.Resolved, profile *scrutineerv1alpha1.RuntimeProfile) []corev1.EnvVar {
	mode := scrutineerv1alpha1.PolicyModeAuditOnly
	rules := session.Spec.Policy.PolicyRules
	if pol != nil {
		mode = pol.Mode
		rules = pol.Rules
	}
	env := []corev1.EnvVar{
		{Name: EnvScrutineerSessionName, Value: session.Name},
		{Name: EnvScrutineerSessionNamespace, Value: session.Namespace},
		{Name: EnvScrutineerSidecarType, Value: sidecarType},
		{Name: EnvPolicyMode, Value: string(mode)},
	}
	if sidecarType == EnforcementTypeToolGateway {
		env = append(env, corev1.EnvVar{Name: EnvScrutineerToolGatewayURL, Value: toolgateway.DefaultInPodURL})
		ctx := enforcement.NewSessionContext(session, profile, NameFor(session))
		ctx.Mode = mode
		ctx.Policy = rules
		env = append(env, toolgateway.EnvForConfig(toolgateway.BuildConfig(ctx))...)
	}
	if sidecarType == EnforcementTypeDNSProxy {
		ctx := enforcement.NewSessionContext(session, profile, NameFor(session))
		ctx.Mode = mode
		ctx.Policy = rules
		env = append(env, dnsproxy.EnvForConfig(dnsproxy.BuildConfig(ctx))...)
	}
	if sidecarType == EnforcementTypeFSGateway {
		ctx := enforcement.NewSessionContext(session, profile, NameFor(session))
		ctx.Mode = mode
		ctx.Policy = rules
		env = append(env, workspace.EnvForConfig(workspace.BuildConfig(ctx))...)
	}
	if hasEnabledReportingSidecar(profile) {
		env = append(env, reporterSidecarEnv()...)
	}
	return env
}

// applyAgentSidecarEnv adds agent env vars when governance sidecars are enabled.
func applyAgentSidecarEnv(env []corev1.EnvVar, session *scrutineerv1alpha1.AgentSession, profile *scrutineerv1alpha1.RuntimeProfile) []corev1.EnvVar {
	if profile == nil {
		return env
	}
	if hasEnabledEnforcement(profile, EnforcementTypeToolGateway) {
		env = append(env, corev1.EnvVar{Name: EnvScrutineerToolGatewayURL, Value: toolgateway.DefaultInPodURL})
	}
	// Egress proxy routing. The out-of-pod per-session Envoy is the preferred, tamper-
	// resistant chokepoint (evidence-integrity design); when enabled it supersedes the
	// in-pod dns-proxy for HTTP(S)_PROXY so the two never emit conflicting proxy env.
	switch {
	case hasEnabledEnforcement(profile, EnforcementTypeEnvoy):
		env = append(env, envoy.ExplicitProxyEnv(agentEnvoyProxyURL(session))...)
	case hasEnabledEnforcement(profile, EnforcementTypeDNSProxy):
		const noProxy = "localhost,127.0.0.1"
		env = append(env,
			corev1.EnvVar{Name: EnvHTTPProxy, Value: dnsproxy.DefaultHTTPProxyURL},
			corev1.EnvVar{Name: EnvHTTPSProxy, Value: dnsproxy.DefaultHTTPProxyURL},
			corev1.EnvVar{Name: EnvNoProxy, Value: noProxy},
			// Lowercase variants for tools (BusyBox wget, curl, Go) that read them;
			// BusyBox wget reads only the lowercase form, so omitting these lets such
			// agents bypass the dns-proxy entirely.
			corev1.EnvVar{Name: EnvHTTPProxyLower, Value: dnsproxy.DefaultHTTPProxyURL},
			corev1.EnvVar{Name: EnvHTTPSProxyLower, Value: dnsproxy.DefaultHTTPProxyURL},
			corev1.EnvVar{Name: EnvNoProxyLower, Value: noProxy},
		)
	}
	if hasEnabledEnforcement(profile, EnforcementTypeFSGateway) {
		env = append(env, corev1.EnvVar{Name: EnvScrutineerFSGatewayURL, Value: workspace.DefaultInPodURL})
	}
	return env
}

// agentEnvoyProxyURL is the address the agent uses for its per-session Envoy. Once the
// controller has resolved the Envoy Service ClusterIP (status.egressProxyEndpoint), the
// agent targets that IP so it needs no DNS — required under the Slice B (#61) routing lock,
// which denies direct DNS. Before the ClusterIP is known (and on non-enforcing CNIs where
// the DNS name still resolves), fall back to the Service DNS name.
func agentEnvoyProxyURL(session *scrutineerv1alpha1.AgentSession) string {
	if ep := session.Status.EgressProxyEndpoint; ep != "" {
		return ep
	}
	return envoy.ProxyURL(session.Name, session.Namespace)
}

func hasEnabledEnforcement(profile *scrutineerv1alpha1.RuntimeProfile, sidecarType string) bool {
	if profile == nil {
		return false
	}
	for _, sc := range profile.Spec.Enforcement {
		if sc.Type == sidecarType && enforcementEnabled(sc) {
			return true
		}
	}
	return false
}

func sidecarContainers(spec *corev1.PodSpec) []corev1.Container {
	if spec == nil {
		return nil
	}
	out := make([]corev1.Container, 0, len(spec.Containers))
	for _, c := range spec.Containers {
		if c.Name == AgentContainerName {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sidecarContainersEqual(a, b []corev1.Container) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Image != b[i].Image {
			return false
		}
		if !envSlicesEqual(a[i].Env, b[i].Env) {
			return false
		}
		if !volumeMountsEqual(a[i].VolumeMounts, b[i].VolumeMounts) {
			return false
		}
	}
	return true
}

func volumeMountsEqual(a, b []corev1.VolumeMount) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].MountPath != b[i].MountPath || a[i].ReadOnly != b[i].ReadOnly {
			return false
		}
	}
	return true
}

func envSlicesEqual(a, b []corev1.EnvVar) bool {
	if len(a) != len(b) {
		return false
	}
	ma := envVarsToMap(a)
	mb := envVarsToMap(b)
	if len(ma) != len(mb) {
		return false
	}
	for k, v := range ma {
		if mb[k] != v {
			return false
		}
	}
	return true
}
