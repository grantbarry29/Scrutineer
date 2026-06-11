/*
Copyright 2026 The Relay Authors.

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

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/enforcement"
	"github.com/secureai/relay/internal/enforcement/dnsproxy"
	"github.com/secureai/relay/internal/enforcement/toolgateway"
	"github.com/secureai/relay/internal/policy"
)

// Known RuntimeProfile sidecar types injected in Phase 3 slice 5.
const (
	SidecarTypeDNSProxy    = "dns-proxy"
	SidecarTypeToolGateway = "tool-gateway"
	SidecarTypeEnvoy       = "envoy"
)

// Placeholder sidecar images until first-party data-plane images ship (slice 7+).
// Each runs `sleep infinity` so envtest and local clusters can schedule pods without custom images.
const (
	PlaceholderDNSProxyImage    = "busybox:latest"
	PlaceholderToolGatewayImage = "busybox:latest"
	PlaceholderEnvoyImage       = "busybox:latest"
)

const (
	EnvRelaySidecarType       = "RELAY_SIDECAR_TYPE"
	EnvRelayToolGatewayURL    = "RELAY_TOOL_GATEWAY_URL"
	EnvRelayReporterURL       = "RELAY_REPORTER_URL"
	EnvRelayReporterTokenPath = "RELAY_REPORTER_TOKEN_PATH"
	EnvHTTPProxy              = "HTTP_PROXY"
	EnvHTTPSProxy             = "HTTPS_PROXY"
	EnvNoProxy                = "NO_PROXY"
	placeholderSidecarSleep   = "infinity"
)

// DefaultReporterURL is the in-cluster base URL for POST /v1/report (no path suffix).
// Matches the relay-controller-reporter Service in config/manager/reporter_service.yaml.
const DefaultReporterURL = "http://relay-controller-reporter.relay-system.svc:8088"

const (
	ReporterTokenVolumeName    = "relay-reporter-token"
	ReporterTokenMountPath     = "/var/run/secrets/relay/reporter-token"
	ReporterTokenFileName      = "token"
	reporterTokenExpirationSec = int64(600)
)

var knownSidecarTypes = map[string]struct{}{
	SidecarTypeDNSProxy:    {},
	SidecarTypeToolGateway: {},
	SidecarTypeEnvoy:       {},
}

// injectSidecars appends enabled, known sidecar containers from a RuntimeProfile.
// Unknown or disabled entries are skipped.
func injectSidecars(spec *corev1.PodSpec, session *relayv1alpha1.AgentSession, pol *policy.Resolved, profile *relayv1alpha1.RuntimeProfile) {
	if spec == nil || profile == nil || len(profile.Spec.Sidecars) == 0 {
		return
	}
	wireReporterAccess(spec, profile)
	for _, sc := range profile.Spec.Sidecars {
		if !sidecarEnabled(sc) {
			continue
		}
		if _, ok := knownSidecarTypes[sc.Type]; !ok {
			continue
		}
		spec.Containers = append(spec.Containers, buildSidecarContainer(sc, session, pol, profile))
	}
}

func hasEnabledReportingSidecar(profile *relayv1alpha1.RuntimeProfile) bool {
	if profile == nil {
		return false
	}
	for _, sc := range profile.Spec.Sidecars {
		if sidecarEnabled(sc) {
			if _, ok := knownSidecarTypes[sc.Type]; ok {
				return true
			}
		}
	}
	return false
}

func wireReporterAccess(spec *corev1.PodSpec, profile *relayv1alpha1.RuntimeProfile) {
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
		{Name: EnvRelayReporterURL, Value: DefaultReporterURL},
		{Name: EnvRelayReporterTokenPath, Value: ReporterTokenMountPath + "/" + ReporterTokenFileName},
	}
}

func reporterVolumeMount() corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      ReporterTokenVolumeName,
		MountPath: ReporterTokenMountPath,
		ReadOnly:  true,
	}
}

func sidecarEnabled(sc relayv1alpha1.RuntimeProfileSidecar) bool {
	return sc.Enabled == nil || *sc.Enabled
}

func buildSidecarContainer(sc relayv1alpha1.RuntimeProfileSidecar, session *relayv1alpha1.AgentSession, pol *policy.Resolved, profile *relayv1alpha1.RuntimeProfile) corev1.Container {
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
		Command:         []string{"sleep", placeholderSidecarSleep},
		Env:             env,
		VolumeMounts:    sidecarVolumeMounts(profile),
		SecurityContext: defaultContainerSecurityContext(),
	}
}

func sidecarVolumeMounts(profile *relayv1alpha1.RuntimeProfile) []corev1.VolumeMount {
	if !hasEnabledReportingSidecar(profile) {
		return nil
	}
	return []corev1.VolumeMount{reporterVolumeMount()}
}

func placeholderImageForType(sidecarType string) string {
	switch sidecarType {
	case SidecarTypeDNSProxy:
		return PlaceholderDNSProxyImage
	case SidecarTypeToolGateway:
		return PlaceholderToolGatewayImage
	case SidecarTypeEnvoy:
		return PlaceholderEnvoyImage
	default:
		return PlaceholderDNSProxyImage
	}
}

func sidecarBaseEnv(sidecarType string, session *relayv1alpha1.AgentSession, pol *policy.Resolved, profile *relayv1alpha1.RuntimeProfile) []corev1.EnvVar {
	mode := relayv1alpha1.PolicyModeAuditOnly
	rules := session.Spec.Policy.PolicyRules
	if pol != nil {
		mode = pol.Mode
		rules = pol.Rules
	}
	env := []corev1.EnvVar{
		{Name: EnvRelaySessionName, Value: session.Name},
		{Name: EnvRelaySessionNamespace, Value: session.Namespace},
		{Name: EnvRelaySidecarType, Value: sidecarType},
		{Name: EnvPolicyMode, Value: string(mode)},
	}
	if sidecarType == SidecarTypeToolGateway {
		env = append(env, corev1.EnvVar{Name: EnvRelayToolGatewayURL, Value: toolgateway.DefaultListenAddr})
	}
	if sidecarType == SidecarTypeDNSProxy {
		ctx := enforcement.NewSessionContext(session, profile, NameFor(session))
		ctx.Mode = mode
		ctx.Policy = rules
		env = append(env, dnsproxy.EnvForConfig(dnsproxy.BuildConfig(ctx))...)
	}
	if hasEnabledReportingSidecar(profile) {
		env = append(env, reporterSidecarEnv()...)
	}
	return env
}

// applyAgentSidecarEnv adds agent env vars when governance sidecars are enabled.
func applyAgentSidecarEnv(env []corev1.EnvVar, profile *relayv1alpha1.RuntimeProfile) []corev1.EnvVar {
	if profile == nil {
		return env
	}
	if hasEnabledSidecar(profile, SidecarTypeToolGateway) {
		env = append(env, corev1.EnvVar{Name: EnvRelayToolGatewayURL, Value: toolgateway.DefaultListenAddr})
	}
	if hasEnabledSidecar(profile, SidecarTypeDNSProxy) {
		env = append(env,
			corev1.EnvVar{Name: EnvHTTPProxy, Value: dnsproxy.DefaultHTTPProxyURL},
			corev1.EnvVar{Name: EnvHTTPSProxy, Value: dnsproxy.DefaultHTTPProxyURL},
			corev1.EnvVar{Name: EnvNoProxy, Value: "localhost,127.0.0.1"},
		)
	}
	return env
}

func hasEnabledSidecar(profile *relayv1alpha1.RuntimeProfile, sidecarType string) bool {
	if profile == nil {
		return false
	}
	for _, sc := range profile.Spec.Sidecars {
		if sc.Type == sidecarType && sidecarEnabled(sc) {
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
