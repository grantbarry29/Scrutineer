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

	batchv1 "k8s.io/api/batch/v1"
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

// podDeadlineExceededReason is the Pod status.reason set by the kubelet when a Pod is
// killed for exceeding spec.activeDeadlineSeconds.
const podDeadlineExceededReason = "DeadlineExceeded"

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

// ensure creates the agent Pod if absent, reconciles policy/runtime-profile drift, and
// returns a normalized observation. It never writes to session status. Returns
// ErrJobNotOwned on an ownership conflict at the deterministic name.
func (b *kubernetesPodBackend) ensure(ctx context.Context, session *relayv1alpha1.AgentSession, task *ResolvedTask, pol *policy.Resolved, profile *relayv1alpha1.RuntimeProfile) (observation, error) {
	podKey := client.ObjectKey{Namespace: session.Namespace, Name: job.NameFor(session)}
	desired := b.buildPod(session, task, pol, profile)

	var existing corev1.Pod
	if err := b.client.Get(ctx, podKey, &existing); err == nil {
		if !metav1.IsControlledBy(&existing, session) {
			return observation{}, fmt.Errorf("%w: Pod %q", ErrJobNotOwned, existing.Name)
		}
		return b.reconcileExisting(ctx, session, &existing, desired, podKey)
	} else if !apierrors.IsNotFound(err) {
		return observation{}, fmt.Errorf("get Pod %s: %w", podKey, err)
	}

	created, didCreate, err := b.createPod(ctx, session, desired, podKey)
	if err != nil {
		return observation{}, err
	}
	obs := b.observe(created, didCreate)
	obs.policyInSync = true
	return obs, nil
}

// reconcileExisting handles an already-owned Pod. Pods are immutable, so policy/profile
// drift can only be resolved by delete+recreate while the Pod has not started; on a
// running Pod the drift is surfaced (policyInSync=false) without disruption, mirroring
// the Job backend's PolicyEnvDrift semantics.
func (b *kubernetesPodBackend) reconcileExisting(ctx context.Context, session *relayv1alpha1.AgentSession, existing, desired *corev1.Pod, podKey client.ObjectKey) (observation, error) {
	policyDrift := podPolicyEnvDrift(existing, desired)
	profileDrift := podRuntimeProfileDrift(existing, desired)

	if !policyDrift && !profileDrift {
		obs := b.observe(existing, false)
		obs.policyInSync = true
		return obs, nil
	}

	if podReplaceableForSync(existing) {
		propagation := metav1.DeletePropagationBackground
		if err := b.client.Delete(ctx, existing, client.PropagationPolicy(propagation)); err != nil && !apierrors.IsNotFound(err) {
			return observation{}, fmt.Errorf("delete Pod %s for runtime sync: %w", podKey, err)
		}
		created, didCreate, err := b.createPod(ctx, session, desired, podKey)
		if err != nil {
			return observation{}, err
		}
		obs := b.observe(created, didCreate)
		obs.replaced = policyDrift
		obs.policyInSync = true
		return obs, nil
	}

	// Running Pod: cannot replace without disruption.
	obs := b.observe(existing, false)
	if policyDrift {
		obs.policyInSync = false
		obs.policyMessage = podPolicyEnvDriftMessage()
		return obs, nil
	}
	// Profile-only drift on a running Pod: policy env is still current.
	obs.policyInSync = true
	return obs, nil
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

// observe maps a live Pod onto a normalized observation (phase + identity). Callers set
// the policy-sync fields per reconcile path. For a Pod backend the runtime object and the
// workload are the same object, so podName == runtimeRef.Name.
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

	// Clear blockOwnerDeletion so the Pod can be removed while the AgentSession still
	// exists (parity with the Job backend). createPod sets this false, but an adopted or
	// legacy Pod may carry blockOwnerDeletion=true and would otherwise deadlock teardown.
	if needsBlockOwnerDeletionPatch(&existing) {
		setBlockOwnerDeletion(&existing, false)
		if err := b.client.Update(ctx, &existing); err != nil {
			return fmt.Errorf("patch Pod owner reference %s: %w", podKey, err)
		}
	}

	if err := b.client.Delete(ctx, &existing); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete Pod %s: %w", podKey, err)
	}
	return nil
}

// podRuntimePhase maps a Pod's status onto the backend-neutral runtimePhase. A Pod failed
// for exceeding spec.activeDeadlineSeconds carries status.reason=DeadlineExceeded and is
// reported as timed-out (distinct from a generic failure). Pending and the empty initial
// phase map to runtimeStarting (the "starting/indeterminate" state).
func podRuntimePhase(pod *corev1.Pod) runtimePhase {
	switch pod.Status.Phase {
	case corev1.PodSucceeded:
		return runtimeSucceeded
	case corev1.PodFailed:
		if pod.Status.Reason == podDeadlineExceededReason {
			return runtimeTimedOut
		}
		return runtimeFailed
	case corev1.PodRunning:
		return runtimeRunning
	default:
		return runtimeStarting
	}
}

// podReplaceableForSync reports whether an owned Pod can be deleted+recreated to sync
// drifted policy/profile without disrupting a started workload. Only a Pod that has not
// begun running (empty initial phase or Pending) is replaceable.
func podReplaceableForSync(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	switch pod.Status.Phase {
	case "", corev1.PodPending:
		return true
	default:
		return false
	}
}

// podDriftJob wraps a Pod's spec as a throwaway Job so the Pod backend can reuse the
// Job backend's tested drift detection (which compares Spec.Template.Spec). This avoids
// duplicating the managed-env-key and runtime-profile comparison logic.
func podDriftJob(pod *corev1.Pod) *batchv1.Job {
	return &batchv1.Job{Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: pod.Spec}}}
}

// podPolicyEnvDrift reports whether the propagated AGENT_POLICY_* env on the existing Pod
// differs from the desired Pod.
func podPolicyEnvDrift(existing, desired *corev1.Pod) bool {
	return job.PolicyEnvDrift(podDriftJob(existing), podDriftJob(desired))
}

// podRuntimeProfileDrift reports whether RuntimeProfile-derived pod fields differ.
func podRuntimeProfileDrift(existing, desired *corev1.Pod) bool {
	return job.RuntimeProfileDrift(podDriftJob(existing), podDriftJob(desired))
}

// podPolicyEnvDriftMessage explains stale env on a running, non-replaceable Pod.
func podPolicyEnvDriftMessage() string {
	return "Effective policy changed but the owned Pod is immutable while running; " +
		"status.effectivePolicy is current; AGENT_POLICY_* env inside the running Pod may be stale until the Pod is replaced"
}
