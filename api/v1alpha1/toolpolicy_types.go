/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ToolPolicySpec defines reusable tool/MCP governance rules for AgentSessions.
type ToolPolicySpec struct {
	// Mode controls how tool rules are interpreted once enforcement exists.
	// Defaults to audit-only when unset.
	// +kubebuilder:validation:Enum=audit-only;dry-run;enforced
	// +kubebuilder:default=audit-only
	// +optional
	Mode PolicyMode `json:"mode,omitempty"`

	// AllowedTools lists tool/MCP identifiers the agent may invoke.
	// +optional
	AllowedTools []string `json:"allowedTools,omitempty"`

	// DeniedTools lists tool/MCP identifiers the agent must not invoke.
	// +optional
	DeniedTools []string `json:"deniedTools,omitempty"`

	// MaxToolCalls caps total tool invocations for the session.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxToolCalls *int32 `json:"maxToolCalls,omitempty"`

	// MaxCallsPerMinute caps tool invocations per minute (propagated to effective policy and env; enforcement is Phase 3).
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxCallsPerMinute *int32 `json:"maxCallsPerMinute,omitempty"`
}

// ToolPolicyRules maps a ToolPolicy spec into PolicyRules for merge.
func (s *ToolPolicySpec) ToolPolicyRules() PolicyRules {
	return PolicyRules{
		AllowedTools:      s.AllowedTools,
		DeniedTools:       s.DeniedTools,
		MaxToolCalls:      s.MaxToolCalls,
		MaxCallsPerMinute: s.MaxCallsPerMinute,
	}
}

// ToolPolicyStatus defines the observed state of a ToolPolicy.
type ToolPolicyStatus struct {
	// ObservedGeneration is reserved for a future ToolPolicy controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// ToolPolicy is a reusable tool/MCP policy that AgentSessions can reference.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=tp;toolpol
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.mode`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type ToolPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ToolPolicySpec   `json:"spec,omitempty"`
	Status ToolPolicyStatus `json:"status,omitempty"`
}

// ToolPolicyList contains a list of ToolPolicy.
//
// +kubebuilder:object:root=true
type ToolPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ToolPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ToolPolicy{}, &ToolPolicyList{})
}
