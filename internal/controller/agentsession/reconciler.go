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
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/approval"
	"github.com/grantbarry29/scrutineer/internal/audit"
	"github.com/grantbarry29/scrutineer/internal/controller/job"
	"github.com/grantbarry29/scrutineer/internal/enforcement/networkpolicy"
	"github.com/grantbarry29/scrutineer/internal/tracing"
)

// AgentSessionReconciler reconciles an AgentSession object.
type AgentSessionReconciler struct {
	client.Client
	// APIReader is an uncached reader used to detect deletion and Job cleanup state when
	// the cached client lags behind the apiserver (common in envtest and after kubectl delete).
	APIReader  client.Reader
	Scheme     *runtime.Scheme
	Recorder   record.EventRecorder
	RESTConfig *rest.Config
	// Notifier delivers approval-gate notifications to an external channel. When nil,
	// notifications are disabled (the approval gate itself is unaffected).
	Notifier  approval.Notifier
	clientset kubernetes.Interface
	// backends maps spec.runtime.orchestrator → runtime backend. Lazily built from the
	// reconciler's client/scheme/recorder so direct (non-manager) test construction works.
	backends backendRegistry
	// egress selects how agent egress is forced through the per-session chokepoint.
	// Lazily defaulted to the explicit-proxy backend (the node interceptor, #64, plugs
	// in here later). See egress_envoy.go.
	egress egressBackend
	// EgressBackstopCIDRs are egress ranges hard-denied to the Envoy proxy pod (defense in
	// depth even if Envoy is compromised). Empty ⇒ networkpolicy.DefaultBackstopCIDRs (cloud
	// metadata). Operators extend it with cluster/service/API CIDRs via the manager flag.
	EgressBackstopCIDRs []string
}

// egressBackstopCIDRs returns the configured Envoy-pod egress backstops, or the safe default.
func (r *AgentSessionReconciler) egressBackstopCIDRs() []string {
	if len(r.EgressBackstopCIDRs) > 0 {
		return r.EgressBackstopCIDRs
	}
	return networkpolicy.DefaultBackstopCIDRs
}

// ensureBackends lazily builds the runtime backend registry from the reconciler's deps.
func (r *AgentSessionReconciler) ensureBackends() {
	if r.backends == nil {
		r.backends = defaultBackendRegistry(r.Client, r.APIReader, r.Scheme)
	}
}

// runtimeBackendFor selects the backend for a session's orchestrator. An empty
// orchestrator defaults to kubernetes-job (validateSpec enforces accepted values).
func (r *AgentSessionReconciler) runtimeBackendFor(session *scrutineerv1alpha1.AgentSession) (runtimeBackend, error) {
	r.ensureBackends()
	orchestrator := session.Spec.Runtime.Orchestrator
	if orchestrator == "" {
		orchestrator = OrchestratorKubernetesJob
	}
	backend, ok := r.backends[orchestrator]
	if !ok {
		return nil, fmt.Errorf("no runtime backend registered for orchestrator %q", orchestrator)
	}
	return backend, nil
}

// requeueAfter is how long the reconciler waits before re-polling Job state when the Job
// is in flight. Reconciles are also triggered by watches on Jobs/Pods, so this is a backstop.
const requeueAfter = 15 * time.Second

// deletionRequeueAfter is used while finalizer cleanup waits for the owned Job to finish deleting.
const deletionRequeueAfter = 2 * time.Second

// +kubebuilder:rbac:groups=scrutineer.sh,resources=agentpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=scrutineer.sh,resources=toolpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=scrutineer.sh,resources=runtimeprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups=scrutineer.sh,resources=approvalpolicies,verbs=get;list;watch
// ApprovalRequests are created+mutated by the controller (owner-ref GC handles deletion, so no delete verb).
// +kubebuilder:rbac:groups=scrutineer.sh,resources=approvalrequests,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=scrutineer.sh,resources=approvalrequests/status,verbs=get;update;patch
// AgentSession is the primary resource: read+mutate (status/finalizers) only; the controller never creates or deletes it.
// +kubebuilder:rbac:groups=scrutineer.sh,resources=agentsessions,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=scrutineer.sh,resources=agentsessions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=scrutineer.sh,resources=agentsessions/finalizers,verbs=update
// Runtime objects: the kubernetes-job backend owns Jobs; the kubernetes-pod backend owns Pods (create/update/delete).
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=pods/log,verbs=get
// +kubebuilder:rbac:groups="",resources=pods/exec,verbs=create
// Output ConfigMaps/Secrets are created+updated then owner-ref GC'd; the per-session egress
// proxy also creates+deletes a ConfigMap, Service, and ServiceAccount (torn down on terminal).
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete

// Reconcile is the main entry point for AgentSession reconciliation.
//
// Flow:
//  1. Fetch the AgentSession (return cleanly on NotFound).
//  2. If deleting: delete the owned Job, then remove the Scrutineer finalizer.
//  3. Ensure the Scrutineer finalizer is present on live sessions.
//  4. Initialize status.phase=Pending on first observation.
//  5. Validate the spec. On failure -> Denied, emit ValidationFailed, return.
//  6. If spec.cancelRequested, delete the owned Job (if any), set Phase=Cancelled, and return.
//  7. Ensure the underlying Job exists. If missing, create it -> Starting + JobCreated.
//  8. Inspect the Job + owned Pod and map to Running/Succeeded/Failed/TimedOut.
//  9. Persist status via the status subresource.
//
// Reconcile is idempotent: re-running it against an unchanged cluster makes no API mutations.
func (r *AgentSessionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	ctx, span := tracing.StartReconcileSpan(ctx, req.Namespace, req.Name)
	var tracedSession *scrutineerv1alpha1.AgentSession
	var initialPhase string
	defer func() {
		phase := ""
		if tracedSession != nil {
			phase = string(tracedSession.Status.Phase)
			if phase != "" && phase != initialPhase {
				audit.Emit(ctx, audit.SessionPhaseChange(req.Namespace, req.Name, initialPhase, phase, time.Now()))
			}
		}
		tracing.SetReconcileSpanResult(ctx, phase, result.RequeueAfter.Seconds(), err)
		span.End()
	}()

	logger := log.FromContext(ctx).WithValues("agentsession", req.NamespacedName)

	session, err := r.getAgentSession(ctx, req.NamespacedName)
	tracedSession = session
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get AgentSession: %w", err)
	}

	if !session.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, session)
	}

	requeue, err := r.ensureFinalizer(ctx, session)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure finalizer: %w", err)
	}
	if requeue {
		return ctrl.Result{}, nil
	}

	// Take a working copy so we can compute a single status patch at the end.
	original := session.DeepCopy()
	initialPhase = string(original.Status.Phase)
	var resolvedProfile *scrutineerv1alpha1.RuntimeProfile

	if session.Status.Phase == "" {
		session.Status.Phase = scrutineerv1alpha1.PhasePending
	}
	session.Status.ObservedGeneration = session.Generation

	if verr := validateSpec(session); verr != nil {
		logger.Info("AgentSession spec rejected", "reason", verr.Error())
		session.Status.Phase = scrutineerv1alpha1.PhaseDenied
		setCompletionTime(session)
		setCondition(session, ConditionValidated, metav1.ConditionFalse, "InvalidSpec", verr.Error())
		setReadyCondition(session)
		r.recordWarning(session, EventReasonValidationFailed, verr.Error())
		r.recordWarning(session, EventReasonSessionDenied, "session denied due to invalid spec")
		return ctrl.Result{}, r.patchStatusWithEnforcement(ctx, original, session, resolvedProfile)
	}
	// Do not re-assert SpecValid on terminal sessions; a prior reconcile may have set JobConflict
	// or InvalidSpec and those reasons must survive status merges on requeue.
	if !isTerminal(session.Status.Phase) {
		setCondition(session, ConditionValidated, metav1.ConditionTrue, "SpecValid", "AgentSession spec accepted")
	}

	resolvedTask, err := r.resolveTask(ctx, session)
	if err != nil {
		logger.Info("AgentSession task resolution failed", "reason", err.Error())
		session.Status.Phase = scrutineerv1alpha1.PhaseDenied
		setCompletionTime(session)
		setCondition(session, ConditionValidated, metav1.ConditionFalse, "InvalidTask", err.Error())
		setReadyCondition(session)
		r.recordWarning(session, EventReasonValidationFailed, err.Error())
		r.recordWarning(session, EventReasonSessionDenied, "session denied due to invalid task")
		return ctrl.Result{}, r.patchStatusWithEnforcement(ctx, original, session, resolvedProfile)
	}

	resolvedPolicy, err := r.resolvePolicy(ctx, session, original.Status.PolicyDecisions)
	if err != nil {
		logger.Info("AgentSession policy resolution failed", "reason", err.Error())
		session.Status.Phase = scrutineerv1alpha1.PhaseDenied
		setCompletionTime(session)
		setCondition(session, ConditionValidated, metav1.ConditionFalse, "InvalidPolicy", err.Error())
		setReadyCondition(session)
		r.recordWarning(session, EventReasonValidationFailed, err.Error())
		r.recordWarning(session, EventReasonSessionDenied, "session denied due to invalid policy")
		return ctrl.Result{}, r.patchStatusWithEnforcement(ctx, original, session, resolvedProfile)
	}
	if len(resolvedPolicy.Matched) > 0 && conditionChanged(original, session, ConditionPolicyResolved) {
		r.recordNormal(session, EventReasonPolicyResolved,
			fmt.Sprintf("Merged %d referenced policies (mode=%s)", len(resolvedPolicy.Matched), resolvedPolicy.Mode))
	}

	resolvedProfile, err = r.resolveRuntimeProfile(ctx, session)
	if err != nil {
		logger.Info("AgentSession runtime profile resolution failed", "reason", err.Error())
		session.Status.Phase = scrutineerv1alpha1.PhaseDenied
		setCompletionTime(session)
		setCondition(session, ConditionValidated, metav1.ConditionFalse, "InvalidRuntimeProfile", err.Error())
		setReadyCondition(session)
		r.recordWarning(session, EventReasonValidationFailed, err.Error())
		r.recordWarning(session, EventReasonSessionDenied, "session denied due to invalid runtime profile")
		return ctrl.Result{}, r.patchStatusWithEnforcement(ctx, original, session, resolvedProfile)
	}
	if resolvedProfile != nil && conditionChanged(original, session, ConditionRuntimeProfileResolved) {
		r.recordNormal(session, EventReasonRuntimeProfileResolved,
			fmt.Sprintf("Applied RuntimeProfile %q to Job template", resolvedProfile.Name))
	}

	if session.Spec.CancelRequested {
		logger.Info("cancellation requested; stopping owned runtime if present")
		backend, err := r.runtimeBackendFor(session)
		if err != nil {
			return ctrl.Result{}, err
		}
		if err := backend.stop(ctx, session); err != nil {
			return ctrl.Result{}, fmt.Errorf("stop runtime Job: %w", err)
		}
		r.applyCancellationStatus(session)
		setReadyCondition(session)
		return ctrl.Result{}, r.patchStatusWithEnforcement(ctx, original, session, resolvedProfile)
	}

	// Terminal sessions must not get a new Job (e.g. after TTL/manual delete). Cancellation
	// is handled above even when phase is already Cancelled.
	if isTerminal(session.Status.Phase) {
		return ctrl.Result{}, r.patchStatusWithEnforcement(ctx, original, session, resolvedProfile)
	}

	// Human-approval gate: hold the session before runtime creation when a matching
	// ApprovalPolicy requires approval. Granting resumes; denial/timeout-deny ends terminal.
	switch outcome, gerr := r.reconcileApprovalGate(ctx, session, resolvedPolicy); {
	case gerr != nil:
		return ctrl.Result{}, fmt.Errorf("approval gate: %w", gerr)
	case outcome == approvalRejected:
		setReadyCondition(session)
		return ctrl.Result{}, r.patchStatusWithEnforcement(ctx, original, session, resolvedProfile)
	case outcome == approvalPending:
		session.Status.Phase = scrutineerv1alpha1.PhaseAwaitingApproval
		setReadyCondition(session)
		if perr := r.patchStatusWithEnforcement(ctx, original, session, resolvedProfile); perr != nil {
			return ctrl.Result{}, perr
		}
		return ctrl.Result{RequeueAfter: approvalRecheckInterval}, nil
	}

	// Mid-execution per-tool approvals: resolve any runtime ApprovalRequests for
	// this session (decision -> state -> audit) without gating the session phase.
	if err := r.reconcileRuntimeApprovals(ctx, session); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconcile runtime approvals: %w", err)
	}

	// Provision the per-session egress proxy before the agent runtime so the agent can be
	// pointed at the proxy's ClusterIP (the Slice B routing lock denies DNS, so a name
	// won't resolve). Idempotent; teardown on terminal/disabled stays in
	// patchStatusWithEnforcement, which the terminal/cancelled paths above already hit.
	if err := r.ensureEgressProxy(ctx, session, resolvedProfile); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure egress proxy: %w", err)
	}
	if err := r.resolveEgressProxyEndpoint(ctx, session, resolvedProfile); err != nil {
		return ctrl.Result{}, fmt.Errorf("resolve egress proxy endpoint: %w", err)
	}

	backend, err := r.runtimeBackendFor(session)
	if err != nil {
		return ctrl.Result{}, err
	}
	obs, err := backend.ensure(ctx, session, resolvedTask, resolvedPolicy, resolvedProfile)
	if err != nil {
		if errors.Is(err, ErrJobNotOwned) {
			session.Status.Phase = scrutineerv1alpha1.PhaseDenied
			setCondition(session, ConditionValidated, metav1.ConditionFalse, "JobConflict", err.Error())
			setReadyCondition(session)
			r.recordWarning(session, EventReasonSessionDenied, err.Error())
			return ctrl.Result{}, r.patchStatusWithEnforcement(ctx, original, session, resolvedProfile)
		}
		return ctrl.Result{}, err
	}
	r.applyObservation(session, obs)

	setReadyCondition(session)
	if err := r.patchStatusWithEnforcement(ctx, original, session, resolvedProfile); err != nil {
		return ctrl.Result{}, err
	}

	if isTerminal(session.Status.Phase) {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func (r *AgentSessionReconciler) getAgentSession(ctx context.Context, key client.ObjectKey) (*scrutineerv1alpha1.AgentSession, error) {
	var session scrutineerv1alpha1.AgentSession
	if err := r.Get(ctx, key, &session); err != nil {
		return nil, err
	}
	if r.APIReader != nil && session.DeletionTimestamp.IsZero() {
		var latest scrutineerv1alpha1.AgentSession
		if err := r.APIReader.Get(ctx, key, &latest); err == nil {
			session = latest
		}
	}
	return &session, nil
}

// handleDeletion runs finalizer cleanup: stop the owned runtime, wait until it is gone,
// then remove the Scrutineer finalizer so the AgentSession object can be removed.
func (r *AgentSessionReconciler) handleDeletion(ctx context.Context, session *scrutineerv1alpha1.AgentSession) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(session, AgentSessionFinalizer) {
		return ctrl.Result{}, nil
	}

	backend, err := r.runtimeBackendFor(session)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Always issue a stop for the deterministic runtime so cleanup does not depend on
	// a prior successful cache read (create vs delete races in tests and slow caches).
	if err := backend.stop(ctx, session); err != nil {
		return ctrl.Result{}, fmt.Errorf("stop runtime Job: %w", err)
	}

	// Stop accepted but the runtime object may linger (e.g. envtest without GC). Once it is
	// gone (or deleting), owned-runtime cleanup is done for finalizer purposes.
	gone, err := backend.runtimeGone(ctx, session)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !gone {
		return ctrl.Result{RequeueAfter: deletionRequeueAfter}, nil
	}

	if err := r.removeFinalizer(ctx, session); err != nil {
		return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

// ensureFinalizer adds the Scrutineer finalizer if missing. Returns true when an update was
// applied and reconcile should return before other work (finalizer was just added).
func (r *AgentSessionReconciler) ensureFinalizer(ctx context.Context, session *scrutineerv1alpha1.AgentSession) (bool, error) {
	if controllerutil.ContainsFinalizer(session, AgentSessionFinalizer) {
		return false, nil
	}

	key := client.ObjectKeyFromObject(session)
	var added bool
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest scrutineerv1alpha1.AgentSession
		if err := r.Get(ctx, key, &latest); err != nil {
			return err
		}
		if controllerutil.ContainsFinalizer(&latest, AgentSessionFinalizer) {
			*session = latest
			return nil
		}
		controllerutil.AddFinalizer(&latest, AgentSessionFinalizer)
		if err := r.Update(ctx, &latest); err != nil {
			return err
		}
		*session = latest
		added = true
		return nil
	})
	return added, err
}

func (r *AgentSessionReconciler) removeFinalizer(ctx context.Context, session *scrutineerv1alpha1.AgentSession) error {
	key := client.ObjectKeyFromObject(session)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest scrutineerv1alpha1.AgentSession
		if err := r.Get(ctx, key, &latest); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		if !controllerutil.ContainsFinalizer(&latest, AgentSessionFinalizer) {
			return nil
		}
		controllerutil.RemoveFinalizer(&latest, AgentSessionFinalizer)
		err := r.Update(ctx, &latest)
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	})
}

// needsBlockOwnerDeletionPatch reports whether any owner reference still sets
// blockOwnerDeletion=true, which would deadlock teardown (the owned runtime object cannot
// be deleted until the session is gone, but the session waits on the runtime). Generic
// over the runtime object kind so both the Job and Pod backends share it.
func needsBlockOwnerDeletionPatch(obj metav1.Object) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.BlockOwnerDeletion != nil && *ref.BlockOwnerDeletion {
			return true
		}
	}
	return false
}

// applyCancellationStatus marks the session terminal after cancellation is processed.
func (r *AgentSessionReconciler) applyCancellationStatus(session *scrutineerv1alpha1.AgentSession) {
	const msg = "Session cancelled by user request"
	if session.Status.Phase != scrutineerv1alpha1.PhaseCancelled {
		r.recordNormal(session, EventReasonSessionCancelled, msg)
	}
	session.Status.Phase = scrutineerv1alpha1.PhaseCancelled
	setCompletionTime(session)
	setCondition(session, ConditionCompleted, metav1.ConditionTrue, "SessionCancelled", msg)
	if session.Status.Result == nil {
		session.Status.Result = &scrutineerv1alpha1.SessionResult{
			Outcome: "cancelled",
			Summary: msg,
		}
	}
}

// applyObservation maps a backend's normalized observation onto the AgentSession status,
// conditions, events, and result. The reconciler — not the backend — owns this mapping
// so governance/status semantics stay backend-independent.
func (r *AgentSessionReconciler) applyObservation(session *scrutineerv1alpha1.AgentSession, obs observation) {
	if obs.runtimeName != "" {
		session.Status.JobName = obs.runtimeName
	}
	if obs.runtimeRef != nil {
		session.Status.RuntimeRef = obs.runtimeRef
	}

	if obs.replaced {
		r.recordNormal(session, EventReasonPolicyEnvSynced, "Replaced pending Job to sync policy env vars")
	}
	if obs.policyInSync {
		setCondition(session, ConditionPolicyPropagated, metav1.ConditionTrue, "EnvCurrent",
			"Job policy env vars match effective policy")
	} else {
		setCondition(session, ConditionPolicyPropagated, metav1.ConditionFalse, "PolicyEnvDrift", obs.policyMessage)
		r.recordWarning(session, EventReasonPolicyEnvDrift, obs.policyMessage)
	}

	if obs.created {
		if session.Status.StartTime == nil {
			now := metav1.Now()
			session.Status.StartTime = &now
		}
		setCondition(session, ConditionRuntimeCreated, metav1.ConditionTrue, "JobCreated",
			fmt.Sprintf("Created Job %q", obs.runtimeName))
		r.recordNormal(session, EventReasonJobCreated, fmt.Sprintf("Created Job %s", obs.runtimeName))
		session.Status.Phase = scrutineerv1alpha1.PhaseStarting
	} else {
		setCondition(session, ConditionRuntimeCreated, metav1.ConditionTrue, "JobCreated",
			fmt.Sprintf("Job %q exists", obs.runtimeName))
	}

	r.applyRuntimePhase(session, obs.phase)

	if obs.workloadName != "" {
		session.Status.PodName = obs.workloadName
	}
}

// applyRuntimePhase maps a backend-neutral runtimePhase onto AgentSessionPhase, the
// Completed condition, result, and lifecycle events. Terminal sessions are never
// overwritten. Events fire only on a phase transition.
func (r *AgentSessionReconciler) applyRuntimePhase(session *scrutineerv1alpha1.AgentSession, phase runtimePhase) {
	if isTerminal(session.Status.Phase) {
		return
	}

	switch phase {
	case runtimeSucceeded:
		if session.Status.Phase != scrutineerv1alpha1.PhaseSucceeded {
			r.recordNormal(session, EventReasonJobSucceeded, "Job completed successfully")
		}
		session.Status.Phase = scrutineerv1alpha1.PhaseSucceeded
		setCompletionTime(session)
		setCondition(session, ConditionCompleted, metav1.ConditionTrue, "JobSucceeded", "Underlying Job completed successfully")
		if session.Status.Result == nil {
			session.Status.Result = &scrutineerv1alpha1.SessionResult{
				Outcome: "completed",
				Summary: "Job completed successfully",
			}
		}

	case runtimeTimedOut:
		msg := "Underlying Job exceeded its activeDeadlineSeconds"
		if session.Status.Phase != scrutineerv1alpha1.PhaseTimedOut {
			r.recordWarning(session, EventReasonJobFailed, msg)
		}
		session.Status.Phase = scrutineerv1alpha1.PhaseTimedOut
		setCompletionTime(session)
		setCondition(session, ConditionCompleted, metav1.ConditionFalse, "JobTimedOut", msg)
		if session.Status.Result == nil {
			session.Status.Result = &scrutineerv1alpha1.SessionResult{
				Outcome: "failed",
				Summary: msg,
			}
		}

	case runtimeFailed:
		msg := "Underlying Job failed"
		if session.Status.Phase != scrutineerv1alpha1.PhaseFailed {
			r.recordWarning(session, EventReasonJobFailed, msg)
		}
		session.Status.Phase = scrutineerv1alpha1.PhaseFailed
		setCompletionTime(session)
		setCondition(session, ConditionCompleted, metav1.ConditionFalse, "JobFailed", msg)
		if session.Status.Result == nil {
			session.Status.Result = &scrutineerv1alpha1.SessionResult{
				Outcome: "failed",
				Summary: msg,
			}
		}

	case runtimeRunning:
		if session.Status.Phase != scrutineerv1alpha1.PhaseRunning {
			r.recordNormal(session, EventReasonJobRunning, "Job is running")
		}
		session.Status.Phase = scrutineerv1alpha1.PhaseRunning

	default:
		// runtimeStarting / indeterminate: only advance a fresh session.
		if session.Status.Phase == "" || session.Status.Phase == scrutineerv1alpha1.PhasePending {
			session.Status.Phase = scrutineerv1alpha1.PhaseStarting
		}
	}
}

func toJobTask(task *ResolvedTask) *job.Task {
	if task == nil {
		return nil
	}
	return &job.Task{Description: task.Description, Prompt: task.Prompt}
}

func (r *AgentSessionReconciler) recordNormal(session *scrutineerv1alpha1.AgentSession, reason, msg string) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Event(session, corev1.EventTypeNormal, reason, msg)
}

func (r *AgentSessionReconciler) recordWarning(session *scrutineerv1alpha1.AgentSession, reason, msg string) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Event(session, corev1.EventTypeWarning, reason, msg)
}

// SetupWithManager wires the reconciler into the controller-runtime manager.
func (r *AgentSessionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.RESTConfig = mgr.GetConfig()
	r.ensureBackends()
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&scrutineerv1alpha1.AgentSession{}).
		Owns(&networkingv1.NetworkPolicy{}).
		// Per-session egress-proxy objects (see egress_envoy.go); a deleted one is
		// re-provisioned on the next reconcile.
		Owns(&corev1.Service{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.ConfigMap{})
	// Watch each registered backend's runtime object so completions trigger reconciles,
	// deduping in case two backends ever share an owned type.
	ownedSeen := map[string]struct{}{}
	for _, backend := range r.backends {
		owned := backend.ownedType()
		key := fmt.Sprintf("%T", owned)
		if _, dup := ownedSeen[key]; dup {
			continue
		}
		ownedSeen[key] = struct{}{}
		builder = builder.Owns(owned)
	}
	return builder.
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(r.mapPodToSessions),
		).
		Watches(
			&scrutineerv1alpha1.AgentPolicy{},
			handler.EnqueueRequestsFromMapFunc(r.mapAgentPolicyToSessions),
		).
		Watches(
			&scrutineerv1alpha1.ToolPolicy{},
			handler.EnqueueRequestsFromMapFunc(r.mapToolPolicyToSessions),
		).
		Watches(
			&scrutineerv1alpha1.RuntimeProfile{},
			handler.EnqueueRequestsFromMapFunc(r.mapRuntimeProfileToSessions),
		).
		Watches(
			&scrutineerv1alpha1.ApprovalRequest{},
			handler.EnqueueRequestsFromMapFunc(r.mapApprovalRequestToSessions),
		).
		Complete(r)
}

// conditionChanged reports whether condType on current differs from the
// reconcile-start snapshot — newly added, or its status/reason/message changed.
// Resolution events (PolicyResolved, RuntimeProfileResolved) are gated on this so
// they are emitted once per real transition instead of on every requeue, which
// otherwise spams Events on a single unchanged resourceVersion.
func conditionChanged(snapshot, current *scrutineerv1alpha1.AgentSession, condType string) bool {
	cur := meta.FindStatusCondition(current.Status.Conditions, condType)
	if cur == nil {
		return false
	}
	prev := meta.FindStatusCondition(snapshot.Status.Conditions, condType)
	if prev == nil {
		return true
	}
	return prev.Status != cur.Status || prev.Reason != cur.Reason || prev.Message != cur.Message
}

// setCondition upserts a condition by Type onto the AgentSession status.
func setCondition(session *scrutineerv1alpha1.AgentSession, condType string, status metav1.ConditionStatus, reason, msg string) {
	meta.SetStatusCondition(&session.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: session.Generation,
	})
}

func setCompletionTime(session *scrutineerv1alpha1.AgentSession) {
	if session.Status.CompletionTime == nil {
		now := metav1.Now()
		session.Status.CompletionTime = &now
	}
}

func setReadyCondition(session *scrutineerv1alpha1.AgentSession) {
	phase := session.Status.Phase
	if phase == "" {
		phase = scrutineerv1alpha1.PhasePending
	}

	switch phase {
	case scrutineerv1alpha1.PhaseRunning:
		setCondition(session, ConditionReady, metav1.ConditionTrue, "JobRunning", "Underlying Job is running")
	case scrutineerv1alpha1.PhaseSucceeded:
		setCondition(session, ConditionReady, metav1.ConditionTrue, "JobSucceeded", "Underlying Job completed successfully")
	case scrutineerv1alpha1.PhaseDenied:
		setCondition(session, ConditionReady, metav1.ConditionFalse, "SessionDenied", "Session was denied by validation or policy")
	case scrutineerv1alpha1.PhaseFailed:
		setCondition(session, ConditionReady, metav1.ConditionFalse, "JobFailed", "Underlying Job failed")
	case scrutineerv1alpha1.PhaseTimedOut:
		setCondition(session, ConditionReady, metav1.ConditionFalse, "JobTimedOut", "Underlying Job exceeded its activeDeadlineSeconds")
	case scrutineerv1alpha1.PhaseCancelled:
		setCondition(session, ConditionReady, metav1.ConditionFalse, "SessionCancelled", "Session was cancelled by user request")
	case scrutineerv1alpha1.PhaseAwaitingApproval:
		setCondition(session, ConditionReady, metav1.ConditionFalse, "AwaitingApproval", "Session is blocked on a human approval gate")
	default:
		// Pending/Starting and any unknown phase.
		setCondition(session, ConditionReady, metav1.ConditionFalse, "NotReady", "Session is not ready yet")
	}
}

func setBlockOwnerDeletion(obj metav1.Object, block bool) {
	refs := obj.GetOwnerReferences()
	for i := range refs {
		refs[i].BlockOwnerDeletion = &block
	}
	obj.SetOwnerReferences(refs)
}

func isTerminal(phase scrutineerv1alpha1.AgentSessionPhase) bool {
	switch phase {
	case scrutineerv1alpha1.PhaseSucceeded,
		scrutineerv1alpha1.PhaseFailed,
		scrutineerv1alpha1.PhaseDenied,
		scrutineerv1alpha1.PhaseTimedOut,
		scrutineerv1alpha1.PhaseCancelled:
		return true
	}
	return false
}
