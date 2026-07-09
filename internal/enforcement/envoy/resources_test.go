/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package envoy

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/grantbarry29/scrutineer/internal/enforcement/sidecarenv"
)

func testPodConfig() PodConfig {
	return PodConfig{
		ServiceAccount:   "scrutineer-egress",
		Image:            "envoy:img",
		ReporterImage:    "egress-reporter:img",
		ReporterURL:      "http://reporter.scrutineer-system.svc:8088",
		ReporterAudience: "scrutineer-reporter",
	}
}

func TestServiceAccountIsPerSession(t *testing.T) {
	sa := ServiceAccount("sess-a", "ns1")
	if sa.Name != ResourceName("sess-a") || sa.Namespace != "ns1" {
		t.Fatalf("meta = %s/%s", sa.Namespace, sa.Name)
	}
	// The egress proxy Pod must run under this dedicated identity, not the
	// namespace default, so evidence (Slice C) is attributable to the proxy.
	cfg := testPodConfig()
	cfg.ServiceAccount = sa.Name
	pod := Pod("sess-a", "ns1", cfg)
	if pod.Spec.ServiceAccountName != sa.Name {
		t.Fatalf("pod SA = %q, want %q", pod.Spec.ServiceAccountName, sa.Name)
	}
	for k, v := range Labels("sess-a") {
		if sa.Labels[k] != v {
			t.Fatalf("sa label %s=%q, want %q", k, sa.Labels[k], v)
		}
	}
}

func TestConfigMapCarriesBootstrap(t *testing.T) {
	cm := ConfigMap("sess-a", "ns1", BootstrapConfig{})
	if cm.Name != ResourceName("sess-a") || cm.Namespace != "ns1" {
		t.Fatalf("meta = %s/%s", cm.Namespace, cm.Name)
	}
	got := cm.Data[configFileName]
	if !strings.Contains(got, "dynamic_forward_proxy") {
		t.Fatalf("configmap missing bootstrap; data=%q", got)
	}
}

func TestServiceSelectsThePod(t *testing.T) {
	svc := Service("sess-a", "ns1")
	pod := Pod("sess-a", "ns1", testPodConfig())

	// The Service must select exactly this session's Envoy pod.
	for k, v := range svc.Spec.Selector {
		if pod.Labels[k] != v {
			t.Fatalf("selector %s=%s does not match pod label %q", k, v, pod.Labels[k])
		}
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != ProxyPort {
		t.Fatalf("service port = %+v, want %d", svc.Spec.Ports, ProxyPort)
	}
}

func TestPodIsSeparateAndUnprivileged(t *testing.T) {
	pod := Pod("sess-a", "ns1", testPodConfig())

	if pod.Name != ResourceName("sess-a") {
		t.Fatalf("pod name = %q", pod.Name)
	}
	if pod.Spec.ServiceAccountName != "scrutineer-egress" {
		t.Fatalf("SA = %q, want its own identity", pod.Spec.ServiceAccountName)
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Fatal("automountServiceAccountToken must stay false (the reporter token is an explicit projected volume)")
	}

	// Every container in the proxy pod is hardened: drop ALL, no privilege
	// escalation, read-only rootfs, non-root.
	if len(pod.Spec.Containers) == 0 {
		t.Fatal("no containers")
	}
	for _, c := range pod.Spec.Containers {
		sc := c.SecurityContext
		if sc == nil {
			t.Fatalf("%s: container securityContext must be set", c.Name)
		}
		if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
			t.Fatalf("%s: allowPrivilegeEscalation must be false", c.Name)
		}
		if sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
			t.Fatalf("%s: readOnlyRootFilesystem must be true", c.Name)
		}
		if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
			t.Fatalf("%s: runAsNonRoot must be true", c.Name)
		}
		if sc.Capabilities == nil || len(sc.Capabilities.Drop) == 0 || sc.Capabilities.Drop[0] != "ALL" {
			t.Fatalf("%s: capabilities must drop ALL, got %+v", c.Name, sc.Capabilities)
		}
		if c.Resources.Limits == nil {
			t.Fatalf("%s: resource limits must be set", c.Name)
		}
	}
}

// Both containers declare their metrics ports (#55): Envoy's stats listener and the
// egress-reporter's /metrics endpoint, named so scrape configs can target them.
func TestPodExposesMetricsPorts(t *testing.T) {
	pod := Pod("sess-a", "ns1", testPodConfig())
	byName := map[string]corev1.Container{}
	for _, c := range pod.Spec.Containers {
		byName[c.Name] = c
	}

	hasPort := func(c corev1.Container, name string, port int32) bool {
		for _, p := range c.Ports {
			if p.Name == name && p.ContainerPort == port {
				return true
			}
		}
		return false
	}
	if !hasPort(byName[containerName], "envoy-stats", StatsPort) {
		t.Fatalf("envoy container must declare the stats port: %+v", byName[containerName].Ports)
	}
	if !hasPort(byName[reporterContainerName], "metrics", ReporterMetricsPort) {
		t.Fatalf("egress-reporter container must declare its metrics port: %+v", byName[reporterContainerName].Ports)
	}
}

// #98: the controller can override the reporter's rotation threshold per pod; zero
// keeps the reporter's built-in default (no env set).
func TestPodPassesRotateThresholdEnv(t *testing.T) {
	cfg := testPodConfig()
	cfg.RotateAfterBytes = 12345
	rep := findContainer(t, Pod("s", "ns", cfg), reporterContainerName)
	found := ""
	for _, e := range rep.Env {
		if e.Name == sidecarenv.EnvRotateAfterBytes {
			found = e.Value
		}
	}
	if found != "12345" {
		t.Fatalf("%s = %q, want 12345", sidecarenv.EnvRotateAfterBytes, found)
	}

	cfg.RotateAfterBytes = 0
	rep = findContainer(t, Pod("s", "ns", cfg), reporterContainerName)
	for _, e := range rep.Env {
		if e.Name == sidecarenv.EnvRotateAfterBytes {
			t.Fatalf("zero threshold must not set %s (reporter default applies)", sidecarenv.EnvRotateAfterBytes)
		}
	}
}

func findContainer(t *testing.T, pod *corev1.Pod, name string) corev1.Container {
	t.Helper()
	for _, c := range pod.Spec.Containers {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("missing container %q", name)
	return corev1.Container{}
}

// TestPodWiresEgressReporter locks in the Slice C evidence plumbing: Envoy writes the
// JSON access log into a shared volume; the egress-reporter container tails it and
// authenticates to the reporter with the pod's projected per-session SA token.
func TestPodWiresEgressReporter(t *testing.T) {
	cfg := testPodConfig()
	pod := Pod("sess-a", "ns1", cfg)

	if len(pod.Spec.Containers) != 2 {
		t.Fatalf("containers = %d, want envoy + egress-reporter", len(pod.Spec.Containers))
	}
	byName := map[string]corev1.Container{}
	for _, c := range pod.Spec.Containers {
		byName[c.Name] = c
	}
	envoyC, ok := byName[containerName]
	if !ok {
		t.Fatalf("missing envoy container: %v", pod.Spec.Containers)
	}
	rep, ok := byName[reporterContainerName]
	if !ok {
		t.Fatalf("missing egress-reporter container: %v", pod.Spec.Containers)
	}
	if rep.Image != cfg.ReporterImage {
		t.Fatalf("reporter image = %q", rep.Image)
	}

	// Envoy writes the access log; the reporter tails it AND rotates the ingested
	// prefix away (#98), so both mounts are writable.
	if !hasMount(envoyC, AccessLogVolumeName, AccessLogDir, false) {
		t.Fatalf("envoy must mount the access-log volume writable: %+v", envoyC.VolumeMounts)
	}
	if !hasMount(rep, AccessLogVolumeName, AccessLogDir, false) {
		t.Fatalf("egress-reporter must mount the access-log volume writable (rotation renames/deletes): %+v", rep.VolumeMounts)
	}
	// Only the reporter container gets the identity token.
	if !hasMount(rep, reporterTokenVolume, reporterTokenMountPath, true) {
		t.Fatalf("egress-reporter must mount the reporter token: %+v", rep.VolumeMounts)
	}
	if hasMountName(envoyC, reporterTokenVolume) {
		t.Fatal("the envoy container must not mount the reporter token")
	}

	env := map[string]string{}
	for _, e := range rep.Env {
		env[e.Name] = e.Value
	}
	if env[sidecarenv.EnvSessionName] != "sess-a" || env[sidecarenv.EnvSessionNamespace] != "ns1" {
		t.Fatalf("session env = %v", env)
	}
	if env[sidecarenv.EnvReporterURL] != cfg.ReporterURL {
		t.Fatalf("reporter URL env = %q", env[sidecarenv.EnvReporterURL])
	}
	if env[sidecarenv.EnvReporterToken] != reporterTokenMountPath+"/"+reporterTokenFileName {
		t.Fatalf("token path env = %q", env[sidecarenv.EnvReporterToken])
	}

	// The token volume is projected with the reporter audience — the identity the
	// reporter requires before stamping evidence observed.
	var tokenVol *corev1.Volume
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == reporterTokenVolume {
			tokenVol = &pod.Spec.Volumes[i]
		}
	}
	if tokenVol == nil || tokenVol.Projected == nil || len(tokenVol.Projected.Sources) != 1 {
		t.Fatalf("token volume = %+v", tokenVol)
	}
	sat := tokenVol.Projected.Sources[0].ServiceAccountToken
	if sat == nil || sat.Audience != cfg.ReporterAudience {
		t.Fatalf("token projection = %+v", sat)
	}

	// The access-log emptyDir is bounded so a chatty session cannot grow node disk
	// unboundedly (overflow evicts the pod → routing lock fails closed).
	var logVol *corev1.Volume
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == AccessLogVolumeName {
			logVol = &pod.Spec.Volumes[i]
		}
	}
	if logVol == nil || logVol.EmptyDir == nil || logVol.EmptyDir.SizeLimit == nil {
		t.Fatalf("access-log volume must be a size-bounded emptyDir: %+v", logVol)
	}
}

// Without a reporter image the pod is a pure proxy (no evidence container) — used by
// tests and staged rollouts; the controller always supplies one in production.
func TestPodWithoutReporterImage(t *testing.T) {
	cfg := testPodConfig()
	cfg.ReporterImage = ""
	pod := Pod("sess-a", "ns1", cfg)
	if len(pod.Spec.Containers) != 1 || pod.Spec.Containers[0].Name != containerName {
		t.Fatalf("containers = %+v", pod.Spec.Containers)
	}
	for _, v := range pod.Spec.Volumes {
		if v.Name == reporterTokenVolume {
			t.Fatal("token volume must not be added without the reporter container")
		}
	}
}

func hasMount(c corev1.Container, name, path string, readOnly bool) bool {
	for _, m := range c.VolumeMounts {
		if m.Name == name && m.MountPath == path && m.ReadOnly == readOnly {
			return true
		}
	}
	return false
}

func hasMountName(c corev1.Container, name string) bool {
	for _, m := range c.VolumeMounts {
		if m.Name == name {
			return true
		}
	}
	return false
}
