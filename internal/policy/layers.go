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

// LoadPolicyLayers fetches policy CRDs referenced by the session in declaration order.
func LoadPolicyLayers(ctx context.Context, c client.Reader, session *relayv1alpha1.AgentSession) ([]Layer, error) {
	var layers []Layer
	for i, ref := range session.Spec.PolicyRefs {
		if err := validatePolicyRef(ref, i); err != nil {
			return nil, err
		}
		kind := ref.Kind
		if kind == "" {
			kind = "AgentPolicy"
		}
		switch kind {
		case "AgentPolicy":
			layer, err := loadAgentPolicy(ctx, c, session.Namespace, ref.Name)
			if err != nil {
				return nil, err
			}
			layers = append(layers, layer)
		case "ToolPolicy":
			layer, err := loadToolPolicy(ctx, c, session.Namespace, ref.Name)
			if err != nil {
				return nil, err
			}
			layers = append(layers, layer)
		default:
			return nil, fmt.Errorf("spec.policyRefs[%d].kind %q is not supported (allowed: AgentPolicy, ToolPolicy)", i, kind)
		}
	}
	return layers, nil
}

func validatePolicyRef(ref relayv1alpha1.PolicyRef, index int) error {
	if strings.TrimSpace(ref.Name) == "" {
		return fmt.Errorf("spec.policyRefs[%d].name is required", index)
	}
	switch ref.Kind {
	case "", "AgentPolicy", "ToolPolicy":
		return nil
	default:
		return fmt.Errorf("spec.policyRefs[%d].kind %q is not supported (allowed: AgentPolicy, ToolPolicy)", index, ref.Kind)
	}
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

func loadToolPolicy(ctx context.Context, c client.Reader, namespace, name string) (Layer, error) {
	var tp relayv1alpha1.ToolPolicy
	key := client.ObjectKey{Namespace: namespace, Name: name}
	if err := c.Get(ctx, key, &tp); err != nil {
		if apierrors.IsNotFound(err) {
			return Layer{}, fmt.Errorf("spec.policyRefs: ToolPolicy %q not found in namespace %q", name, namespace)
		}
		return Layer{}, fmt.Errorf("spec.policyRefs: get ToolPolicy %q: %w", name, err)
	}
	return Layer{
		Rules: tp.Spec.ToolPolicyRules(),
		Mode:  NormalizeMode(tp.Spec.Mode),
		Match: &relayv1alpha1.MatchedPolicyRef{
			Kind:       "ToolPolicy",
			Name:       tp.Name,
			UID:        string(tp.UID),
			Generation: tp.Generation,
			Mode:       NormalizeMode(tp.Spec.Mode),
		},
	}, nil
}
