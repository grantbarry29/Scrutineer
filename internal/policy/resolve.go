/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package policy

import (
	"time"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

// Layer is one policy source merged into the effective result.
type Layer struct {
	Rules relayv1alpha1.PolicyRules
	Mode  relayv1alpha1.PolicyMode
	Match *relayv1alpha1.MatchedPolicyRef
}

// Resolved is the merged policy used when building the runtime Job.
type Resolved struct {
	Rules   relayv1alpha1.PolicyRules
	Mode    relayv1alpha1.PolicyMode
	Matched []relayv1alpha1.MatchedPolicyRef
}

// Resolve merges policy layers in order, then applies inline session overrides last.
func Resolve(layers []Layer, inline relayv1alpha1.PolicyRules) Resolved {
	var (
		rules relayv1alpha1.PolicyRules
		modes []relayv1alpha1.PolicyMode
		match []relayv1alpha1.MatchedPolicyRef
	)
	for _, layer := range layers {
		rules = MergeRules(rules, layer.Rules)
		modes = append(modes, NormalizeMode(layer.Mode))
		if layer.Match != nil {
			match = append(match, *layer.Match)
		}
	}
	rules = MergeRules(rules, inline)
	modes = append(modes, relayv1alpha1.PolicyModeAuditOnly) // inline has no mode yet
	return Resolved{
		Rules:   rules,
		Mode:    StrictestMode(modes...),
		Matched: match,
	}
}

// ApplyStatus writes merged policy and merge-time decisions onto AgentSession status.
func ApplyStatus(session *relayv1alpha1.AgentSession, resolved Resolved) {
	ApplyStatusAt(session, resolved, time.Now())
}

// ApplyStatusAt is like ApplyStatus but accepts a clock for tests.
func ApplyStatusAt(session *relayv1alpha1.AgentSession, resolved Resolved, now time.Time) {
	session.Status.MatchedPolicies = append([]relayv1alpha1.MatchedPolicyRef(nil), resolved.Matched...)
	session.Status.EffectivePolicy = &relayv1alpha1.EffectivePolicyStatus{
		Mode:        resolved.Mode,
		PolicyRules: resolved.Rules,
	}
	session.Status.PolicyDecisions = BuildMergeDecisions(resolved, now)
}
