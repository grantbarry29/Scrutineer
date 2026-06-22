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
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/controller/job"
	"github.com/secureai/relay/internal/policy"
)

// runtimeBackend abstracts orchestrator-specific runtime mechanics (create/observe/stop)
// behind the backend-neutral governance pipeline. See
// docs/design/phase-6-orchestrator-interface.md.
//
// Slice 2 ships the kubernetes-job backend only. It is transitional: backend methods
// still mutate AgentSession status/conditions/events directly (the normalized-Observation
// refinement where the reconciler owns all status mapping is a tracked follow-up). The
// value of this slice is the seam — the reconciler routes every runtime call through a
// registry keyed by spec.runtime.orchestrator, so future backends (Tekton/Argo/Temporal)
// plug in without touching governance logic.
type runtimeBackend interface {
	// name is the spec.runtime.orchestrator value this backend handles.
	name() string

	// ensure creates the runtime if absent, reconciles drift, and maps observed runtime
	// state onto the session status (jobName, podName, phase, conditions, events).
	// Idempotent. Returns ErrJobNotOwned on an ownership conflict.
	ensure(ctx context.Context, session *relayv1alpha1.AgentSession, task *ResolvedTask, pol *policy.Resolved, profile *relayv1alpha1.RuntimeProfile) error

	// stop terminates the runtime (cancellation/finalizer). Idempotent; NotFound is success.
	stop(ctx context.Context, session *relayv1alpha1.AgentSession) error

	// runtimeGone reports whether the runtime object is fully removed (or deleting), used
	// to gate finalizer removal.
	runtimeGone(ctx context.Context, session *relayv1alpha1.AgentSession) (bool, error)

	// ownedType is the runtime object kind to watch via Owns() so completions trigger
	// reconciles (e.g. &batchv1.Job{}).
	ownedType() client.Object
}

// backendRegistry maps spec.runtime.orchestrator → backend.
type backendRegistry map[string]runtimeBackend

// defaultBackendRegistry builds the registry of supported runtime backends. Slice 2
// registers only the kubernetes-job backend.
func defaultBackendRegistry(c client.Client, apiReader client.Reader, scheme *runtime.Scheme, rec record.EventRecorder) backendRegistry {
	jobBackend := newKubernetesJobBackend(c, apiReader, scheme, rec)
	return backendRegistry{jobBackend.name(): jobBackend}
}

// kubernetesJobBackend runs governed AgentSessions as Kubernetes Jobs.
type kubernetesJobBackend struct {
	client    client.Client
	apiReader client.Reader
	scheme    *runtime.Scheme
	recorder  record.EventRecorder
}

func newKubernetesJobBackend(c client.Client, apiReader client.Reader, scheme *runtime.Scheme, rec record.EventRecorder) *kubernetesJobBackend {
	return &kubernetesJobBackend{client: c, apiReader: apiReader, scheme: scheme, recorder: rec}
}

func (b *kubernetesJobBackend) name() string { return OrchestratorKubernetesJob }

func (b *kubernetesJobBackend) ownedType() client.Object { return &batchv1.Job{} }

func (b *kubernetesJobBackend) recordNormal(session *relayv1alpha1.AgentSession, reason, msg string) {
	if b.recorder == nil {
		return
	}
	b.recorder.Event(session, corev1.EventTypeNormal, reason, msg)
}

func (b *kubernetesJobBackend) recordWarning(session *relayv1alpha1.AgentSession, reason, msg string) {
	if b.recorder == nil {
		return
	}
	b.recorder.Event(session, corev1.EventTypeWarning, reason, msg)
}

// ensure drives the create→observe→pod-discovery sequence and folds the observed state
// onto the session status, preserving the previous inline reconciler behavior.
func (b *kubernetesJobBackend) ensure(ctx context.Context, session *relayv1alpha1.AgentSession, task *ResolvedTask, pol *policy.Resolved, profile *relayv1alpha1.RuntimeProfile) error {
	runtimeJob, err := b.ensureJob(ctx, session, task, pol, profile)
	if err != nil {
		return err
	}
	b.syncStatusFromJob(ctx, session, runtimeJob)
	podName, err := b.findPodName(ctx, session, runtimeJob)
	if err != nil {
		return fmt.Errorf("find pod for job %q: %w", runtimeJob.Name, err)
	}
	if podName != "" {
		session.Status.PodName = podName
	}
	return nil
}

func (b *kubernetesJobBackend) getJob(ctx context.Context, key client.ObjectKey, runtimeJob *batchv1.Job) error {
	if b.apiReader != nil {
		if err := b.apiReader.Get(ctx, key, runtimeJob); err == nil || !apierrors.IsNotFound(err) {
			return err
		}
	}
	return b.client.Get(ctx, key, runtimeJob)
}

// runtimeGone reports whether the owned Job is fully removed (NotFound) or already
// deleting (deletionTimestamp set). A live Job without a deletion timestamp is not gone.
func (b *kubernetesJobBackend) runtimeGone(ctx context.Context, session *relayv1alpha1.AgentSession) (bool, error) {
	jobKey := client.ObjectKey{Namespace: session.Namespace, Name: job.NameFor(session)}
	var runtimeJob batchv1.Job
	if err := b.getJob(ctx, jobKey, &runtimeJob); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, fmt.Errorf("get Job %s: %w", jobKey, err)
	}
	return !runtimeJob.DeletionTimestamp.IsZero(), nil
}

// stop deletes the deterministic Job for the AgentSession. A missing Job is treated as
// already stopped. Delete is issued after a Get so the blockOwnerDeletion patch can be
// applied; NotFound at any point is success.
func (b *kubernetesJobBackend) stop(ctx context.Context, session *relayv1alpha1.AgentSession) error {
	jobKey := client.ObjectKey{Namespace: session.Namespace, Name: job.NameFor(session)}

	var existing batchv1.Job
	if err := b.client.Get(ctx, jobKey, &existing); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get Job %s: %w", jobKey, err)
	}

	// Clear blockOwnerDeletion so the Job can be removed while the AgentSession still exists.
	if needsBlockOwnerDeletionPatch(&existing) {
		setBlockOwnerDeletion(&existing, false)
		if err := b.client.Update(ctx, &existing); err != nil {
			return fmt.Errorf("patch Job owner reference %s: %w", jobKey, err)
		}
	}

	propagation := metav1.DeletePropagationBackground
	if err := b.client.Delete(ctx, &existing, client.PropagationPolicy(propagation)); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete Job %s: %w", jobKey, err)
	}
	return nil
}

// ensureJob fetches the Job owned by the AgentSession, creating it if it does not yet exist.
//
// On every successful return (whether the Job was newly created or already existed),
// the RuntimeCreated condition is re-asserted on the AgentSession. This is required
// because the controller's local cache may lag behind the apiserver immediately after
// a Create — a follow-up reconcile that reads a stale cached AgentSession would
// otherwise issue a JSON-merge-patch that overwrites the conditions array and drops
// RuntimeCreated. Re-asserting on each reconcile keeps the condition convergent.
func (b *kubernetesJobBackend) ensureJob(ctx context.Context, session *relayv1alpha1.AgentSession, task *ResolvedTask, pol *policy.Resolved, profile *relayv1alpha1.RuntimeProfile) (*batchv1.Job, error) {
	jobKey := client.ObjectKey{Namespace: session.Namespace, Name: job.NameFor(session)}

	desired := job.Build(session, toJobTask(task), pol, profile)

	var existing batchv1.Job
	if err := b.client.Get(ctx, jobKey, &existing); err == nil {
		if !metav1.IsControlledBy(&existing, session) {
			return nil, fmt.Errorf("%w: Job %q", ErrJobNotOwned, existing.Name)
		}
		if job.PolicyEnvDrift(&existing, desired) || job.RuntimeProfileDrift(&existing, desired) {
			if job.ReplaceableForSync(&existing) {
				propagation := metav1.DeletePropagationBackground
				if err := b.client.Delete(ctx, &existing, client.PropagationPolicy(propagation)); err != nil && !apierrors.IsNotFound(err) {
					return nil, fmt.Errorf("delete Job %s for runtime sync: %w", jobKey, err)
				}
				if job.PolicyEnvDrift(&existing, desired) {
					b.recordNormal(session, EventReasonPolicyEnvSynced, "Replaced pending Job to sync policy env vars")
				}
			} else if job.PolicyEnvDrift(&existing, desired) {
				msg := job.PolicyEnvDriftMessage()
				session.Status.JobName = existing.Name
				setCondition(session, ConditionPolicyPropagated, metav1.ConditionFalse, "PolicyEnvDrift", msg)
				setCondition(session, ConditionRuntimeCreated, metav1.ConditionTrue, "JobCreated",
					fmt.Sprintf("Job %q exists", existing.Name))
				b.recordWarning(session, EventReasonPolicyEnvDrift, msg)
				return &existing, nil
			} else {
				session.Status.JobName = existing.Name
				setCondition(session, ConditionPolicyPropagated, metav1.ConditionTrue, "EnvCurrent",
					"Job policy env vars match effective policy")
				setCondition(session, ConditionRuntimeCreated, metav1.ConditionTrue, "JobCreated",
					fmt.Sprintf("Job %q exists", existing.Name))
				return &existing, nil
			}
		} else {
			session.Status.JobName = existing.Name
			setCondition(session, ConditionPolicyPropagated, metav1.ConditionTrue, "EnvCurrent",
				"Job policy env vars match effective policy")
			setCondition(session, ConditionRuntimeCreated, metav1.ConditionTrue, "JobCreated",
				fmt.Sprintf("Job %q exists", existing.Name))
			return &existing, nil
		}
	} else if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("get Job %s: %w", jobKey, err)
	}
	if err := controllerutil.SetControllerReference(session, desired, b.scheme); err != nil {
		return nil, fmt.Errorf("set owner reference on Job: %w", err)
	}
	// Allow the owned Job to be deleted while the AgentSession is still present (e.g. during
	// finalizer cleanup or cancellation). The default blockOwnerDeletion=true deadlocks
	// deletion: the session cannot finish until the Job is gone, but the Job cannot be
	// removed until the session is gone.
	setBlockOwnerDeletion(desired, false)

	if err := b.client.Create(ctx, desired); err != nil {
		if apierrors.IsAlreadyExists(err) {
			var got batchv1.Job
			if gErr := b.client.Get(ctx, jobKey, &got); gErr != nil {
				return nil, fmt.Errorf("get Job after AlreadyExists: %w", gErr)
			}
			if !metav1.IsControlledBy(&got, session) {
				return nil, fmt.Errorf("%w: Job %q", ErrJobNotOwned, got.Name)
			}
			return &got, nil
		}
		return nil, fmt.Errorf("create Job: %w", err)
	}

	session.Status.JobName = desired.Name
	if session.Status.StartTime == nil {
		now := metav1.Now()
		session.Status.StartTime = &now
	}
	session.Status.Phase = relayv1alpha1.PhaseStarting
	setCondition(session, ConditionPolicyPropagated, metav1.ConditionTrue, "EnvCurrent",
		"Job policy env vars match effective policy")
	setCondition(session, ConditionRuntimeCreated, metav1.ConditionTrue, "JobCreated",
		fmt.Sprintf("Created Job %q", desired.Name))
	b.recordNormal(session, EventReasonJobCreated, fmt.Sprintf("Created Job %s", desired.Name))
	return desired, nil
}

// syncStatusFromJob maps the Job's status fields onto the AgentSession status.
func (b *kubernetesJobBackend) syncStatusFromJob(ctx context.Context, session *relayv1alpha1.AgentSession, runtimeJob *batchv1.Job) {
	if runtimeJob == nil {
		return
	}
	session.Status.JobName = runtimeJob.Name
	if isTerminal(session.Status.Phase) {
		return
	}

	switch {
	case runtimeJob.Status.Succeeded > 0:
		if session.Status.Phase != relayv1alpha1.PhaseSucceeded {
			b.recordNormal(session, EventReasonJobSucceeded, "Job completed successfully")
		}
		session.Status.Phase = relayv1alpha1.PhaseSucceeded
		setCompletionTime(session)
		setCondition(session, ConditionCompleted, metav1.ConditionTrue, "JobSucceeded", "Underlying Job completed successfully")
		if session.Status.Result == nil {
			session.Status.Result = &relayv1alpha1.SessionResult{
				Outcome: "completed",
				Summary: "Job completed successfully",
			}
		}

	case job.TimedOut(runtimeJob):
		msg := "Underlying Job exceeded its activeDeadlineSeconds"
		if session.Status.Phase != relayv1alpha1.PhaseTimedOut {
			b.recordWarning(session, EventReasonJobFailed, msg)
		}
		session.Status.Phase = relayv1alpha1.PhaseTimedOut
		setCompletionTime(session)
		setCondition(session, ConditionCompleted, metav1.ConditionFalse, "JobTimedOut", msg)
		if session.Status.Result == nil {
			session.Status.Result = &relayv1alpha1.SessionResult{
				Outcome: "failed",
				Summary: msg,
			}
		}

	case runtimeJob.Status.Failed > 0 && job.BackoffExhausted(runtimeJob):
		msg := "Underlying Job failed"
		if session.Status.Phase != relayv1alpha1.PhaseFailed {
			b.recordWarning(session, EventReasonJobFailed, msg)
		}
		session.Status.Phase = relayv1alpha1.PhaseFailed
		setCompletionTime(session)
		setCondition(session, ConditionCompleted, metav1.ConditionFalse, "JobFailed", msg)
		if session.Status.Result == nil {
			session.Status.Result = &relayv1alpha1.SessionResult{
				Outcome: "failed",
				Summary: msg,
			}
		}

	case runtimeJob.Status.Active > 0:
		if session.Status.Phase != relayv1alpha1.PhaseRunning {
			b.recordNormal(session, EventReasonJobRunning, "Job is running")
		}
		session.Status.Phase = relayv1alpha1.PhaseRunning

	default:
		if session.Status.Phase == "" || session.Status.Phase == relayv1alpha1.PhasePending {
			session.Status.Phase = relayv1alpha1.PhaseStarting
		}
	}
}
