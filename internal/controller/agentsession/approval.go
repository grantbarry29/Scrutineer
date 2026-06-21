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
	"sort"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/approval"
	"github.com/secureai/relay/internal/policy"
)

// approvalOutcome is the result of evaluating the human-approval gate.
type approvalOutcome int

const (
	// approvalProceed: not gated, or approved — continue to runtime creation.
	approvalProceed approvalOutcome = iota
	// approvalPending: a gate is open — hold the session in AwaitingApproval.
	approvalPending
	// approvalRejected: denied or timed-out-deny — session is terminal Denied.
	approvalRejected
)

// approvalRecheckInterval bounds how often a pending gate re-polls for a decision
// or timeout. Decisions also trigger a reconcile via the ApprovalRequest watch,
// so this is a backstop (and the only driver of onTimeout enforcement).
const approvalRecheckInterval = 15 * time.Second

// reconcileApprovalGate enforces human approval before runtime creation. The gate
// is active only when the effective policy declares requireHumanApproval AND a
// matching ApprovalPolicy exists in the namespace; otherwise approval is declared
// but not enforced (legacy warning) and the session proceeds.
func (r *AgentSessionReconciler) reconcileApprovalGate(ctx context.Context, session *relayv1alpha1.AgentSession, resolved *policy.Resolved) (approvalOutcome, error) {
	actions := resolved.Rules.RequireHumanApproval
	if len(actions) == 0 {
		return approvalProceed, nil
	}
	// Once the runtime exists the gate has already been cleared; never re-block a
	// session that is starting/running.
	if meta.IsStatusConditionTrue(session.Status.Conditions, ConditionRuntimeCreated) {
		return approvalProceed, nil
	}

	gatePolicy, gatedAction, err := r.matchApprovalPolicy(ctx, session.Namespace, actions)
	if err != nil {
		return approvalProceed, fmt.Errorf("match ApprovalPolicy: %w", err)
	}
	if gatePolicy == nil {
		r.recordWarning(session, EventReasonApprovalNotEnforced,
			fmt.Sprintf("requireHumanApproval declared (%v) but no ApprovalPolicy gates it", actions))
		return approvalProceed, nil
	}

	req, err := r.ensureApprovalRequest(ctx, session, gatePolicy, gatedAction)
	if err != nil {
		return approvalProceed, fmt.Errorf("ensure ApprovalRequest: %w", err)
	}

	switch req.Spec.Decision {
	case relayv1alpha1.ApprovalDecisionGranted:
		if err := r.setApprovalRequestState(ctx, req, relayv1alpha1.ApprovalStateGranted, gatePolicy, "decision granted"); err != nil {
			return approvalProceed, err
		}
		if session.Status.Phase == relayv1alpha1.PhaseAwaitingApproval {
			r.recordNormal(session, EventReasonApprovalGranted, fmt.Sprintf("Approval granted for action %q", gatedAction))
		}
		r.recordApprovalDecision(session, relayv1alpha1.PolicyDecisionAllow, gatedAction, req, "ApprovalGranted", "human approval granted")
		setCondition(session, ConditionApprovalRequired, metav1.ConditionFalse, "Approved",
			fmt.Sprintf("ApprovalRequest %q granted", req.Name))
		return approvalProceed, nil

	case relayv1alpha1.ApprovalDecisionDenied:
		_ = r.setApprovalRequestState(ctx, req, relayv1alpha1.ApprovalStateDenied, gatePolicy, "decision denied")
		r.denyForApproval(session, gatedAction, req, "ApprovalDenied", "human approval was denied")
		return approvalRejected, nil

	default: // pending
		if expired, onTimeout := approvalTimedOut(req, gatePolicy); expired {
			if onTimeout == relayv1alpha1.ApprovalTimeoutAllow {
				_ = r.setApprovalRequestState(ctx, req, relayv1alpha1.ApprovalStateGranted, gatePolicy, "timed out; onTimeout=allow")
				r.recordApprovalDecision(session, relayv1alpha1.PolicyDecisionAllow, gatedAction, req, "ApprovalTimeoutAllow", "approval timed out; policy onTimeout=allow")
				setCondition(session, ConditionApprovalRequired, metav1.ConditionFalse, "ApprovalTimeoutAllow",
					"approval timed out; policy onTimeout=allow")
				return approvalProceed, nil
			}
			_ = r.setApprovalRequestState(ctx, req, relayv1alpha1.ApprovalStateExpired, gatePolicy, "timed out; onTimeout=deny")
			r.denyForApproval(session, gatedAction, req, "ApprovalExpired", "approval timed out; policy onTimeout=deny")
			return approvalRejected, nil
		}

		_ = r.setApprovalRequestState(ctx, req, relayv1alpha1.ApprovalStatePending, gatePolicy, "awaiting decision")
		if session.Status.Phase != relayv1alpha1.PhaseAwaitingApproval {
			r.recordNormal(session, EventReasonApprovalRequested,
				fmt.Sprintf("Awaiting approval for action %q (ApprovalRequest %q)", gatedAction, req.Name))
		}
		r.notifyApprovalRequest(ctx, session, req)
		setCondition(session, ConditionApprovalRequired, metav1.ConditionTrue, "AwaitingApproval",
			fmt.Sprintf("Waiting on ApprovalRequest %q", req.Name))
		return approvalPending, nil
	}
}

// matchApprovalPolicy returns the first ApprovalPolicy (ordered by name for
// determinism) whose actions intersect the session's requireHumanApproval set,
// along with the matched action. Returns (nil, "", nil) when none match.
func (r *AgentSessionReconciler) matchApprovalPolicy(ctx context.Context, namespace string, actions []string) (*relayv1alpha1.ApprovalPolicy, string, error) {
	var list relayv1alpha1.ApprovalPolicyList
	if err := r.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, "", err
	}
	want := make(map[string]struct{}, len(actions))
	for _, a := range actions {
		want[a] = struct{}{}
	}
	sort.Slice(list.Items, func(i, j int) bool { return list.Items[i].Name < list.Items[j].Name })
	for i := range list.Items {
		p := &list.Items[i]
		matched := append([]string(nil), p.Spec.Actions...)
		sort.Strings(matched)
		for _, a := range matched {
			if _, ok := want[a]; ok {
				return p, a, nil
			}
		}
	}
	return nil, "", nil
}

// approvalRequestName is the deterministic, 1:1 ApprovalRequest name for a session.
func approvalRequestName(session *relayv1alpha1.AgentSession) string {
	return session.Name
}

// ensureApprovalRequest creates the owned ApprovalRequest for a gated session if
// it does not yet exist, and returns the current object. Idempotent.
func (r *AgentSessionReconciler) ensureApprovalRequest(ctx context.Context, session *relayv1alpha1.AgentSession, gatePolicy *relayv1alpha1.ApprovalPolicy, action string) (*relayv1alpha1.ApprovalRequest, error) {
	key := types.NamespacedName{Namespace: session.Namespace, Name: approvalRequestName(session)}
	var existing relayv1alpha1.ApprovalRequest
	if err := r.Get(ctx, key, &existing); err == nil {
		return &existing, nil
	} else if !apierrors.IsNotFound(err) {
		return nil, err
	}

	req := &relayv1alpha1.ApprovalRequest{
		ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
		Spec: relayv1alpha1.ApprovalRequestSpec{
			SessionRef: relayv1alpha1.ApprovalSessionRef{Name: session.Name},
			PolicyRef:  gatePolicy.Name,
			Action:     action,
			Scope:      relayv1alpha1.ApprovalScope{Window: gatePolicy.Spec.ExpiresAfter},
			Decision:   relayv1alpha1.ApprovalDecisionPending,
		},
	}
	if err := controllerutil.SetControllerReference(session, req, r.Scheme); err != nil {
		return nil, fmt.Errorf("set owner reference on ApprovalRequest: %w", err)
	}
	// Allow GC of the request while the session still exists (mirrors owned Job).
	setBlockOwnerDeletion(req, false)

	if err := r.Create(ctx, req); err != nil {
		if apierrors.IsAlreadyExists(err) {
			if gErr := r.Get(ctx, key, &existing); gErr != nil {
				return nil, gErr
			}
			return &existing, nil
		}
		return nil, err
	}
	return req, nil
}

// setApprovalRequestState updates the ApprovalRequest status subresource when it
// changed. It is a no-op when the observed state already matches.
func (r *AgentSessionReconciler) setApprovalRequestState(ctx context.Context, req *relayv1alpha1.ApprovalRequest, state relayv1alpha1.ApprovalState, gatePolicy *relayv1alpha1.ApprovalPolicy, reason string) error {
	key := client.ObjectKeyFromObject(req)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest relayv1alpha1.ApprovalRequest
		if err := r.Get(ctx, key, &latest); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		decided := state == relayv1alpha1.ApprovalStateGranted ||
			state == relayv1alpha1.ApprovalStateDenied ||
			state == relayv1alpha1.ApprovalStateExpired

		changed := latest.Status.State != state || latest.Status.Reason != reason
		if changed {
			latest.Status.State = state
			latest.Status.Reason = reason
			latest.Status.ObservedGeneration = latest.Generation
			if decided && latest.Status.DecidedAt == nil {
				now := metav1.Now()
				latest.Status.DecidedAt = &now
				if state == relayv1alpha1.ApprovalStateGranted && gatePolicy.Spec.ExpiresAfter != nil {
					exp := metav1.NewTime(now.Add(gatePolicy.Spec.ExpiresAfter.Duration))
					latest.Status.ExpiresAt = &exp
				}
			}
		}
		if !changed {
			return nil
		}
		if err := r.Status().Update(ctx, &latest); err != nil {
			return err
		}
		*req = latest
		return nil
	})
}

// approvalTimedOut reports whether a still-pending request has exceeded the
// policy's decision deadline (expiresAfter from creation), and the configured
// onTimeout action. No deadline means the gate waits indefinitely.
func approvalTimedOut(req *relayv1alpha1.ApprovalRequest, gatePolicy *relayv1alpha1.ApprovalPolicy) (bool, relayv1alpha1.ApprovalTimeoutAction) {
	if gatePolicy.Spec.ExpiresAfter == nil || gatePolicy.Spec.ExpiresAfter.Duration <= 0 {
		return false, gatePolicy.Spec.OnTimeout
	}
	deadline := req.CreationTimestamp.Add(gatePolicy.Spec.ExpiresAfter.Duration)
	return time.Now().After(deadline), gatePolicy.Spec.OnTimeout
}

// denyForApproval marks the session terminally Denied due to an approval outcome.
func (r *AgentSessionReconciler) denyForApproval(session *relayv1alpha1.AgentSession, action string, req *relayv1alpha1.ApprovalRequest, reason, msg string) {
	session.Status.Phase = relayv1alpha1.PhaseDenied
	setCompletionTime(session)
	setCondition(session, ConditionApprovalRequired, metav1.ConditionFalse, reason, msg)
	r.recordApprovalDecision(session, relayv1alpha1.PolicyDecisionDeny, action, req, reason, msg)
	r.recordWarning(session, EventReasonApprovalDenied, msg)
	if session.Status.Result == nil {
		session.Status.Result = &relayv1alpha1.SessionResult{Outcome: "denied", Summary: msg}
	}
}

// recordApprovalDecision appends a control-plane authoritative approval decision
// to status.policyDecisions, idempotent per (action, outcome).
func (r *AgentSessionReconciler) recordApprovalDecision(session *relayv1alpha1.AgentSession, action relayv1alpha1.PolicyDecisionAction, target string, req *relayv1alpha1.ApprovalRequest, reason, msg string) {
	if hasApprovalDecision(session, target, action) {
		return
	}
	actor := "relay-controller"
	if req != nil && req.Status.DecidedBy != "" {
		actor = req.Status.DecidedBy
	}
	AppendRuntimePolicyDecisions(session, []relayv1alpha1.PolicyDecision{{
		Time:           metav1.Now(),
		Phase:          relayv1alpha1.PolicyDecisionPhaseRuntime,
		Type:           "approval",
		Action:         action,
		Actor:          actor,
		Target:         target,
		Reason:         reason,
		Message:        msg,
		AssuranceLevel: relayv1alpha1.EvidenceControllerComputed,
	}})
}

func hasApprovalDecision(session *relayv1alpha1.AgentSession, target string, action relayv1alpha1.PolicyDecisionAction) bool {
	for _, d := range session.Status.PolicyDecisions {
		if d.Type == "approval" && d.Target == target && d.Action == action {
			return true
		}
	}
	return false
}

// approvalNotifiedAnnotation marks an ApprovalRequest whose open-gate notification
// was delivered, making notification fire at most once and retry until it succeeds.
const approvalNotifiedAnnotation = "relay.secureai.dev/approval-notified"

// notifyApprovalRequest delivers a best-effort notification for an open gate. It is
// idempotent (guarded by an annotation) and never fails the reconcile: a delivery
// error leaves the annotation unset so the next pending requeue retries.
func (r *AgentSessionReconciler) notifyApprovalRequest(ctx context.Context, session *relayv1alpha1.AgentSession, req *relayv1alpha1.ApprovalRequest) {
	if r.Notifier == nil {
		return
	}
	if _, done := req.Annotations[approvalNotifiedAnnotation]; done {
		return
	}

	err := r.Notifier.Notify(ctx, approval.Notification{
		Namespace: req.Namespace,
		Name:      req.Name,
		Session:   session.Name,
		Action:    req.Spec.Action,
		PolicyRef: req.Spec.PolicyRef,
		Target:    req.Spec.Scope.Target,
		Message:   fmt.Sprintf("AgentSession %q awaits approval for action %q", session.Name, req.Spec.Action),
	})
	if err != nil {
		r.recordWarning(session, EventReasonApprovalNotifyFailed,
			fmt.Sprintf("Approval notification failed (will retry): %v", err))
		return
	}
	if aErr := r.markApprovalNotified(ctx, req); aErr != nil {
		// Delivery succeeded but the marker did not persist; a retry may re-notify.
		// Surface it rather than silently risking a duplicate.
		r.recordWarning(session, EventReasonApprovalNotifyFailed,
			fmt.Sprintf("Approval notified but marker not persisted (may re-notify): %v", aErr))
		return
	}
	r.recordNormal(session, EventReasonApprovalNotified,
		fmt.Sprintf("Notified approvers for ApprovalRequest %q", req.Name))
}

// markApprovalNotified records that the open-gate notification was delivered.
func (r *AgentSessionReconciler) markApprovalNotified(ctx context.Context, req *relayv1alpha1.ApprovalRequest) error {
	key := client.ObjectKeyFromObject(req)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest relayv1alpha1.ApprovalRequest
		if err := r.Get(ctx, key, &latest); err != nil {
			return err
		}
		if _, done := latest.Annotations[approvalNotifiedAnnotation]; done {
			*req = latest
			return nil
		}
		if latest.Annotations == nil {
			latest.Annotations = map[string]string{}
		}
		latest.Annotations[approvalNotifiedAnnotation] = metav1.Now().UTC().Format(time.RFC3339)
		if err := r.Update(ctx, &latest); err != nil {
			return err
		}
		*req = latest
		return nil
	})
}

// mapApprovalRequestToSessions enqueues the AgentSession a changed ApprovalRequest
// gates so a granted/denied decision resumes reconciliation promptly.
func (r *AgentSessionReconciler) mapApprovalRequestToSessions(_ context.Context, obj client.Object) []reconcile.Request {
	req, ok := obj.(*relayv1alpha1.ApprovalRequest)
	if !ok || req.Spec.SessionRef.Name == "" {
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{Namespace: req.Namespace, Name: req.Spec.SessionRef.Name},
	}}
}
