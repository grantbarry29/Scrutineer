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

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

func TestMergeContainerSecurityContext(t *testing.T) {
	base := defaultContainerSecurityContext()
	runAsNonRoot := true
	profile := &relayv1alpha1.RuntimeProfile{
		Spec: relayv1alpha1.RuntimeProfileSpec{
			Container: &relayv1alpha1.RuntimeProfileContainerSpec{
				RunAsNonRoot: &runAsNonRoot,
			},
		},
	}

	merged := mergeContainerSecurityContext(base, profile)
	if merged.RunAsNonRoot == nil || !*merged.RunAsNonRoot {
		t.Fatalf("expected runAsNonRoot true from profile")
	}
	if merged.Capabilities == nil || len(merged.Capabilities.Drop) == 0 {
		t.Fatalf("expected baseline capability drops to remain")
	}

	if got := mergeContainerSecurityContext(base, nil); got == nil || got.RunAsNonRoot != nil {
		t.Fatalf("expected nil profile to return baseline without profile overrides")
	}
}

func TestApplyRuntimeProfileToPodSpec(t *testing.T) {
	spec := &corev1.PodSpec{}
	profile := &relayv1alpha1.RuntimeProfile{
		Spec: relayv1alpha1.RuntimeProfileSpec{
			Pod: &relayv1alpha1.RuntimeProfilePodSpec{
				RuntimeClassName: "gvisor",
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
			},
		},
	}

	applyRuntimeProfileToPodSpec(spec, profile)
	if spec.RuntimeClassName == nil || *spec.RuntimeClassName != "gvisor" {
		t.Fatalf("runtimeClassName = %v, want gvisor", spec.RuntimeClassName)
	}
	if spec.SecurityContext == nil || spec.SecurityContext.SeccompProfile == nil {
		t.Fatalf("expected seccomp profile on pod spec")
	}
}
