/*
Copyright 2026 The Scrutineer Authors.

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

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	scrutineerjob "github.com/grantbarry29/scrutineer/internal/controller/job"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
	"github.com/grantbarry29/scrutineer/internal/enforcement/networkpolicy"
)

// patchStatusWithEnforcement persists status and reconciles NetworkPolicy enforcement.
func (r *AgentSessionReconciler) patchStatusWithEnforcement(ctx context.Context, original, session *scrutineerv1alpha1.AgentSession, profile *scrutineerv1alpha1.RuntimeProfile) error {
	if isTerminal(session.Status.Phase) {
		// Runtime tool-approval holds are only meaningful while the session runs;
		// reconcileRuntimeApprovals does not run on terminal passes, so clear any
		// stale "pending approval" entries here as a central guard.
		session.Status.PendingApprovals = nil
		if err := r.collectSessionOutputs(ctx, session); err != nil {
			r.recordWarning(session, EventReasonOutputsCollectionFailed, err.Error())
		}
	}
	if err := r.ensureEgressProxy(ctx, session, profile); err != nil {
		return fmt.Errorf("ensure egress proxy: %w", err)
	}
	if err := r.ensureNetworkPolicy(ctx, session, profile); err != nil {
		return fmt.Errorf("ensure NetworkPolicy: %w", err)
	}
	return r.patchStatus(ctx, original, session)
}

// ensureNetworkPolicy reconciles both per-session egress policies: the agent-pod routing
// lock (allow only Envoy) and, when the egress proxy is enabled, the Envoy-pod backstop
// (deny cloud metadata + operator CIDRs even though Envoy egresses freely). Both are torn
// down on terminal or when no longer applicable.
func (r *AgentSessionReconciler) ensureNetworkPolicy(ctx context.Context, session *scrutineerv1alpha1.AgentSession, profile *scrutineerv1alpha1.RuntimeProfile) error {
	if session == nil {
		return nil
	}
	enfCtx := enforcement.NewSessionContext(session, profile, scrutineerjob.NameFor(session))
	terminal := isTerminal(session.Status.Phase)

	// Agent-pod routing lock.
	var backend networkpolicy.Backend
	desired, err := backend.DesiredState(enfCtx)
	if err != nil {
		return err
	}
	var lock *networkingv1.NetworkPolicy
	if !terminal && desired != nil {
		np, ok := desired.(*networkingv1.NetworkPolicy)
		if !ok {
			return fmt.Errorf("networkpolicy backend returned %T", desired)
		}
		lock = np
	}
	lockKey := client.ObjectKey{Namespace: session.Namespace, Name: networkpolicy.NameFor(session.Namespace, session.Name)}
	if err := r.reconcileOwnedNetworkPolicy(ctx, session, lockKey, lock); err != nil {
		return err
	}

	// Envoy-pod egress backstop (only while the egress proxy is enabled).
	var backstop *networkingv1.NetworkPolicy
	if !terminal && profileEnablesEnvoy(profile) {
		backstop = networkpolicy.BuildEgressProxyBackstop(enfCtx, r.egressBackstopCIDRs())
	}
	backstopKey := client.ObjectKey{Namespace: session.Namespace, Name: networkpolicy.BackstopNameFor(session.Namespace, session.Name)}
	return r.reconcileOwnedNetworkPolicy(ctx, session, backstopKey, backstop)
}

// reconcileOwnedNetworkPolicy converges one owned NetworkPolicy to desired (nil ⇒ delete).
// Idempotent; an existing policy of the same name not owned by the session is a conflict.
func (r *AgentSessionReconciler) reconcileOwnedNetworkPolicy(ctx context.Context, session *scrutineerv1alpha1.AgentSession, key client.ObjectKey, desired *networkingv1.NetworkPolicy) error {
	if desired == nil {
		return r.deleteNetworkPolicyIfExists(ctx, key)
	}

	var existing networkingv1.NetworkPolicy
	if getErr := r.Get(ctx, key, &existing); getErr == nil {
		if !metav1.IsControlledBy(&existing, session) {
			return fmt.Errorf("NetworkPolicy %q is not owned by AgentSession %q", key.Name, session.Name)
		}
		if networkPolicySpecEqual(&existing, desired) {
			return nil
		}
		existing.Labels = desired.Labels
		existing.Spec = desired.Spec
		if updateErr := r.Update(ctx, &existing); updateErr != nil {
			return fmt.Errorf("update NetworkPolicy %s: %w", key, updateErr)
		}
		r.recordNormal(session, EventReasonNetworkPolicySynced, fmt.Sprintf("Updated NetworkPolicy %q", key.Name))
		return nil
	} else if !apierrors.IsNotFound(getErr) {
		return fmt.Errorf("get NetworkPolicy %s: %w", key, getErr)
	}

	if err := controllerutil.SetControllerReference(session, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner reference on NetworkPolicy: %w", err)
	}
	setBlockOwnerDeletion(desired, false)
	if err := r.Create(ctx, desired); err != nil {
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
