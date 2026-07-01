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
)

func TestConfigMapCarriesBootstrap(t *testing.T) {
	cm := ConfigMap("sess-a", "ns1")
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
	pod := Pod("sess-a", "ns1", "sa", "img")

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
	pod := Pod("sess-a", "ns1", "scrutineer-egress", "envoy:img")

	if pod.Name != ResourceName("sess-a") {
		t.Fatalf("pod name = %q", pod.Name)
	}
	if pod.Spec.ServiceAccountName != "scrutineer-egress" {
		t.Fatalf("SA = %q, want its own identity", pod.Spec.ServiceAccountName)
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Fatal("automountServiceAccountToken must be false in Slice A")
	}
	if len(pod.Spec.Containers) != 1 || pod.Spec.Containers[0].Image != "envoy:img" {
		t.Fatalf("containers = %+v", pod.Spec.Containers)
	}

	sc := pod.Spec.Containers[0].SecurityContext
	if sc == nil {
		t.Fatal("container securityContext must be set")
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Fatal("allowPrivilegeEscalation must be false")
	}
	if sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
		t.Fatal("readOnlyRootFilesystem must be true")
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Fatal("runAsNonRoot must be true")
	}
	if sc.Capabilities == nil || len(sc.Capabilities.Drop) == 0 || sc.Capabilities.Drop[0] != "ALL" {
		t.Fatalf("capabilities must drop ALL, got %+v", sc.Capabilities)
	}
}
