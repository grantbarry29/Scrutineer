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

	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	relayjob "github.com/secureai/relay/internal/controller/job"
	"github.com/secureai/relay/internal/enforcement"
	"github.com/secureai/relay/internal/enforcement/networkpolicy"
)

// patchStatusWithEnforcement persists status and reconciles NetworkPolicy enforcement.
func (r *AgentSessionReconciler) patchStatusWithEnforcement(ctx context.Context, original, session *relayv1alpha1.AgentSession, profile *relayv1alpha1.RuntimeProfile) error {
	if isTerminal(session.Status.Phase) {
		// Runtime tool-approval holds are only meaningful while the session runs;
		// reconcileRuntimeApprovals does not run on terminal passes, so clear any
		// stale "pending approval" entries here as a central guard.
		session.Status.PendingApprovals = nil
		if err := r.collectSessionOutputs(ctx, session); err != nil {
			r.recordWarning(session, EventReasonOutputsCollectionFailed, err.Error())
		}
	}
	if err := r.ensureNetworkPolicy(ctx, session, profile); err != nil {
		return fmt.Errorf("ensure NetworkPolicy: %w", err)
	}
	return r.patchStatus(ctx, original, session)
}

func (r *AgentSessionReconciler) ensureNetworkPolicy(ctx context.Context, session *relayv1alpha1.AgentSession, profile *relayv1alpha1.RuntimeProfile) error {
	if session == nil {
		return nil
	}

	enfCtx := enforcement.NewSessionContext(session, profile, relayjob.NameFor(session))
	var backend networkpolicy.Backend
	desired, err := backend.DesiredState(enfCtx)
	if err != nil {
		return err
	}

	key := client.ObjectKey{
		Namespace: session.Namespace,
		Name:      networkpolicy.NameFor(session.Namespace, session.Name),
	}

	if desired == nil || isTerminal(session.Status.Phase) {
		return r.deleteNetworkPolicyIfExists(ctx, key)
	}

	np, ok := desired.(*networkingv1.NetworkPolicy)
	if !ok {
		return fmt.Errorf("networkpolicy backend returned %T", desired)
	}

	var existing networkingv1.NetworkPolicy
	if getErr := r.Get(ctx, key, &existing); getErr == nil {
		if !metav1.IsControlledBy(&existing, session) {
			return fmt.Errorf("NetworkPolicy %q is not owned by AgentSession %q", key.Name, session.Name)
		}
		if networkPolicySpecEqual(&existing, np) {
			return nil
		}
		existing.Labels = np.Labels
		existing.Spec = np.Spec
		if updateErr := r.Update(ctx, &existing); updateErr != nil {
			return fmt.Errorf("update NetworkPolicy %s: %w", key, updateErr)
		}
		r.recordNormal(session, EventReasonNetworkPolicySynced, fmt.Sprintf("Updated NetworkPolicy %q", key.Name))
		return nil
	} else if !apierrors.IsNotFound(getErr) {
		return fmt.Errorf("get NetworkPolicy %s: %w", key, getErr)
	}

	if err := controllerutil.SetControllerReference(session, np, r.Scheme); err != nil {
		return fmt.Errorf("set owner reference on NetworkPolicy: %w", err)
	}
	setBlockOwnerDeletion(np, false)
	if err := r.Create(ctx, np); err != nil {
		return fmt.Errorf("create NetworkPolicy %s: %w", key, err)
	}
	r.recordNormal(session, EventReasonNetworkPolicySynced, fmt.Sprintf("Created NetworkPolicy %q", key.Name))
	return nil
}

func (r *AgentSessionReconciler) deleteNetworkPolicyIfExists(ctx context.Context, key client.ObjectKey) error {
	var existing networkingv1.NetworkPolicy
	if err := r.Get(ctx, key, &existing); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get NetworkPolicy %s: %w", key, err)
	}
	policy := metav1.DeletePropagationBackground
	if err := r.Delete(ctx, &existing, client.PropagationPolicy(policy)); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete NetworkPolicy %s: %w", key, err)
	}
	return nil
}

func networkPolicySpecEqual(existing, desired *networkingv1.NetworkPolicy) bool {
	if existing == nil || desired == nil {
		return existing == desired
	}
	return equality.Semantic.DeepEqual(existing.Labels, desired.Labels) &&
		equality.Semantic.DeepEqual(existing.Spec, desired.Spec)
}
