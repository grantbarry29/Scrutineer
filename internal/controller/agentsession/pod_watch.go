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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/grantbarry29/scrutineer/internal/controller/job"
)

// mapPodToSessions enqueues the AgentSession referenced by a Job-owned Pod label.
func (r *AgentSessionReconciler) mapPodToSessions(_ context.Context, obj client.Object) []reconcile.Request {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil
	}
	sessionName := pod.Labels[job.LabelSessionRef]
	if sessionName == "" || !podHasJobOwner(pod) {
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{
			Namespace: pod.Namespace,
			Name:      sessionName,
		},
	}}
}

func podHasJobOwner(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "Job" {
			return true
		}
	}
	return false
}
