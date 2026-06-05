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

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

// mapAgentPolicyToSessions enqueues AgentSessions in the policy namespace that reference it.
func (r *AgentSessionReconciler) mapAgentPolicyToSessions(ctx context.Context, obj client.Object) []reconcile.Request {
	ap, ok := obj.(*relayv1alpha1.AgentPolicy)
	if !ok {
		return nil
	}

	var sessions relayv1alpha1.AgentSessionList
	if err := r.List(ctx, &sessions, client.InNamespace(ap.Namespace)); err != nil {
		return nil
	}

	var out []reconcile.Request
	for i := range sessions.Items {
		session := &sessions.Items[i]
		if sessionReferencesAgentPolicy(session, ap.Name) {
			out = append(out, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: session.Namespace,
					Name:      session.Name,
				},
			})
		}
	}
	return out
}

func sessionReferencesAgentPolicy(session *relayv1alpha1.AgentSession, policyName string) bool {
	for _, ref := range session.Spec.PolicyRefs {
		if ref.Name != policyName {
			continue
		}
		if ref.Kind == "" || ref.Kind == "AgentPolicy" {
			return true
		}
	}
	return false
}
