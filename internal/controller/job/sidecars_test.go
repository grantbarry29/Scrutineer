/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package job

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/enforcement/toolgateway"
	"github.com/secureai/relay/internal/policy"
)

func TestInjectSidecars_enabledKnownTypes(t *testing.T) {
	enabled := true
	disabled := false
	session := &relayv1alpha1.AgentSession{
		ObjectMeta: objectMeta("team-a", "demo"),
	}
	profile := &relayv1alpha1.RuntimeProfile{
		Spec: relayv1alpha1.RuntimeProfileSpec{
			Sidecars: []relayv1alpha1.RuntimeProfileSidecar{
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
	injectSidecars(spec, session, &policy.Resolved{Mode: relayv1alpha1.PolicyModeEnforced}, profile)

	if len(spec.Containers) != 3 {
		t.Fatalf("containers = %d, want agent + 2 sidecars", len(spec.Containers))
	}
	if spec.Containers[1].Name != "egress" || spec.Containers[1].Image != PlaceholderDNSProxyImage {
		t.Fatalf("dns sidecar = %+v", spec.Containers[1])
	}
	if spec.Containers[2].Name != "tools" {
		t.Fatalf("tool sidecar = %+v", spec.Containers[2])
	}
}

func TestBuild_agentToolGatewayEnv(t *testing.T) {
	enabled := true
	session := minimalSession()
	profile := &relayv1alpha1.RuntimeProfile{
		Spec: relayv1alpha1.RuntimeProfileSpec{
			Sidecars: []relayv1alpha1.RuntimeProfileSidecar{{
				Name: "tools", Type: SidecarTypeToolGateway, Enabled: &enabled,
			}},
		},
	}
	job := Build(session, &Task{}, nil, profile)
	agent := job.Spec.Template.Spec.Containers[0]
	env := envVarsToMap(agent.Env)
	if env[EnvRelayToolGatewayURL] != toolgateway.DefaultListenAddr {
		t.Fatalf("agent env = %v", env[EnvRelayToolGatewayURL])
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

func minimalSession() *relayv1alpha1.AgentSession {
	return &relayv1alpha1.AgentSession{
		ObjectMeta: objectMeta("default", "demo"),
		Spec: relayv1alpha1.AgentSessionSpec{
			Runtime: relayv1alpha1.RuntimeSpec{Image: "busybox:latest", Command: []string{"true"}},
		},
	}
}

func profileWithSidecar(sidecarType string) *relayv1alpha1.RuntimeProfile {
	enabled := true
	return &relayv1alpha1.RuntimeProfile{
		Spec: relayv1alpha1.RuntimeProfileSpec{
			Sidecars: []relayv1alpha1.RuntimeProfileSidecar{{
				Name: sidecarType, Type: sidecarType, Enabled: &enabled,
			}},
		},
	}
}
