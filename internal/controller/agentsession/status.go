/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// patchStatus writes the status subresource when it has changed.
//
// CRD status subresources accept JSON merge patch and server-side apply, but not
// strategic merge patch. A plain MergeFrom patch replaces the entire conditions
// array, which drops types that exist on the apiserver but were missing from a
// stale controller cache read.
//
// Strategy:
//  1. Union conditions from the reconcile-start snapshot and the reconciler's
//     desired status (mergeStatusConditionsInPlace).
//  2. Re-read the live object and union its conditions again so concurrent writes
//     and cache lag cannot erase condition types.
//  3. Status().Update the live object (optimistic concurrency via resourceVersion).
func (r *AgentSessionReconciler) patchStatus(ctx context.Context, original, updated *relayv1alpha1.AgentSession) error {
	desired := updated.Status.DeepCopy()
	mergeStatusConditionsInPlace(&desired.Conditions, original.Status.Conditions)
	mergeRuntimePolicyDecisionsInPlace(&desired.PolicyDecisions, original.Status.PolicyDecisions)
	mergeViolationsInPlace(&desired.Violations, original.Status.Violations)
	mergeEventsInPlace(&desired.Events, original.Status.Events)

	var live relayv1alpha1.AgentSession
	key := client.ObjectKeyFromObject(updated)
	if err := r.Get(ctx, key, &live); err != nil {
		return fmt.Errorf("get AgentSession before status update: %w", err)
	}
	mergeStatusConditionsInPlace(&desired.Conditions, live.Status.Conditions)
	mergeRuntimePolicyDecisionsInPlace(&desired.PolicyDecisions, live.Status.PolicyDecisions)
	mergeViolationsInPlace(&desired.Violations, live.Status.Violations)
	mergeEventsInPlace(&desired.Events, live.Status.Events)

	if equalStatus(&live.Status, desired) {
		return nil
	}

	live.Status = *desired
	if err := r.Status().Update(ctx, &live); err != nil {
		if apierrors.IsConflict(err) {
			return fmt.Errorf("update AgentSession status: conflict (will requeue): %w", err)
		}
		return fmt.Errorf("update AgentSession status: %w", err)
	}
	return nil
}

// mergeStatusConditionsInPlace adds conditions from preserve that are absent in dst.
// When both sides have the same type, dst wins (the reconciler's latest intent).
func mergeStatusConditionsInPlace(dst *[]metav1.Condition, preserve []metav1.Condition) {
	if dst == nil {
		return
	}
	for _, c := range preserve {
		if meta.FindStatusCondition(*dst, c.Type) == nil {
			*dst = append(*dst, c)
		}
	}
}
