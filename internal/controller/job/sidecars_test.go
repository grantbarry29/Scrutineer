/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package job

import (
	"net/url"
	"testing"

	"golang.org/x/net/http/httpproxy"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/dnsproxy"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
	"github.com/grantbarry29/scrutineer/internal/enforcement/toolgateway"
	"github.com/grantbarry29/scrutineer/internal/enforcement/workspace"
	"github.com/grantbarry29/scrutineer/internal/policy"
)

func TestInjectSidecars_enabledKnownTypes(t *testing.T) {
	enabled := true
	disabled := false
	session := &scrutineerv1alpha1.AgentSession{
		ObjectMeta: objectMeta("team-a", "demo"),
	}
	profile := &scrutineerv1alpha1.RuntimeProfile{
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Sidecars: []scrutineerv1alpha1.RuntimeProfileSidecar{
				{Name: "egress", Type: SidecarTypeDNSProxy, Enabled: &enabled},
				{Name: "tools", Type: SidecarTypeToolGateway, Enabled: &enabled},
				{Name: "envoy-off", Type: SidecarTypeEnvoy, Enabled: &disabled},
				{Name: "unknown", Type: "custom-proxy", Enabled: &enabled},
			},
		},
	}
	spec := &corev1.PodSpec{
		Containers: []corev1.Container{{Name: AgentContainerName, Image: "agent:latest"}},
	}
	injectSidecars(spec, session, &policy.Resolved{Mode: scrutineerv1alpha1.PolicyModeEnforced}, profile)

	if len(spec.Containers) != 3 {
		t.Fatalf("containers = %d, want agent + 2 sidecars", len(spec.Containers))
	}
	if spec.Containers[1].Name != "egress" || spec.Containers[1].Image != dnsproxy.DefaultDNSProxyImage {
		t.Fatalf("dns sidecar = %+v", spec.Containers[1])
	}
	if spec.Containers[1].Command != nil {
		t.Fatalf("dns sidecar command = %v, want nil (image entrypoint)", spec.Containers[1].Command)
	}
	if spec.Containers[2].Name != "tools" || spec.Containers[2].Image != toolgateway.DefaultToolGatewayImage {
		t.Fatalf("tool sidecar = %+v", spec.Containers[2])
	}
	if spec.Containers[2].Command != nil {
		t.Fatalf("tool sidecar command = %v, want nil (image entrypoint)", spec.Containers[2].Command)
	}
}

func TestBuild_agentDNSProxyEnv(t *testing.T) {
	enabled := true
	session := minimalSession()
	profile := &scrutineerv1alpha1.RuntimeProfile{
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Sidecars: []scrutineerv1alpha1.RuntimeProfileSidecar{{
				Name: "egress", Type: SidecarTypeDNSProxy, Enabled: &enabled,
			}},
		},
	}
	pol := &policy.Resolved{
		Mode:  scrutineerv1alpha1.PolicyModeEnforced,
		Rules: scrutineerv1alpha1.PolicyRules{DeniedDomains: []string{"evil.example"}},
	}
	job := Build(session, &Task{}, pol, profile)

	byName := map[string]corev1.Container{}
	for _, c := range job.Spec.Template.Spec.Containers {
		byName[c.Name] = c
	}
	agentEnv := envVarsToMap(byName[AgentContainerName].Env)
	if agentEnv[EnvHTTPProxy] != dnsproxy.DefaultHTTPProxyURL {
		t.Fatalf("HTTP_PROXY = %q", agentEnv[EnvHTTPProxy])
	}
	// Lowercase variants must also be set, or BusyBox-wget-style agents bypass the proxy.
	if agentEnv[EnvHTTPProxyLower] != dnsproxy.DefaultHTTPProxyURL {
		t.Fatalf("http_proxy = %q", agentEnv[EnvHTTPProxyLower])
	}
	if agentEnv[EnvHTTPSProxyLower] != dnsproxy.DefaultHTTPProxyURL {
		t.Fatalf("https_proxy = %q", agentEnv[EnvHTTPSProxyLower])
	}
	if agentEnv[EnvNoProxyLower] == "" {
		t.Fatalf("no_proxy unset")
	}

	proxyEnv := envVarsToMap(byName["egress"].Env)
	if proxyEnv[dnsproxy.EnvPolicyDeniedDomains] != "evil.example" {
		t.Fatalf("sidecar denied domains = %q", proxyEnv[dnsproxy.EnvPolicyDeniedDomains])
	}
}

// TestBuild_agentEnvRoutesGoAndBusyBoxClients locks in the lowercase-proxy fix:
// it proves that an agent egress client is routed through the dns-proxy
// regardless of which env-var casing it honors. BusyBox wget reads ONLY the
// lowercase names while Go's net/http and curl prefer uppercase; using the same
// httpproxy logic net/http uses, both casing sets must resolve to the proxy and
// must bypass loopback. If a future change drops either casing, this fails.
func TestBuild_agentEnvRoutesGoAndBusyBoxClients(t *testing.T) {
	enabled := true
	session := minimalSession()
	profile := &scrutineerv1alpha1.RuntimeProfile{
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Sidecars: []scrutineerv1alpha1.RuntimeProfileSidecar{{
				Name: "egress", Type: SidecarTypeDNSProxy, Enabled: &enabled,
			}},
		},
	}
	pol := &policy.Resolved{Mode: scrutineerv1alpha1.PolicyModeEnforced}
	job := Build(session, &Task{}, pol, profile)

	var agentEnv map[string]string
	for _, c := range job.Spec.Template.Spec.Containers {
		if c.Name == AgentContainerName {
			agentEnv = envVarsToMap(c.Env)
		}
	}

	wantProxy, err := url.Parse(dnsproxy.DefaultHTTPProxyURL)
	if err != nil {
		t.Fatalf("parse proxy url: %v", err)
	}

	cases := []struct {
		name              string
		http, https, none string
	}{
		{"uppercase (Go net/http, curl)", agentEnv[EnvHTTPProxy], agentEnv[EnvHTTPSProxy], agentEnv[EnvNoProxy]},
		{"lowercase (BusyBox wget)", agentEnv[EnvHTTPProxyLower], agentEnv[EnvHTTPSProxyLower], agentEnv[EnvNoProxyLower]},
	}
	for _, tc := range cases {
		cfg := &httpproxy.Config{HTTPProxy: tc.http, HTTPSProxy: tc.https, NoProxy: tc.none}
		proxyFor := cfg.ProxyFunc()

		for _, target := range []string{"http://evil.example/", "https://evil.example/"} {
			u, _ := url.Parse(target)
			got, err := proxyFor(u)
			if err != nil {
				t.Fatalf("%s: proxy func for %s: %v", tc.name, target, err)
			}
			if got == nil || got.Host != wantProxy.Host {
				t.Fatalf("%s: %s routed to %v, want dns-proxy %s (egress would bypass enforcement)", tc.name, target, got, wantProxy.Host)
			}
		}

		loop, _ := url.Parse("http://127.0.0.1:8088/")
		if got, _ := proxyFor(loop); got != nil {
			t.Fatalf("%s: loopback routed to %v, want direct (no_proxy bypass)", tc.name, got)
		}
	}
}

func TestBuild_reporterWiringForSidecars(t *testing.T) {
	enabled := true
	session := minimalSession()
	profile := &scrutineerv1alpha1.RuntimeProfile{
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Sidecars: []scrutineerv1alpha1.RuntimeProfileSidecar{{
				Name: "egress", Type: SidecarTypeDNSProxy, Enabled: &enabled,
			}},
		},
	}
	job := Build(session, &Task{}, nil, profile)
	spec := job.Spec.Template.Spec

	var tokenVol *corev1.Volume
	for i := range spec.Volumes {
		if spec.Volumes[i].Name == ReporterTokenVolumeName {
			tokenVol = &spec.Volumes[i]
			break
		}
	}
	if tokenVol == nil || tokenVol.Projected == nil || len(tokenVol.Projected.Sources) != 1 {
		t.Fatalf("expected projected reporter token volume, got volumes=%+v", spec.Volumes)
	}
	tok := tokenVol.Projected.Sources[0].ServiceAccountToken
	if tok == nil || tok.Audience != ReporterTokenAudience {
		t.Fatalf("token projection = %+v", tok)
	}

	byName := map[string]corev1.Container{}
	for _, c := range spec.Containers {
		byName[c.Name] = c
	}
	agent := byName[AgentContainerName]
	if len(agent.VolumeMounts) != 0 {
		t.Fatalf("agent should not mount reporter token, got %+v", agent.VolumeMounts)
	}
	for _, key := range []string{EnvScrutineerReporterURL, EnvScrutineerReporterTokenPath} {
		if envVarsToMap(agent.Env)[key] != "" {
			t.Fatalf("agent should not have %s", key)
		}
	}

	sidecar := byName["egress"]
	sidecarEnv := envVarsToMap(sidecar.Env)
	if sidecarEnv[EnvScrutineerReporterURL] != DefaultReporterURL {
		t.Fatalf("SCRUTINEER_REPORTER_URL = %q", sidecarEnv[EnvScrutineerReporterURL])
	}
	wantToken := ReporterTokenMountPath + "/" + ReporterTokenFileName
	if sidecarEnv[EnvScrutineerReporterTokenPath] != wantToken {
		t.Fatalf("SCRUTINEER_REPORTER_TOKEN_PATH = %q", sidecarEnv[EnvScrutineerReporterTokenPath])
	}
	if len(sidecar.VolumeMounts) != 1 || sidecar.VolumeMounts[0].Name != ReporterTokenVolumeName {
		t.Fatalf("sidecar volume mounts = %+v", sidecar.VolumeMounts)
	}
}

func TestBuild_noReporterWiringWithoutSidecars(t *testing.T) {
	job := Build(minimalSession(), &Task{}, nil, nil)
	for _, v := range job.Spec.Template.Spec.Volumes {
		if v.Name == ReporterTokenVolumeName {
			t.Fatal("reporter token volume should not be present without sidecars")
		}
	}
}

func TestBuild_agentToolGatewayEnv(t *testing.T) {
	enabled := true
	session := minimalSession()
	profile := &scrutineerv1alpha1.RuntimeProfile{
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Sidecars: []scrutineerv1alpha1.RuntimeProfileSidecar{{
				Name: "tools", Type: SidecarTypeToolGateway, Enabled: &enabled,
			}},
		},
	}
	pol := &policy.Resolved{
		Mode:  scrutineerv1alpha1.PolicyModeEnforced,
		Rules: scrutineerv1alpha1.PolicyRules{DeniedTools: []string{"kubectl"}},
	}
	job := Build(session, &Task{}, pol, profile)

	byName := map[string]corev1.Container{}
	for _, c := range job.Spec.Template.Spec.Containers {
		byName[c.Name] = c
	}
	agentEnv := envVarsToMap(byName[AgentContainerName].Env)
	if agentEnv[EnvScrutineerToolGatewayURL] != toolgateway.DefaultInPodURL {
		t.Fatalf("agent env = %v", agentEnv[EnvScrutineerToolGatewayURL])
	}

	gwEnv := envVarsToMap(byName["tools"].Env)
	if gwEnv[toolgateway.EnvPolicyDeniedTools] != "kubectl" {
		t.Fatalf("sidecar denied tools = %q", gwEnv[toolgateway.EnvPolicyDeniedTools])
	}
	if gwEnv[toolgateway.EnvPolicyMode] != string(scrutineerv1alpha1.PolicyModeEnforced) {
		t.Fatalf("sidecar mode = %q", gwEnv[toolgateway.EnvPolicyMode])
	}
}

func TestBuild_reporterWiringForToolGateway(t *testing.T) {
	enabled := true
	session := minimalSession()
	profile := &scrutineerv1alpha1.RuntimeProfile{
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Sidecars: []scrutineerv1alpha1.RuntimeProfileSidecar{{
				Name: "tools", Type: SidecarTypeToolGateway, Enabled: &enabled,
			}},
		},
	}
	job := Build(session, &Task{}, nil, profile)

	byName := map[string]corev1.Container{}
	for _, c := range job.Spec.Template.Spec.Containers {
		byName[c.Name] = c
	}
	sidecar := byName["tools"]
	sidecarEnv := envVarsToMap(sidecar.Env)
	if sidecarEnv[EnvScrutineerReporterURL] != DefaultReporterURL {
		t.Fatalf("SCRUTINEER_REPORTER_URL = %q", sidecarEnv[EnvScrutineerReporterURL])
	}
	wantToken := ReporterTokenMountPath + "/" + ReporterTokenFileName
	if sidecarEnv[EnvScrutineerReporterTokenPath] != wantToken {
		t.Fatalf("SCRUTINEER_REPORTER_TOKEN_PATH = %q", sidecarEnv[EnvScrutineerReporterTokenPath])
	}
	if len(sidecar.VolumeMounts) != 1 || sidecar.VolumeMounts[0].Name != ReporterTokenVolumeName {
		t.Fatalf("sidecar volume mounts = %+v", sidecar.VolumeMounts)
	}
}

// The per-session Envoy egress proxy runs OUT of the agent pod (evidence-integrity
// design, #8/#60): enabling it must NOT inject an in-pod container the agent could
// tamper with. The proxy pod is provisioned by the controller; the agent is only
// pointed at it via explicit-proxy env (see TestBuild_agentEnvoyProxyEnv).
func TestInjectSidecars_envoyIsOutOfPod(t *testing.T) {
	enabled := true
	session := minimalSession()
	profile := &scrutineerv1alpha1.RuntimeProfile{
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Sidecars: []scrutineerv1alpha1.RuntimeProfileSidecar{{
				Name: "envoy", Type: SidecarTypeEnvoy, Enabled: &enabled,
			}},
		},
	}
	spec := &corev1.PodSpec{
		Containers: []corev1.Container{{Name: AgentContainerName, Image: "agent:latest"}},
	}
	injectSidecars(spec, session, nil, profile)
	if len(spec.Containers) != 1 {
		t.Fatalf("containers = %d, want agent only (envoy is out-of-pod)", len(spec.Containers))
	}
	// An out-of-pod egress proxy also needs no in-agent-pod reporter token wiring.
	for _, v := range spec.Volumes {
		if v.Name == ReporterTokenVolumeName {
			t.Fatalf("envoy-only profile wired reporter token into the agent pod: %+v", spec.Volumes)
		}
	}
}

// TestBuild_agentEnvoyProxyEnv proves that enabling the out-of-pod Envoy egress proxy
// points the agent at its per-session Envoy Service via explicit-proxy env (both
// casings, loopback bypassed).
func TestBuild_agentEnvoyProxyEnv(t *testing.T) {
	enabled := true
	session := minimalSession()
	profile := &scrutineerv1alpha1.RuntimeProfile{
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Sidecars: []scrutineerv1alpha1.RuntimeProfileSidecar{{
				Name: "envoy", Type: SidecarTypeEnvoy, Enabled: &enabled,
			}},
		},
	}
	job := Build(session, &Task{}, &policy.Resolved{Mode: scrutineerv1alpha1.PolicyModeEnforced}, profile)

	var agentEnv map[string]string
	for _, c := range job.Spec.Template.Spec.Containers {
		if c.Name == AgentContainerName {
			agentEnv = envVarsToMap(c.Env)
		}
	}
	wantURL := envoy.ProxyURL(session.Name, session.Namespace)
	for _, k := range []string{EnvHTTPProxy, EnvHTTPSProxy, EnvHTTPProxyLower, EnvHTTPSProxyLower} {
		if agentEnv[k] != wantURL {
			t.Fatalf("%s = %q, want per-session Envoy %q", k, agentEnv[k], wantURL)
		}
	}
	if agentEnv[EnvNoProxy] == "" || agentEnv[EnvNoProxyLower] == "" {
		t.Fatalf("NO_PROXY unset; loopback would route through Envoy")
	}
}

// Once the controller resolves the Envoy Service ClusterIP (status.egressProxyEndpoint),
// the agent must target that IP — not the DNS name — so it needs no DNS under the Slice B
// routing lock (#61).
func TestBuild_agentEnvoyProxyEnv_prefersClusterIPEndpoint(t *testing.T) {
	enabled := true
	session := minimalSession()
	session.Status.EgressProxyEndpoint = "http://10.96.7.7:15001"
	profile := &scrutineerv1alpha1.RuntimeProfile{
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Sidecars: []scrutineerv1alpha1.RuntimeProfileSidecar{{
				Name: "envoy", Type: SidecarTypeEnvoy, Enabled: &enabled,
			}},
		},
	}
	job := Build(session, &Task{}, &policy.Resolved{Mode: scrutineerv1alpha1.PolicyModeEnforced}, profile)

	var agentEnv map[string]string
	for _, c := range job.Spec.Template.Spec.Containers {
		if c.Name == AgentContainerName {
			agentEnv = envVarsToMap(c.Env)
		}
	}
	for _, k := range []string{EnvHTTPProxy, EnvHTTPSProxy, EnvHTTPProxyLower, EnvHTTPSProxyLower} {
		if agentEnv[k] != "http://10.96.7.7:15001" {
			t.Fatalf("%s = %q, want the resolved ClusterIP endpoint", k, agentEnv[k])
		}
	}
}

func TestBuild_agentFSGatewayEnv(t *testing.T) {
	enabled := true
	session := minimalSession()
	profile := &scrutineerv1alpha1.RuntimeProfile{
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Sidecars: []scrutineerv1alpha1.RuntimeProfileSidecar{{
				Name: "files", Type: SidecarTypeFSGateway, Enabled: &enabled,
			}},
		},
	}
	pol := &policy.Resolved{
		Mode:  scrutineerv1alpha1.PolicyModeEnforced,
		Rules: scrutineerv1alpha1.PolicyRules{DeniedPaths: []string{"/etc/**"}},
	}
	job := Build(session, &Task{}, pol, profile)

	byName := map[string]corev1.Container{}
	for _, c := range job.Spec.Template.Spec.Containers {
		byName[c.Name] = c
	}
	agentEnv := envVarsToMap(byName[AgentContainerName].Env)
	if agentEnv[EnvScrutineerFSGatewayURL] != workspace.DefaultInPodURL {
		t.Fatalf("agent env = %v", agentEnv[EnvScrutineerFSGatewayURL])
	}

	fsEnv := envVarsToMap(byName["files"].Env)
	if fsEnv[workspace.EnvPolicyDeniedPaths] != "/etc/**" {
		t.Fatalf("denied paths = %q", fsEnv[workspace.EnvPolicyDeniedPaths])
	}
	if fsEnv[EnvScrutineerReporterURL] != DefaultReporterURL {
		t.Fatalf("reporter url = %q", fsEnv[EnvScrutineerReporterURL])
	}
	if byName["files"].Image != workspace.DefaultFSGatewayImage {
		t.Fatalf("image = %q", byName["files"].Image)
	}
}

func TestRuntimeProfileDrift_sidecars(t *testing.T) {
	base := Build(minimalSession(), &Task{}, nil, nil)
	withSidecar := Build(minimalSession(), &Task{}, nil, profileWithSidecar(SidecarTypeDNSProxy))
	if !RuntimeProfileDrift(base, withSidecar) {
		t.Fatal("expected drift when sidecars added")
	}
}

func objectMeta(ns, name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Namespace: ns, Name: name}
}

func minimalSession() *scrutineerv1alpha1.AgentSession {
	return &scrutineerv1alpha1.AgentSession{
		ObjectMeta: objectMeta("default", "demo"),
		Spec: scrutineerv1alpha1.AgentSessionSpec{
			Runtime: scrutineerv1alpha1.RuntimeSpec{Image: "busybox:latest", Command: []string{"true"}},
		},
	}
}

func profileWithSidecar(sidecarType string) *scrutineerv1alpha1.RuntimeProfile {
	enabled := true
	return &scrutineerv1alpha1.RuntimeProfile{
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Sidecars: []scrutineerv1alpha1.RuntimeProfileSidecar{{
				Name: sidecarType, Type: sidecarType, Enabled: &enabled,
			}},
		},
	}
}
