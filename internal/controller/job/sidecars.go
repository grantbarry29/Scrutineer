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
	EnvRelaySidecarType     = "RELAY_SIDECAR_TYPE"
	EnvRelayToolGatewayURL  = "RELAY_TOOL_GATEWAY_URL"
	EnvHTTPProxy            = "HTTP_PROXY"
	EnvHTTPSProxy           = "HTTPS_PROXY"
	EnvNoProxy              = "NO_PROXY"
	placeholderSidecarSleep = "infinity"
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
		SecurityContext: defaultContainerSecurityContext(),
	}
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
