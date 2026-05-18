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
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

// AgentSessionReconciler reconciles an AgentSession object.
type AgentSessionReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// requeueAfter is how long the reconciler waits before re-polling Job state when the Job
// is in flight. Reconciles are also triggered by watches on Jobs/Pods, so this is a backstop.
const requeueAfter = 15 * time.Second

// +kubebuilder:rbac:groups=relay.secureai.dev,resources=agentsessions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=relay.secureai.dev,resources=agentsessions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=relay.secureai.dev,resources=agentsessions/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

// Reconcile is the main entry point for AgentSession reconciliation.
//
// Flow:
//  1. Fetch the AgentSession (return cleanly on NotFound).
//  2. Initialize status.phase=Pending on first observation.
//  3. Validate the spec. On failure -> Denied, emit ValidationFailed, return.
//  4. Ensure the underlying Job exists. If missing, create it -> Starting + JobCreated.
//  5. Inspect the Job + owned Pod and map to Running/Succeeded/Failed/TimedOut.
//  6. Persist status via the status subresource.
//
// Reconcile is idempotent: re-running it against an unchanged cluster makes no API mutations.
func (r *AgentSessionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("agentsession", req.NamespacedName)

	var session relayv1alpha1.AgentSession
	if err := r.Get(ctx, req.NamespacedName, &session); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get AgentSession: %w", err)
	}

	if !session.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// Take a working copy so we can compute a single status patch at the end.
	original := session.DeepCopy()

	if session.Status.Phase == "" {
		session.Status.Phase = relayv1alpha1.PhasePending
	}
	session.Status.ObservedGeneration = session.Generation

	if verr := validateSpec(&session); verr != nil {
		logger.Info("AgentSession spec rejected", "reason", verr.Error())
		session.Status.Phase = relayv1alpha1.PhaseDenied
		setCompletionTime(&session)
		setCondition(&session, ConditionValidated, metav1.ConditionFalse, "InvalidSpec", verr.Error())
		r.recordWarning(&session, EventReasonValidationFailed, verr.Error())
		r.recordWarning(&session, EventReasonSessionDenied, "session denied due to invalid spec")
		return ctrl.Result{}, r.patchStatus(ctx, original, &session)
	}
	setCondition(&session, ConditionValidated, metav1.ConditionTrue, "SpecValid", "AgentSession spec accepted")

	resolvedTask, err := r.resolveTask(ctx, &session)
	if err != nil {
		logger.Info("AgentSession task resolution failed", "reason", err.Error())
		session.Status.Phase = relayv1alpha1.PhaseDenied
		setCompletionTime(&session)
		setCondition(&session, ConditionValidated, metav1.ConditionFalse, "InvalidTask", err.Error())
		r.recordWarning(&session, EventReasonValidationFailed, err.Error())
		r.recordWarning(&session, EventReasonSessionDenied, "session denied due to invalid task")
		return ctrl.Result{}, r.patchStatus(ctx, original, &session)
	}

	// Approval gate: if any approval reasons are listed, surface them. The MVP does not
	// block execution; future ApprovalPolicy/ApprovalRequest CRDs will introduce a
	// real gate (e.g. block-and-wait until a signed Approval object exists).
	if len(session.Spec.Policy.RequireHumanApproval) > 0 {
		logger.V(1).Info("session declares required approvals (not yet enforced)",
			"approvals", session.Spec.Policy.RequireHumanApproval)
	}

	job, err := r.ensureJob(ctx, &session, resolvedTask)
	if err != nil {
		return ctrl.Result{}, err
	}

	r.syncStatusFromJob(ctx, &session, job)
	podName, err := r.findPodName(ctx, &session, job)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("find pod for job %q: %w", job.Name, err)
	}
	if podName != "" {
		session.Status.PodName = podName
	}

	if err := r.patchStatus(ctx, original, &session); err != nil {
		return ctrl.Result{}, err
	}

	if isTerminal(session.Status.Phase) {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// ensureJob fetches the Job owned by the AgentSession, creating it if it does not yet exist.
//
// On every successful return (whether the Job was newly created or already existed),
// the RuntimeCreated condition is re-asserted on the AgentSession. This is required
// because the controller's local cache may lag behind the apiserver immediately after
// a Create — a follow-up reconcile that reads a stale cached AgentSession would
// otherwise issue a JSON-merge-patch that overwrites the conditions array and drops
// RuntimeCreated. Re-asserting on each reconcile keeps the condition convergent.
func (r *AgentSessionReconciler) ensureJob(ctx context.Context, session *relayv1alpha1.AgentSession, task *ResolvedTask) (*batchv1.Job, error) {
	jobKey := client.ObjectKey{Namespace: session.Namespace, Name: jobNameFor(session)}

	var existing batchv1.Job
	if err := r.Get(ctx, jobKey, &existing); err == nil {
		session.Status.JobName = existing.Name
		setCondition(session, ConditionRuntimeCreated, metav1.ConditionTrue, "JobCreated",
			fmt.Sprintf("Job %q exists", existing.Name))
		return &existing, nil
	} else if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("get Job %s: %w", jobKey, err)
	}

	desired := buildJob(session, task)
	if err := controllerutil.SetControllerReference(session, desired, r.Scheme); err != nil {
		return nil, fmt.Errorf("set owner reference on Job: %w", err)
	}

	if err := r.Create(ctx, desired); err != nil {
		if apierrors.IsAlreadyExists(err) {
			var got batchv1.Job
			if gErr := r.Get(ctx, jobKey, &got); gErr != nil {
				return nil, fmt.Errorf("get Job after AlreadyExists: %w", gErr)
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
	setCondition(session, ConditionRuntimeCreated, metav1.ConditionTrue, "JobCreated",
		fmt.Sprintf("Created Job %q", desired.Name))
	r.recordNormal(session, EventReasonJobCreated, fmt.Sprintf("Created Job %s", desired.Name))
	return desired, nil
}

// syncStatusFromJob maps the Job's status fields onto the AgentSession status.
func (r *AgentSessionReconciler) syncStatusFromJob(ctx context.Context, session *relayv1alpha1.AgentSession, job *batchv1.Job) {
	if job == nil {
		return
	}
	session.Status.JobName = job.Name

	switch {
	case job.Status.Succeeded > 0:
		if session.Status.Phase != relayv1alpha1.PhaseSucceeded {
			r.recordNormal(session, EventReasonJobSucceeded, "Job completed successfully")
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

	case job.Status.Failed > 0 && backoffExhausted(job):
		reason := "JobFailed"
		msg := "Underlying Job failed"
		phase := relayv1alpha1.PhaseFailed
		if jobTimedOut(job) {
			phase = relayv1alpha1.PhaseTimedOut
			reason = "JobTimedOut"
			msg = "Underlying Job exceeded its activeDeadlineSeconds"
		}
		if session.Status.Phase != phase {
			r.recordWarning(session, EventReasonJobFailed, msg)
		}
		session.Status.Phase = phase
		setCompletionTime(session)
		setCondition(session, ConditionCompleted, metav1.ConditionFalse, reason, msg)
		if session.Status.Result == nil {
			session.Status.Result = &relayv1alpha1.SessionResult{
				Outcome: "failed",
				Summary: msg,
			}
		}

	case job.Status.Active > 0:
		if session.Status.Phase != relayv1alpha1.PhaseRunning {
			r.recordNormal(session, EventReasonJobRunning, "Job is running")
		}
		session.Status.Phase = relayv1alpha1.PhaseRunning

	default:
		if session.Status.Phase == "" || session.Status.Phase == relayv1alpha1.PhasePending {
			session.Status.Phase = relayv1alpha1.PhaseStarting
		}
	}
}

// backoffExhausted returns true if the Job has hit its backoffLimit.
//
// For the MVP we set backoffLimit=0, so a single failed pod is terminal.
func backoffExhausted(job *batchv1.Job) bool {
	limit := int32(0)
	if job.Spec.BackoffLimit != nil {
		limit = *job.Spec.BackoffLimit
	}
	return job.Status.Failed > limit
}

// jobTimedOut returns true if the Job hit its activeDeadlineSeconds.
func jobTimedOut(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue && strings.EqualFold(c.Reason, "DeadlineExceeded") {
			return true
		}
	}
	return false
}

func (r *AgentSessionReconciler) recordNormal(session *relayv1alpha1.AgentSession, reason, msg string) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Event(session, corev1.EventTypeNormal, reason, msg)
}

func (r *AgentSessionReconciler) recordWarning(session *relayv1alpha1.AgentSession, reason, msg string) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Event(session, corev1.EventTypeWarning, reason, msg)
}

// SetupWithManager wires the reconciler into the controller-runtime manager.
func (r *AgentSessionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&relayv1alpha1.AgentSession{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}

// setCondition upserts a condition by Type onto the AgentSession status.
func setCondition(session *relayv1alpha1.AgentSession, condType string, status metav1.ConditionStatus, reason, msg string) {
	meta.SetStatusCondition(&session.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: session.Generation,
	})
}

func setCompletionTime(session *relayv1alpha1.AgentSession) {
	if session.Status.CompletionTime == nil {
		now := metav1.Now()
		session.Status.CompletionTime = &now
	}
}

func isTerminal(phase relayv1alpha1.AgentSessionPhase) bool {
	switch phase {
	case relayv1alpha1.PhaseSucceeded,
		relayv1alpha1.PhaseFailed,
		relayv1alpha1.PhaseDenied,
		relayv1alpha1.PhaseTimedOut,
		relayv1alpha1.PhaseCancelled:
		return true
	}
	return false
}
