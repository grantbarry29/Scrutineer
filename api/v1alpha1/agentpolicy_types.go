/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentPolicySpec defines reusable governance rules for AgentSessions.
type AgentPolicySpec struct {
	// Mode controls how rules are interpreted once enforcement exists.
	// Defaults to audit-only when unset.
	// +kubebuilder:validation:Enum=audit-only;dry-run;enforced
	// +kubebuilder:default=audit-only
	// +optional
	Mode PolicyMode `json:"mode,omitempty"`

	// Network, tool, and approval rules applied to referencing sessions.
	PolicyRules `json:",inline"`
}

// AgentPolicyStatus defines the observed state of an AgentPolicy.
type AgentPolicyStatus struct {
	// ObservedGeneration is the last spec generation observed by a controller.
	// Reserved for a future AgentPolicy controller; unused in Phase 2 slice.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// AgentPolicy is a reusable governance policy that AgentSessions can reference.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=ap;agentpol
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.mode`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type AgentPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentPolicySpec   `json:"spec,omitempty"`
	Status AgentPolicyStatus `json:"status,omitempty"`
}

// AgentPolicyList contains a list of AgentPolicy.
//
// +kubebuilder:object:root=true
type AgentPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentPolicy{}, &AgentPolicyList{})
}
