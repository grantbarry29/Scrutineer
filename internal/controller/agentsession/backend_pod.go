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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/controller/job"
	"github.com/secureai/relay/internal/policy"
)

// kubernetesPodBackend runs governed AgentSessions as a single bare Kubernetes Pod
// (no Job wrapper). It reuses the shared agent pod template (job.BuildPodTemplateSpec)
// so the data-plane wiring is identical to the Job backend; only the runtime object
// kind differs. It is the second reference backend proving Relay is orchestrator-agnostic.
//
// This slice covers the create/observe/stop happy path. Completion/timeout/drift edge
// cases and watch wiring are handled in later Phase 6 slices.
type kubernetesPodBackend struct {
	client    client.Client
	apiReader client.Reader
	scheme    *runtime.Scheme
}

func newKubernetesPodBackend(c client.Client, apiReader client.Reader, scheme *runtime.Scheme) *kubernetesPodBackend {
	return &kubernetesPodBackend{client: c, apiReader: apiReader, scheme: scheme}
}

func (b *kubernetesPodBackend) name() string { return OrchestratorKubernetesPod }

func (b *kubernetesPodBackend) ownedType() client.Object { return &corev1.Pod{} }

// ensure creates the agent Pod if absent and returns a normalized observation. It never
// writes to session status. Returns ErrJobNotOwned on an ownership conflict at the
// deterministic name.
func (b *kubernetesPodBackend) ensure(ctx context.Context, session *relayv1alpha1.AgentSession, task *ResolvedTask, pol *policy.Resolved, profile *relayv1alpha1.RuntimeProfile) (observation, error) {
	podKey := client.ObjectKey{Namespace: session.Namespace, Name: job.NameFor(session)}

	var existing corev1.Pod
	if err := b.client.Get(ctx, podKey, &existing); err == nil {
		if !metav1.IsControlledBy(&existing, session) {
			return observation{}, fmt.Errorf("%w: Pod %q", ErrJobNotOwned, existing.Name)
		}
		return b.observe(&existing, false), nil
	} else if !apierrors.IsNotFound(err) {
		return observation{}, fmt.Errorf("get Pod %s: %w", podKey, err)
	}

	created, didCreate, err := b.createPod(ctx, session, b.buildPod(session, task, pol, profile), podKey)
	if err != nil {
		return observation{}, err
	}
	return b.observe(created, didCreate), nil
}

// buildPod renders the desired agent Pod from the shared pod template, adding the
// deterministic name and the active deadline derived from spec.runtime.timeoutSeconds.
func (b *kubernetesPodBackend) buildPod(session *relayv1alpha1.AgentSession, task *ResolvedTask, pol *policy.Resolved, profile *relayv1alpha1.RuntimeProfile) *corev1.Pod {
	tmpl := job.BuildPodTemplateSpec(session, toJobTask(task), pol, profile)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      job.NameFor(session),
			Namespace: session.Namespace,
			Labels:    tmpl.Labels,
		},
		Spec: tmpl.Spec,
	}
	if rt := session.Spec.Runtime; rt.TimeoutSeconds != nil && *rt.TimeoutSeconds > 0 {
		t := *rt.TimeoutSeconds
		pod.Spec.ActiveDeadlineSeconds = &t
	}
	return pod
}

// createPod creates the desired Pod, resolving AlreadyExists races. didCreate is true
// only when this call actually created the object.
func (b *kubernetesPodBackend) createPod(ctx context.Context, session *relayv1alpha1.AgentSession, desired *corev1.Pod, podKey client.ObjectKey) (*corev1.Pod, bool, error) {
	if err := controllerutil.SetControllerReference(session, desired, b.scheme); err != nil {
		return nil, false, fmt.Errorf("set owner reference on Pod: %w", err)
	}
	// Allow the owned Pod to be deleted while the AgentSession still exists (cancellation
	// or finalizer cleanup); blockOwnerDeletion=true would deadlock teardown.
	setBlockOwnerDeletion(desired, false)

	if err := b.client.Create(ctx, desired); err != nil {
		if apierrors.IsAlreadyExists(err) {
			var got corev1.Pod
			if gErr := b.client.Get(ctx, podKey, &got); gErr != nil {
				return nil, false, fmt.Errorf("get Pod after AlreadyExists: %w", gErr)
			}
			if !metav1.IsControlledBy(&got, session) {
				return nil, false, fmt.Errorf("%w: Pod %q", ErrJobNotOwned, got.Name)
			}
			return &got, false, nil
		}
		return nil, false, fmt.Errorf("create Pod: %w", err)
	}
	return desired, true, nil
}

// observe maps a live Pod onto a normalized observation. For a Pod backend the runtime
// object and the workload are the same object, so podName == runtimeRef.Name.
func (b *kubernetesPodBackend) observe(pod *corev1.Pod, created bool) observation {
	return observation{
		phase:       podRuntimePhase(pod),
		runtimeName: pod.Name,
		runtimeRef: &relayv1alpha1.RuntimeRef{
			APIVersion: corev1.SchemeGroupVersion.String(),
			Kind:       "Pod",
			Name:       pod.Name,
			UID:        string(pod.UID),
		},
		workloadName: pod.Name,
		created:      created,
		// Policy-env drift detection/replacement for Pods is slice 6; the freshly built
		// Pod always reflects current policy on create.
		policyInSync: true,
	}
}

// getPod reads through the API (uncached) first when an apiReader is available so the
// finalizer path sees deletions promptly.
func (b *kubernetesPodBackend) getPod(ctx context.Context, key client.ObjectKey, pod *corev1.Pod) error {
	if b.apiReader != nil {
		if err := b.apiReader.Get(ctx, key, pod); err == nil || !apierrors.IsNotFound(err) {
			return err
		}
	}
	return b.client.Get(ctx, key, pod)
}

// runtimeGone reports whether the owned Pod is fully removed (NotFound) or already
// deleting (deletionTimestamp set).
func (b *kubernetesPodBackend) runtimeGone(ctx context.Context, session *relayv1alpha1.AgentSession) (bool, error) {
	podKey := client.ObjectKey{Namespace: session.Namespace, Name: job.NameFor(session)}
	var pod corev1.Pod
	if err := b.getPod(ctx, podKey, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, fmt.Errorf("get Pod %s: %w", podKey, err)
	}
	return !pod.DeletionTimestamp.IsZero(), nil
}

// stop deletes the deterministic Pod for the AgentSession. A missing Pod is treated as
// already stopped. blockOwnerDeletion was cleared at create time, so deletion does not
// deadlock against the still-present session.
func (b *kubernetesPodBackend) stop(ctx context.Context, session *relayv1alpha1.AgentSession) error {
	podKey := client.ObjectKey{Namespace: session.Namespace, Name: job.NameFor(session)}

	var existing corev1.Pod
	if err := b.client.Get(ctx, podKey, &existing); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get Pod %s: %w", podKey, err)
	}

	if err := b.client.Delete(ctx, &existing); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete Pod %s: %w", podKey, err)
	}
	return nil
}

// podRuntimePhase maps a Pod's status.phase onto the backend-neutral runtimePhase.
// Timeout (DeadlineExceeded) distinction and drift handling are refined in slice 6.
func podRuntimePhase(pod *corev1.Pod) runtimePhase {
	switch pod.Status.Phase {
	case corev1.PodSucceeded:
		return runtimeSucceeded
	case corev1.PodFailed:
		return runtimeFailed
	case corev1.PodRunning:
		return runtimeRunning
	default:
		return runtimeStarting
	}
}
