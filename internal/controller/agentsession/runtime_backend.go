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

	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/controller/job"
	"github.com/grantbarry29/scrutineer/internal/policy"
)

// runtimePhase is the backend-neutral lifecycle of a runtime. The reconciler maps it onto
// AgentSessionPhase (see applyRuntimePhase). runtimeStarting also means "indeterminate" —
// the reconciler only advances an empty/Pending session to Starting on it.
type runtimePhase string

const (
	runtimeStarting  runtimePhase = "Starting"
	runtimeRunning   runtimePhase = "Running"
	runtimeSucceeded runtimePhase = "Succeeded"
	runtimeFailed    runtimePhase = "Failed"
	runtimeTimedOut  runtimePhase = "TimedOut"
)

// observation is normalized runtime state returned by a backend. It carries no
// orchestrator types; the reconciler maps it onto AgentSession status/conditions/events.
type observation struct {
	// phase is the observed runtime lifecycle.
	phase runtimePhase
	// runtimeName feeds status.jobName (e.g. the Job name). Empty if no runtime yet.
	runtimeName string
	// runtimeRef is the backend-neutral identity of the created runtime object (kind +
	// apiVersion + name [+ uid]). Empty until a runtime exists. Feeds status.runtimeRef.
	runtimeRef *scrutineerv1alpha1.RuntimeRef
	// workloadName feeds status.podName (e.g. the Pod name). Empty if none yet.
	workloadName string
	// created reports that the runtime object was created on this reconcile (drives
	// StartTime, the JobCreated event, and the initial Starting phase).
	created bool
	// replaced reports that a pending runtime was deleted+recreated to sync policy env
	// vars (drives the PolicyEnvSynced event).
	replaced bool
	// policyInSync reports whether the runtime's propagated policy env matches the
	// effective policy. False surfaces PolicyEnvDrift on the session.
	policyInSync bool
	// policyMessage is the drift detail when policyInSync is false.
	policyMessage string
}

// runtimeBackend abstracts orchestrator-specific runtime mechanics (create/observe/stop)
// behind the backend-neutral governance pipeline. See
// docs/design/phase-6-orchestrator-interface.md.
//
// Backends own runtime mechanics only. They return a normalized observation; the
// reconciler — not the backend — owns AgentSession status, conditions, events, and audit.
// Selection is by spec.runtime.orchestrator via backendRegistry, so future backends
// (Tekton/Argo/Temporal) plug in without touching governance logic.
type runtimeBackend interface {
	// name is the spec.runtime.orchestrator value this backend handles.
	name() string

	// ensure creates the runtime if absent, reconciles drift, and returns normalized
	// observed state without mutating the session. Idempotent. Returns ErrJobNotOwned on
	// an ownership conflict.
	ensure(ctx context.Context, session *scrutineerv1alpha1.AgentSession, task *ResolvedTask, pol *policy.Resolved, profile *scrutineerv1alpha1.RuntimeProfile) (observation, error)

	// stop terminates the runtime (cancellation/finalizer). Idempotent; NotFound is success.
	stop(ctx context.Context, session *scrutineerv1alpha1.AgentSession) error

	// runtimeGone reports whether the runtime object is fully removed (or deleting), used
	// to gate finalizer removal.
	runtimeGone(ctx context.Context, session *scrutineerv1alpha1.AgentSession) (bool, error)

	// ownedType is the runtime object kind to watch via Owns() so completions trigger
	// reconciles (e.g. &batchv1.Job{}).
	ownedType() client.Object
}

// backendRegistry maps spec.runtime.orchestrator → backend.
type backendRegistry map[string]runtimeBackend

// defaultBackendRegistry builds the registry of supported runtime backends: the
// kubernetes-job backend and the kubernetes-pod reference backend.
func defaultBackendRegistry(c client.Client, apiReader client.Reader, scheme *runtime.Scheme) backendRegistry {
	jobBackend := newKubernetesJobBackend(c, apiReader, scheme)
	podBackend := newKubernetesPodBackend(c, apiReader, scheme)
	return backendRegistry{
		jobBackend.name(): jobBackend,
		podBackend.name(): podBackend,
	}
}

// kubernetesJobBackend runs governed AgentSessions as Kubernetes Jobs.
type kubernetesJobBackend struct {
	client    client.Client
	apiReader client.Reader
	scheme    *runtime.Scheme
}

func newKubernetesJobBackend(c client.Client, apiReader client.Reader, scheme *runtime.Scheme) *kubernetesJobBackend {
	return &kubernetesJobBackend{client: c, apiReader: apiReader, scheme: scheme}
}

func (b *kubernetesJobBackend) name() string { return OrchestratorKubernetesJob }

func (b *kubernetesJobBackend) ownedType() client.Object { return &batchv1.Job{} }

// ensure drives create→observe→pod-discovery and returns a normalized observation. It
// performs runtime mutations (create/delete) but never writes to session status.
func (b *kubernetesJobBackend) ensure(ctx context.Context, session *scrutineerv1alpha1.AgentSession, task *ResolvedTask, pol *policy.Resolved, profile *scrutineerv1alpha1.RuntimeProfile) (observation, error) {
	runtimeJob, meta, err := b.ensureJob(ctx, session, task, pol, profile)
	if err != nil {
		return observation{}, err
	}
	podName, err := b.findPodName(ctx, session, runtimeJob)
	if err != nil {
		return observation{}, fmt.Errorf("find pod for job %q: %w", runtimeJob.Name, err)
	}
	return observation{
		phase:       jobRuntimePhase(runtimeJob),
		runtimeName: runtimeJob.Name,
		runtimeRef: &scrutineerv1alpha1.RuntimeRef{
			APIVersion: batchv1.SchemeGroupVersion.String(),
			Kind:       "Job",
			Name:       runtimeJob.Name,
			UID:        string(runtimeJob.UID),
		},
		workloadName:  podName,
		created:       meta.created,
		replaced:      meta.replaced,
		policyInSync:  meta.policyInSync,
		policyMessage: meta.policyMessage,
	}, nil
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
func (b *kubernetesJobBackend) runtimeGone(ctx context.Context, session *scrutineerv1alpha1.AgentSession) (bool, error) {
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
func (b *kubernetesJobBackend) stop(ctx context.Context, session *scrutineerv1alpha1.AgentSession) error {
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

// ensureMeta captures non-phase facts the reconciler needs to map an ensure pass onto
// status (without leaking Job types).
type ensureMeta struct {
	created       bool
	replaced      bool
	policyInSync  bool
	policyMessage string
}

// ensureJob fetches the Job owned by the AgentSession, creating it if it does not yet
// exist and reconciling policy/runtime-profile drift. It returns the live Job and an
// ensureMeta describing what happened. It does not mutate session status.
func (b *kubernetesJobBackend) ensureJob(ctx context.Context, session *scrutineerv1alpha1.AgentSession, task *ResolvedTask, pol *policy.Resolved, profile *scrutineerv1alpha1.RuntimeProfile) (*batchv1.Job, ensureMeta, error) {
	jobKey := client.ObjectKey{Namespace: session.Namespace, Name: job.NameFor(session)}

	desired := job.Build(session, toJobTask(task), pol, profile)

	var existing batchv1.Job
	if err := b.client.Get(ctx, jobKey, &existing); err == nil {
		if !metav1.IsControlledBy(&existing, session) {
			return nil, ensureMeta{}, fmt.Errorf("%w: Job %q", ErrJobNotOwned, existing.Name)
		}
		if job.PolicyEnvDrift(&existing, desired) || job.RuntimeProfileDrift(&existing, desired) {
			if job.ReplaceableForSync(&existing) {
				propagation := metav1.DeletePropagationBackground
				if err := b.client.Delete(ctx, &existing, client.PropagationPolicy(propagation)); err != nil && !apierrors.IsNotFound(err) {
					return nil, ensureMeta{}, fmt.Errorf("delete Job %s for runtime sync: %w", jobKey, err)
				}
				replaced := job.PolicyEnvDrift(&existing, desired)
				runtimeJob, created, err := b.createJob(ctx, session, desired, jobKey)
				if err != nil {
					return nil, ensureMeta{}, err
				}
				return runtimeJob, ensureMeta{created: created, replaced: replaced, policyInSync: true}, nil
			}
			if job.PolicyEnvDrift(&existing, desired) {
				return &existing, ensureMeta{policyInSync: false, policyMessage: job.PolicyEnvDriftMessage()}, nil
			}
			// Non-replaceable runtime-profile-only drift: policy env is still current.
			return &existing, ensureMeta{policyInSync: true}, nil
		}
		return &existing, ensureMeta{policyInSync: true}, nil
	} else if !apierrors.IsNotFound(err) {
		return nil, ensureMeta{}, fmt.Errorf("get Job %s: %w", jobKey, err)
	}

	runtimeJob, created, err := b.createJob(ctx, session, desired, jobKey)
	if err != nil {
		return nil, ensureMeta{}, err
	}
	return runtimeJob, ensureMeta{created: created, policyInSync: true}, nil
}

// createJob creates the desired Job, resolving AlreadyExists races. created is true only
// when this call actually created the object (false when it already existed).
func (b *kubernetesJobBackend) createJob(ctx context.Context, session *scrutineerv1alpha1.AgentSession, desired *batchv1.Job, jobKey client.ObjectKey) (*batchv1.Job, bool, error) {
	if err := controllerutil.SetControllerReference(session, desired, b.scheme); err != nil {
		return nil, false, fmt.Errorf("set owner reference on Job: %w", err)
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
				return nil, false, fmt.Errorf("get Job after AlreadyExists: %w", gErr)
			}
			if !metav1.IsControlledBy(&got, session) {
				return nil, false, fmt.Errorf("%w: Job %q", ErrJobNotOwned, got.Name)
			}
			return &got, false, nil
		}
		return nil, false, fmt.Errorf("create Job: %w", err)
	}
	return desired, true, nil
}

// jobRuntimePhase maps a Job's status fields onto the backend-neutral runtimePhase.
func jobRuntimePhase(runtimeJob *batchv1.Job) runtimePhase {
	switch {
	case runtimeJob.Status.Succeeded > 0:
		return runtimeSucceeded
	case job.TimedOut(runtimeJob):
		return runtimeTimedOut
	case runtimeJob.Status.Failed > 0 && job.BackoffExhausted(runtimeJob):
		return runtimeFailed
	case runtimeJob.Status.Active > 0:
		return runtimeRunning
	default:
		return runtimeStarting
	}
}
