/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package policy

import (
	"encoding/json"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	merge := BuildMergeDecisions(resolved, now)
	preserveMergeDecisionTimes(merge, session.Status.PolicyDecisions)
	session.Status.PolicyDecisions = merge
}

// preserveMergeDecisionTimes keeps the earliest previously-recorded timestamp on each
// fresh merge-phase decision whose content (everything except Time) already appears in
// prior status. A decision's time is when it was first made: rebuilding an identical
// decision on a later reconcile is not a re-decision, and re-stamping it makes the
// audit chronology claim the policy was resolved after the runtime evidence it
// authorized (#154). Decisions absent from prior — a genuine policy change — keep
// their fresh stamp.
func preserveMergeDecisionTimes(fresh, prior []scrutineerv1alpha1.PolicyDecision) {
	if len(fresh) == 0 || len(prior) == 0 {
		return
	}
	firstSeen := make(map[string]metav1.Time, len(prior))
	for _, d := range prior {
		if d.Phase != scrutineerv1alpha1.PolicyDecisionPhaseMerge {
			continue
		}
		k := decisionContentKey(d)
		if t, ok := firstSeen[k]; !ok || d.Time.Before(&t) {
			firstSeen[k] = d.Time
		}
	}
	for i := range fresh {
		if t, ok := firstSeen[decisionContentKey(fresh[i])]; ok {
			fresh[i].Time = t
		}
	}
}

// decisionContentKey identifies a decision by every field except Time. (The
// controller's policyDecisionKey includes Time — exactly what must be ignored here.)
func decisionContentKey(d scrutineerv1alpha1.PolicyDecision) string {
	d.Time = metav1.Time{}
	b, _ := json.Marshal(d) // plain strings and value fields; cannot fail
	return string(b)
}
