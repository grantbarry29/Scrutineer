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

	// ArgumentRules constrain tool calls by their arguments (e.g. restrict read_file to
	// /workspace), applied only after name-level allow/deny. Declared policy only: no
	// enforcement backend until the out-of-pod tools chokepoint lands. See
	// docs/design/tools-pod-chokepoint.md.
	// +optional
	ArgumentRules []ToolArgumentRule `json:"argumentRules,omitempty"`
}

// ArgumentOperator is the comparison applied to a tool-argument value.
//
// +kubebuilder:validation:Enum=Equals;NotEquals;In;NotIn;Matches;NotMatches;HasPrefix;NotHasPrefix;Exists;NotExists
type ArgumentOperator string

const (
	ArgOpEquals       ArgumentOperator = "Equals"
	ArgOpNotEquals    ArgumentOperator = "NotEquals"
	ArgOpIn           ArgumentOperator = "In"
	ArgOpNotIn        ArgumentOperator = "NotIn"
	ArgOpMatches      ArgumentOperator = "Matches"
	ArgOpNotMatches   ArgumentOperator = "NotMatches"
	ArgOpHasPrefix    ArgumentOperator = "HasPrefix"
	ArgOpNotHasPrefix ArgumentOperator = "NotHasPrefix"
	ArgOpExists       ArgumentOperator = "Exists"
	ArgOpNotExists    ArgumentOperator = "NotExists"
)

// ConstraintEffect declares what a constraint match means.
//
// +kubebuilder:validation:Enum=Allow;Deny
type ConstraintEffect string

const (
	// ConstraintEffectDeny (default): a match blocks the call.
	ConstraintEffectDeny ConstraintEffect = "Deny"
	// ConstraintEffectAllow: the constraint is an allowlist gate — the call is permitted
	// only if it matches.
	ConstraintEffectAllow ConstraintEffect = "Allow"
)

// ArgumentConstraint matches one tool-argument value and declares whether a match allows
// or denies the call. v1 uses structured matchers (not an expression language). See
// docs/design/phase-3-tool-argument-constraints.md.
type ArgumentConstraint struct {
	// Arg is a dotted path into the tool's argument object (e.g. "path", "args[0]",
	// "options.recursive").
	// +kubebuilder:validation:MinLength=1
	Arg string `json:"arg"`

	// Op is the comparison operator.
	Op ArgumentOperator `json:"op"`

	// Values holds the operands: regular expressions for Matches/NotMatches, literals
	// otherwise. Ignored for Exists/NotExists.
	// +optional
	Values []string `json:"values,omitempty"`

	// Effect declares what a match means. Deny (default) blocks the call; Allow makes the
	// constraint an allowlist gate (call permitted only if it matches).
	// +kubebuilder:default=Deny
	// +optional
	Effect ConstraintEffect `json:"effect,omitempty"`
}

// ToolArgumentRule constrains the arguments of matching tool calls. It applies only to
// calls that already passed name-level allow/deny. This models declared policy; the
// future out-of-pod tools chokepoint enforces it.
type ToolArgumentRule struct {
	// Tools lists tool identifiers this rule applies to. "*" matches any tool.
	// +kubebuilder:validation:MinItems=1
	Tools []string `json:"tools"`

	// Server optionally scopes the rule to a single MCP server / provider id.
	// +optional
	Server string `json:"server,omitempty"`

	// Constraints are ANDed: every constraint must pass for the call to be allowed by
	// this rule.
	// +kubebuilder:validation:MinItems=1
	Constraints []ArgumentConstraint `json:"constraints"`
}

// ToolPolicyRules maps a ToolPolicy spec into PolicyRules for merge.
func (s *ToolPolicySpec) ToolPolicyRules() PolicyRules {
	return PolicyRules{
		AllowedTools:      s.AllowedTools,
		DeniedTools:       s.DeniedTools,
		MaxToolCalls:      s.MaxToolCalls,
		MaxCallsPerMinute: s.MaxCallsPerMinute,
		ArgumentRules:     s.ArgumentRules,
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
