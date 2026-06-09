/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RuntimeProfileRef references a RuntimeProfile in the same namespace as the AgentSession.
type RuntimeProfileRef struct {
	// Kind is the profile resource kind. Only RuntimeProfile is supported in MVP.
	// +kubebuilder:validation:Enum=RuntimeProfile
	// +kubebuilder:default=RuntimeProfile
	// +optional
	Kind string `json:"kind,omitempty"`

	// Name is the RuntimeProfile resource name in the AgentSession namespace.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// RuntimeProfileSpec defines reusable runtime hardening for AgentSessions.
// Fields are declarative in Phase 2; enforcement backends (sidecars, sandboxes) arrive in Phase 3.
type RuntimeProfileSpec struct {
	// Container holds security settings applied to the agent container.
	// +optional
	Container *RuntimeProfileContainerSpec `json:"container,omitempty"`

	// Pod holds pod-level runtime settings (sandbox class, seccomp).
	// +optional
	Pod *RuntimeProfilePodSpec `json:"pod,omitempty"`

	// Sidecars lists optional data-plane sidecars injected when enabled (dns-proxy, tool-gateway, envoy).
	// Schema only in Phase 2 — the controller does not inject these containers yet.
	// +optional
	Sidecars []RuntimeProfileSidecar `json:"sidecars,omitempty"`
}

// RuntimeProfileContainerSpec mirrors a subset of corev1.SecurityContext for agent containers.
type RuntimeProfileContainerSpec struct {
	// RunAsNonRoot requires the container to run as a non-root UID.
	// +optional
	RunAsNonRoot *bool `json:"runAsNonRoot,omitempty"`

	// ReadOnlyRootFilesystem mounts the container root filesystem read-only.
	// +optional
	ReadOnlyRootFilesystem *bool `json:"readOnlyRootFilesystem,omitempty"`

	// AllowPrivilegeEscalation controls whether a process can gain more privileges than its parent.
	// +optional
	AllowPrivilegeEscalation *bool `json:"allowPrivilegeEscalation,omitempty"`

	// Capabilities adjusts Linux capabilities for the agent container.
	// +optional
	Capabilities *corev1.Capabilities `json:"capabilities,omitempty"`
}

// RuntimeProfilePodSpec holds pod-level runtime settings for sessions referencing this profile.
type RuntimeProfilePodSpec struct {
	// RuntimeClassName selects a RuntimeClass (e.g. gVisor/Kata) when the cluster provides one.
	// Declarative only until sandbox runtimes are enforced in Phase 3.
	// +optional
	RuntimeClassName string `json:"runtimeClassName,omitempty"`

	// SeccompProfile applies a seccomp profile at the pod level.
	// +optional
	SeccompProfile *corev1.SeccompProfile `json:"seccompProfile,omitempty"`
}

// RuntimeProfileSidecar describes a sidecar to inject alongside the agent when enabled.
type RuntimeProfileSidecar struct {
	// Name is a unique identifier for this sidecar within the profile.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Type identifies the sidecar role. Known values: envoy, dns-proxy, tool-gateway.
	// Additional types may be added when enforcement backends ship.
	// +kubebuilder:validation:MinLength=1
	Type string `json:"type"`

	// Enabled gates whether the sidecar should be injected when injection is implemented.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`
}

// MatchedRuntimeProfileRef records the RuntimeProfile applied to a session Job template.
type MatchedRuntimeProfileRef struct {
	// Kind is the profile resource kind.
	Kind string `json:"kind"`

	// Name is the profile resource name.
	Name string `json:"name"`

	// UID is the profile object UID at resolution time.
	// +optional
	UID string `json:"uid,omitempty"`

	// ResourceVersion is the profile resourceVersion at resolution time.
	// +optional
	ResourceVersion string `json:"resourceVersion,omitempty"`

	// Generation is the profile generation at resolution time.
	// +optional
	Generation int64 `json:"generation,omitempty"`
}

// RuntimeProfileStatus defines the observed state of a RuntimeProfile.
type RuntimeProfileStatus struct {
	// ObservedGeneration is reserved for a future RuntimeProfile controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// RuntimeProfile is a reusable runtime hardening profile that AgentSessions can reference.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=rp;runtimeprof
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="RuntimeClass",type=string,JSONPath=`.spec.pod.runtimeClassName`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type RuntimeProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RuntimeProfileSpec   `json:"spec,omitempty"`
	Status RuntimeProfileStatus `json:"status,omitempty"`
}

// RuntimeProfileList contains a list of RuntimeProfile.
//
// +kubebuilder:object:root=true
type RuntimeProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RuntimeProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RuntimeProfile{}, &RuntimeProfileList{})
}
