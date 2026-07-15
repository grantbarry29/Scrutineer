/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package job

import (
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
	"github.com/grantbarry29/scrutineer/internal/policy"
)

func envoyProfile() *scrutineerv1alpha1.RuntimeProfile {
	enabled := true
	return &scrutineerv1alpha1.RuntimeProfile{
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Enforcement: []scrutineerv1alpha1.RuntimeProfileEnforcement{{
				Name: "envoy", Type: EnforcementTypeEnvoy, Enabled: &enabled,
			}},
		},
	}
}

// The Envoy egress proxy is out-of-pod: enabling it adds no in-agent-pod container and no
// reporter-token volume (the agent pod carries no enforcement sidecars since #71).
func TestBuildPodTemplate_envoyIsOutOfPod(t *testing.T) {
	job := Build(minimalSession(), &Task{}, nil, envoyProfile())
	containers := job.Spec.Template.Spec.Containers
	if len(containers) != 1 || containers[0].Name != AgentContainerName {
		t.Fatalf("containers = %+v, want agent only (envoy is out-of-pod)", containers)
	}
	if len(sidecarContainers(&job.Spec.Template.Spec)) != 0 {
		t.Fatalf("expected no in-pod sidecars, got %+v", sidecarContainers(&job.Spec.Template.Spec))
	}
}

// TestBuild_agentEnvoyProxyEnv proves that enabling the out-of-pod Envoy egress proxy
// points the agent at its per-session Envoy Service via explicit-proxy env (both
// casings, loopback bypassed).
func TestBuild_agentEnvoyProxyEnv(t *testing.T) {
	session := minimalSession()
	job := Build(session, &Task{}, &policy.Resolved{Mode: scrutineerv1alpha1.PolicyModeEnforced}, envoyProfile())

	agentEnv := agentEnvOf(job)
	wantURL := envoy.ProxyURL(session.Name, session.Namespace)
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		if agentEnv[k] != wantURL {
			t.Fatalf("%s = %q, want per-session Envoy %q", k, agentEnv[k], wantURL)
		}
	}
	if agentEnv["NO_PROXY"] == "" || agentEnv["no_proxy"] == "" {
		t.Fatalf("NO_PROXY unset; loopback would route through Envoy")
	}
}

// Once the controller resolves the Envoy Service ClusterIP (status.egressProxyEndpoint),
// the agent must target that IP — not the DNS name — so it needs no DNS under the routing
// lock (#61).
func TestBuild_agentEnvoyProxyEnv_prefersClusterIPEndpoint(t *testing.T) {
	session := minimalSession()
	session.Status.EgressProxyEndpoint = "http://10.96.7.7:15001"
	job := Build(session, &Task{}, &policy.Resolved{Mode: scrutineerv1alpha1.PolicyModeEnforced}, envoyProfile())

	agentEnv := agentEnvOf(job)
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		if agentEnv[k] != "http://10.96.7.7:15001" {
			t.Fatalf("%s = %q, want the resolved ClusterIP endpoint", k, agentEnv[k])
		}
	}
}

func agentEnvOf(job *batchv1.Job) map[string]string {
	for _, c := range job.Spec.Template.Spec.Containers {
		if c.Name == AgentContainerName {
			return envVarsToMap(c.Env)
		}
	}
	return nil
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
