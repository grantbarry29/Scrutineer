/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package policy

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

// LoadAgentPolicyLayers fetches AgentPolicy refs for a session in declaration order.
func LoadAgentPolicyLayers(ctx context.Context, c client.Reader, session *relayv1alpha1.AgentSession) ([]Layer, error) {
	var layers []Layer
	for i, ref := range session.Spec.PolicyRefs {
		if err := validatePolicyRef(ref, i); err != nil {
			return nil, err
		}
		switch ref.Kind {
		case "AgentPolicy", "":
			layer, err := loadAgentPolicy(ctx, c, session.Namespace, ref.Name)
			if err != nil {
				return nil, err
			}
			layers = append(layers, layer)
		default:
			return nil, fmt.Errorf("spec.policyRefs[%d].kind %q is not supported in MVP (allowed: AgentPolicy)", i, ref.Kind)
		}
	}
	return layers, nil
}

func validatePolicyRef(ref relayv1alpha1.PolicyRef, index int) error {
	if strings.TrimSpace(ref.Name) == "" {
		return fmt.Errorf("spec.policyRefs[%d].name is required", index)
	}
	if ref.Kind != "" && ref.Kind != "AgentPolicy" {
		return fmt.Errorf("spec.policyRefs[%d].kind %q is not supported in MVP (allowed: AgentPolicy)", index, ref.Kind)
	}
	return nil
}

func loadAgentPolicy(ctx context.Context, c client.Reader, namespace, name string) (Layer, error) {
	var ap relayv1alpha1.AgentPolicy
	key := client.ObjectKey{Namespace: namespace, Name: name}
	if err := c.Get(ctx, key, &ap); err != nil {
		if apierrors.IsNotFound(err) {
			return Layer{}, fmt.Errorf("spec.policyRefs: AgentPolicy %q not found in namespace %q", name, namespace)
		}
		return Layer{}, fmt.Errorf("spec.policyRefs: get AgentPolicy %q: %w", name, err)
	}
	return Layer{
		Rules: ap.Spec.PolicyRules,
		Mode:  NormalizeMode(ap.Spec.Mode),
		Match: &relayv1alpha1.MatchedPolicyRef{
			Kind:       "AgentPolicy",
			Name:       ap.Name,
			UID:        string(ap.UID),
			Generation: ap.Generation,
			Mode:       NormalizeMode(ap.Spec.Mode),
		},
	}, nil
}

// ApplyStatus writes merged policy onto AgentSession status.
func ApplyStatus(session *relayv1alpha1.AgentSession, resolved Resolved) {
	session.Status.MatchedPolicies = append([]relayv1alpha1.MatchedPolicyRef(nil), resolved.Matched...)
	session.Status.EffectivePolicy = &relayv1alpha1.EffectivePolicyStatus{
		Mode:        resolved.Mode,
		PolicyRules: resolved.Rules,
	}
}
