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

	corev1 "k8s.io/api/core/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
)

// EnforcementTypeEnvoy is the only RuntimeProfile spec.enforcement type: the per-session
// Envoy egress proxy, provisioned by the controller as a separate out-of-pod pod (own
// identity/netns) so a compromised agent cannot tamper with the enforcement point it
// would otherwise share a pod with (evidence-integrity design, #8/#60; the untamperable
// pivot, docs/design/untamperable-pivot.md). The cooperative in-pod sidecar tier was
// removed in the pivot (#71): a control the agent could bypass or starve is advisory,
// not enforcement. Enabling this type provisions the proxy
// and points the agent at it via explicit-proxy env; see
// internal/controller/agentsession/egress_envoy.go.
const EnforcementTypeEnvoy = "envoy"

// DefaultReporterURL is the in-cluster base URL for POST /v1/report (no path suffix).
// Matches the scrutineer-controller-reporter Service in config/manager/reporter_service.yaml.
// The per-session Envoy egress-reporter (out-of-pod) reports observed egress evidence here.
const DefaultReporterURL = "http://scrutineer-controller-reporter.scrutineer-system.svc:8088"

// applyAgentSidecarEnv adds the explicit-proxy env that routes agent egress through the
// per-session Envoy when the profile enables it. This only routes well-behaved traffic;
// the mandatory, non-bypassable enforcement is the default-deny egress NetworkPolicy
// (verified by the lock gate, #70), which makes Envoy the sole reachable egress.
func applyAgentSidecarEnv(env []corev1.EnvVar, session *scrutineerv1alpha1.AgentSession, profile *scrutineerv1alpha1.RuntimeProfile) []corev1.EnvVar {
	if profile == nil {
		return env
	}
	if hasEnabledEnforcement(profile, EnforcementTypeEnvoy) {
		env = append(env, envoy.ExplicitProxyEnv(agentEnvoyProxyURL(session))...)
	}
	return env
}

// agentEnvoyProxyURL is the address the agent uses for its per-session Envoy. Once the
// controller has resolved the Envoy Service ClusterIP (status.egressProxyEndpoint), the
// agent targets that IP so it needs no DNS — required under the routing lock (#61), which
// denies direct DNS. Before the ClusterIP is known, fall back to the Service DNS name.
func agentEnvoyProxyURL(session *scrutineerv1alpha1.AgentSession) string {
	if ep := session.Status.EgressProxyEndpoint; ep != "" {
		return ep
	}
	return envoy.ProxyURL(session.Name, session.Namespace)
}

func enforcementEnabled(sc scrutineerv1alpha1.RuntimeProfileEnforcement) bool {
	return sc.Enabled == nil || *sc.Enabled
}

func hasEnabledEnforcement(profile *scrutineerv1alpha1.RuntimeProfile, enforcementType string) bool {
	if profile == nil {
		return false
	}
	for _, sc := range profile.Spec.Enforcement {
		if sc.Type == enforcementType && enforcementEnabled(sc) {
			return true
		}
	}
	return false
}

// sidecarContainers returns the pod's non-agent containers (sorted by name) for Job drift
// detection. Post-pivot the agent pod carries no enforcement sidecars, but the helper
// stays general so drift comparison keeps working for any future in-pod additions.
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
