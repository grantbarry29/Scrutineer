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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

// resolveRuntimeProfile loads the referenced RuntimeProfile, writes status, and sets RuntimeProfileResolved.
func (r *AgentSessionReconciler) resolveRuntimeProfile(ctx context.Context, session *relayv1alpha1.AgentSession) (*relayv1alpha1.RuntimeProfile, error) {
	ref := session.Spec.RuntimeProfileRef
	if ref == nil {
		session.Status.MatchedRuntimeProfile = nil
		if !isTerminal(session.Status.Phase) {
			setCondition(session, ConditionRuntimeProfileResolved, metav1.ConditionTrue, "NoProfileRef",
				"no runtime profile referenced")
		}
		return nil, nil
	}

	kind := ref.Kind
	if kind == "" {
		kind = "RuntimeProfile"
	}
	if kind != "RuntimeProfile" {
		return nil, fmt.Errorf("spec.runtimeProfileRef.kind %q is not supported (allowed: RuntimeProfile)", kind)
	}

	var rp relayv1alpha1.RuntimeProfile
	key := client.ObjectKey{Namespace: session.Namespace, Name: ref.Name}
	if err := r.Get(ctx, key, &rp); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("spec.runtimeProfileRef: RuntimeProfile %q not found in namespace %q", ref.Name, session.Namespace)
		}
		return nil, fmt.Errorf("spec.runtimeProfileRef: get RuntimeProfile %q: %w", ref.Name, err)
	}

	applyMatchedRuntimeProfileStatus(session, &rp)
	if !isTerminal(session.Status.Phase) {
		setCondition(session, ConditionRuntimeProfileResolved, metav1.ConditionTrue, "ProfileApplied",
			fmt.Sprintf("RuntimeProfile %q applied to Job template", rp.Name))
	}
	return &rp, nil
}

func applyMatchedRuntimeProfileStatus(session *relayv1alpha1.AgentSession, rp *relayv1alpha1.RuntimeProfile) {
	if rp == nil {
		session.Status.MatchedRuntimeProfile = nil
		return
	}
	session.Status.MatchedRuntimeProfile = &relayv1alpha1.MatchedRuntimeProfileRef{
		Kind:            "RuntimeProfile",
		Name:            rp.Name,
		UID:             string(rp.UID),
		ResourceVersion: rp.ResourceVersion,
		Generation:      rp.Generation,
	}
}
