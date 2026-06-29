/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package policy

import (
	"time"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// Layer is one policy source merged into the effective result.
type Layer struct {
	Rules scrutineerv1alpha1.PolicyRules
	Mode  scrutineerv1alpha1.PolicyMode
	Match *scrutineerv1alpha1.MatchedPolicyRef
}

// Resolved is the merged policy used when building the runtime Job.
type Resolved struct {
	Rules   scrutineerv1alpha1.PolicyRules
	Mode    scrutineerv1alpha1.PolicyMode
	Matched []scrutineerv1alpha1.MatchedPolicyRef
}

// Resolve merges policy layers in order, then applies inline session overrides last.
func Resolve(layers []Layer, inline scrutineerv1alpha1.PolicyRules) Resolved {
	var (
		rules scrutineerv1alpha1.PolicyRules
		modes []scrutineerv1alpha1.PolicyMode
		match []scrutineerv1alpha1.MatchedPolicyRef
	)
	for _, layer := range layers {
		rules = MergeRules(rules, layer.Rules)
		modes = append(modes, NormalizeMode(layer.Mode))
		if layer.Match != nil {
			match = append(match, *layer.Match)
		}
	}
	rules = MergeRules(rules, inline)
	modes = append(modes, scrutineerv1alpha1.PolicyModeAuditOnly) // inline has no mode yet
	return Resolved{
		Rules:   rules,
		Mode:    StrictestMode(modes...),
		Matched: match,
	}
}

// ApplyStatus writes merged policy and merge-time decisions onto AgentSession status.
func ApplyStatus(session *scrutineerv1alpha1.AgentSession, resolved Resolved) {
	ApplyStatusAt(session, resolved, time.Now())
}

// ApplyStatusAt is like ApplyStatus but accepts a clock for tests.
func ApplyStatusAt(session *scrutineerv1alpha1.AgentSession, resolved Resolved, now time.Time) {
	session.Status.MatchedPolicies = append([]scrutineerv1alpha1.MatchedPolicyRef(nil), resolved.Matched...)
	session.Status.EffectivePolicy = &scrutineerv1alpha1.EffectivePolicyStatus{
		Mode:        resolved.Mode,
		PolicyRules: resolved.Rules,
	}
	session.Status.PolicyDecisions = BuildMergeDecisions(resolved, now)
}
