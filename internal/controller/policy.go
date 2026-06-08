/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/policy"
)

// resolvePolicy loads referenced policies, merges them with inline overrides, and writes status.
func (r *AgentSessionReconciler) resolvePolicy(ctx context.Context, session *relayv1alpha1.AgentSession) (*policy.Resolved, error) {
	layers, err := policy.LoadPolicyLayers(ctx, r, session)
	if err != nil {
		return nil, err
	}
	resolved := policy.Resolve(layers, session.Spec.Policy.PolicyRules)
	policy.ApplyStatus(session, resolved)
	if !isTerminal(session.Status.Phase) {
		msg := fmt.Sprintf("merged %d referenced policies with inline overrides (mode=%s)",
			len(resolved.Matched), resolved.Mode)
		setCondition(session, ConditionPolicyResolved, metav1.ConditionTrue, "PoliciesMerged", msg)
	}
	return &resolved, nil
}
