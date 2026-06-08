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

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

func (r *AgentSessionReconciler) mapRuntimeProfileToSessions(ctx context.Context, obj client.Object) []reconcile.Request {
	rp, ok := obj.(*relayv1alpha1.RuntimeProfile)
	if !ok {
		return nil
	}
	return r.sessionsReferencingRuntimeProfile(ctx, rp.Namespace, rp.Name)
}

func (r *AgentSessionReconciler) sessionsReferencingRuntimeProfile(ctx context.Context, namespace, name string) []reconcile.Request {
	var sessions relayv1alpha1.AgentSessionList
	if err := r.List(ctx, &sessions, client.InNamespace(namespace)); err != nil {
		return nil
	}
	var out []reconcile.Request
	for i := range sessions.Items {
		session := &sessions.Items[i]
		if sessionReferencesRuntimeProfile(session, name) {
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

func sessionReferencesRuntimeProfile(session *relayv1alpha1.AgentSession, profileName string) bool {
	ref := session.Spec.RuntimeProfileRef
	if ref == nil {
		return false
	}
	if ref.Name != profileName {
		return false
	}
	kind := ref.Kind
	if kind == "" {
		kind = "RuntimeProfile"
	}
	return kind == "RuntimeProfile"
}
