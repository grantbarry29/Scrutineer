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

func (r *AgentSessionReconciler) mapAgentPolicyToSessions(ctx context.Context, obj client.Object) []reconcile.Request {
	ap, ok := obj.(*relayv1alpha1.AgentPolicy)
	if !ok {
		return nil
	}
	return r.sessionsReferencingPolicy(ctx, ap.Namespace, "AgentPolicy", ap.Name)
}

func (r *AgentSessionReconciler) mapToolPolicyToSessions(ctx context.Context, obj client.Object) []reconcile.Request {
	tp, ok := obj.(*relayv1alpha1.ToolPolicy)
	if !ok {
		return nil
	}
	return r.sessionsReferencingPolicy(ctx, tp.Namespace, "ToolPolicy", tp.Name)
}

func (r *AgentSessionReconciler) sessionsReferencingPolicy(ctx context.Context, namespace, kind, name string) []reconcile.Request {
	var sessions relayv1alpha1.AgentSessionList
	if err := r.List(ctx, &sessions, client.InNamespace(namespace)); err != nil {
		return nil
	}
	var out []reconcile.Request
	for i := range sessions.Items {
		session := &sessions.Items[i]
		if sessionReferencesPolicy(session, kind, name) {
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

func sessionReferencesPolicy(session *relayv1alpha1.AgentSession, kind, policyName string) bool {
	for _, ref := range session.Spec.PolicyRefs {
		if ref.Name != policyName {
			continue
		}
		refKind := ref.Kind
		if refKind == "" {
			refKind = "AgentPolicy"
		}
		if refKind == kind {
			return true
		}
	}
	return false
}

// sessionReferencesAgentPolicy supports tests that predate ToolPolicy.
func sessionReferencesAgentPolicy(session *relayv1alpha1.AgentSession, policyName string) bool {
	return sessionReferencesPolicy(session, "AgentPolicy", policyName)
}
