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

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

// findPodName returns the name of the newest Pod owned by the Job, if any.
// Returns ("", nil) when no Pod exists yet so reconcile can continue.
func (r *AgentSessionReconciler) findPodName(ctx context.Context, session *relayv1alpha1.AgentSession, job *batchv1.Job) (string, error) {
	if job == nil {
		return "", nil
	}

	var pods corev1.PodList
	if err := r.List(ctx, &pods,
		client.InNamespace(session.Namespace),
		client.MatchingLabels{LabelSessionRef: session.Name},
	); err != nil {
		return "", err
	}

	if chosen := newestPodOwnedByJob(pods.Items, job.UID); chosen != nil {
		return chosen.Name, nil
	}
	return "", nil
}

// podOwnedByJob reports whether pod is owned by the Job with the given UID.
func podOwnedByJob(pod *corev1.Pod, jobUID types.UID) bool {
	if pod == nil {
		return false
	}
	for _, ref := range pod.OwnerReferences {
		if ref.UID == jobUID && ref.Kind == batchv1.SchemeGroupVersion.WithKind("Job").Kind {
			return true
		}
	}
	return false
}

// newestPodOwnedByJob returns the Pod with the latest CreationTimestamp among
// those owned by the Job. Nil when none match (e.g. Pod not created yet).
func newestPodOwnedByJob(pods []corev1.Pod, jobUID types.UID) *corev1.Pod {
	var newest *corev1.Pod
	for i := range pods {
		p := &pods[i]
		if !podOwnedByJob(p, jobUID) {
			continue
		}
		if newest == nil || p.CreationTimestamp.After(newest.CreationTimestamp.Time) {
			newest = p
		}
	}
	return newest
}
