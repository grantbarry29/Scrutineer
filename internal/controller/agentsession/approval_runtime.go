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
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/audit"
)

// reconcileRuntimeApprovals resolves the lifecycle of mid-execution, per-tool
// ApprovalRequests (spec.trigger=runtime) that reference this session, WITHOUT
// touching the session phase. The session keeps running/starting; only the
// individual held tool call is gated by its own ApprovalRequest.
//
// The controller is the sole writer of ApprovalRequest.status. It resolves a
// human decision (granted/denied), enforces the policy's approver allowlist and
// allOf requirement, applies the decision deadline (onTimeout), and emits an
// audit record for the human decision. Session-level runtime evidence
// (status.policyDecisions) is populated via the reporter by whichever data-plane
// component holds the call (dormant until the out-of-pod tools chokepoint lands;
// see docs/design/tools-pod-chokepoint.md), not here — the controller only records
// the authoritative human decision into the audit sink and the ApprovalRequest status.
func (r *AgentSessionReconciler) reconcileRuntimeApprovals(ctx context.Context, session *scrutineerv1alpha1.AgentSession) error {
	var list scrutineerv1alpha1.ApprovalRequestList
	if err := r.List(ctx, &list, client.InNamespace(session.Namespace)); err != nil {
		return fmt.Errorf("list ApprovalRequests: %w", err)
	}
	var pending []scrutineerv1alpha1.RuntimeApprovalSummary
	for i := range list.Items {
		req := &list.Items[i]
		if req.Spec.SessionRef.Name != session.Name || !req.Spec.IsRuntime() {
			continue
		}
		// A decided request is final; the gateway re-checks expiry at consume time.
		if approvalStateDecided(req.Status.State) {
			continue
		}
		if err := r.reconcileRuntimeApproval(ctx, session, req); err != nil {
			return err
		}
		// Surface only holds still awaiting a human after this pass; a request
		// that just resolved (e.g. onTimeout) must not linger in the summary.
		if !approvalStateDecided(req.Status.State) {
			pending = append(pending, runtimeApprovalSummary(req))
		}
	}
	setPendingApprovals(session, pending)
	return nil
}

// maxPendingApprovals bounds status.pendingApprovals so a chatty/hostile agent
// cannot grow the session object unboundedly. It mirrors the API MaxItems cap;
// the reporter independently caps outstanding holds per session well below this.
const maxPendingApprovals = 64

// runtimeApprovalSummary projects an ApprovalRequest into the redaction-safe
// status view (argDigest only — never raw arguments).
func runtimeApprovalSummary(req *scrutineerv1alpha1.ApprovalRequest) scrutineerv1alpha1.RuntimeApprovalSummary {
	state := req.Status.State
	if state == "" {
		state = scrutineerv1alpha1.ApprovalStatePending
	}
	return scrutineerv1alpha1.RuntimeApprovalSummary{
		Name:        req.Name,
		RequestID:   req.Spec.RequestID,
		Action:      req.Spec.Action,
		Target:      runtimeApprovalTarget(req),
		ArgDigest:   req.Spec.Scope.ArgDigest,
		State:       state,
		PolicyRef:   req.Spec.PolicyRef,
		RequestedAt: req.CreationTimestamp.DeepCopy(),
		Reason:      req.Status.Reason,
	}
}

// setPendingApprovals sorts (for stable, idempotent status patches), caps, and
// assigns the outstanding-hold summary. An empty list clears the field.
func setPendingApprovals(session *scrutineerv1alpha1.AgentSession, pending []scrutineerv1alpha1.RuntimeApprovalSummary) {
	if len(pending) == 0 {
		session.Status.PendingApprovals = nil
		return
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].Name < pending[j].Name })
	if len(pending) > maxPendingApprovals {
		pending = pending[:maxPendingApprovals]
	}
	session.Status.PendingApprovals = pending
}

// reconcileRuntimeApproval drives a single runtime ApprovalRequest from its
// current human decision to a controller-observed state.
func (r *AgentSessionReconciler) reconcileRuntimeApproval(ctx context.Context, session *scrutineerv1alpha1.AgentSession, req *scrutineerv1alpha1.ApprovalRequest) error {
	gatePolicy := r.lookupApprovalPolicy(ctx, session.Namespace, req.Spec.PolicyRef)
	target := runtimeApprovalTarget(req)
	firstObservation := req.Status.State == ""

	switch req.Spec.Decision {
	case scrutineerv1alpha1.ApprovalDecisionGranted:
		// Honor a grant only from a listed approver. DecidedBy is authenticated
		// when the identity webhook is enabled (--enable-webhooks); otherwise it
		// is self-declared and the real boundary is RBAC on patching the request.
		if gatePolicy != nil && !approverAllowed(gatePolicy, req.Spec.DecidedBy) {
			_ = r.setApprovalRequestState(ctx, req, scrutineerv1alpha1.ApprovalStatePending, gatePolicy,
				"granted by an unlisted approver; awaiting an authorized decision")
			r.recordWarning(session, EventReasonApprovalUnauthorized,
				fmt.Sprintf("ApprovalRequest %q grant from %q is not a listed approver of policy %q; not honored",
					req.Name, req.Spec.DecidedBy, gatePolicy.Name))
			return nil
		}
		// allOf: hold until every listed approver has granted.
		if gatePolicy != nil && requiresAllOf(gatePolicy) {
			if err := r.recordApprover(ctx, req, req.Spec.DecidedBy); err != nil {
				return err
			}
			if remaining := remainingApprovers(gatePolicy, req); len(remaining) > 0 {
				all := approverNames(gatePolicy)
				_ = r.setApprovalRequestState(ctx, req, scrutineerv1alpha1.ApprovalStatePending, gatePolicy,
					fmt.Sprintf("allOf: awaiting %d more approver(s): %s", len(remaining), strings.Join(remaining, ", ")))
				r.recordNormal(session, EventReasonApprovalPartial,
					fmt.Sprintf("Approval %d of %d for tool %q; awaiting %s",
						len(all)-len(remaining), len(all), target, strings.Join(remaining, ", ")))
				return nil
			}
		}
		if err := r.setApprovalRequestState(ctx, req, scrutineerv1alpha1.ApprovalStateGranted, gatePolicy, "decision granted"); err != nil {
			return err
		}
		r.recordNormal(session, EventReasonApprovalGranted,
			fmt.Sprintf("Runtime approval granted for tool %q (ApprovalRequest %q)", target, req.Name))
		r.auditRuntimeApprovalDecision(ctx, session, req, target, true, "ApprovalGranted")
		return nil

	case scrutineerv1alpha1.ApprovalDecisionDenied:
		if err := r.setApprovalRequestState(ctx, req, scrutineerv1alpha1.ApprovalStateDenied, gatePolicy, "decision denied"); err != nil {
			return err
		}
		r.recordWarning(session, EventReasonApprovalDenied,
			fmt.Sprintf("Runtime approval denied for tool %q (ApprovalRequest %q)", target, req.Name))
		r.auditRuntimeApprovalDecision(ctx, session, req, target, false, "ApprovalDenied")
		return nil

	default: // pending
		if gatePolicy != nil {
			if expired, onTimeout := approvalTimedOut(req, gatePolicy); expired {
				if onTimeout == scrutineerv1alpha1.ApprovalTimeoutAllow {
					if err := r.setApprovalRequestState(ctx, req, scrutineerv1alpha1.ApprovalStateGranted, gatePolicy, "timed out; onTimeout=allow"); err != nil {
						return err
					}
					r.auditRuntimeApprovalDecision(ctx, session, req, target, true, "ApprovalTimeoutAllow")
					return nil
				}
				if err := r.setApprovalRequestState(ctx, req, scrutineerv1alpha1.ApprovalStateExpired, gatePolicy, "timed out; onTimeout=deny"); err != nil {
					return err
				}
				r.recordWarning(session, EventReasonApprovalDenied,
					fmt.Sprintf("Runtime approval for tool %q timed out (ApprovalRequest %q)", target, req.Name))
				r.auditRuntimeApprovalDecision(ctx, session, req, target, false, "ApprovalExpired")
				return nil
			}
		}
		_ = r.setApprovalRequestState(ctx, req, scrutineerv1alpha1.ApprovalStatePending, gatePolicy, "awaiting decision")
		if firstObservation {
			r.recordNormal(session, EventReasonApprovalRequested,
				fmt.Sprintf("Awaiting runtime approval for tool %q (ApprovalRequest %q)", target, req.Name))
		}
		r.notifyApprovalRequest(ctx, session, req)
		return nil
	}
}

// auditRuntimeApprovalDecision emits the authoritative human-decision record for a
// runtime hold to the audit sink. It is called once per request because decided
// requests are skipped on subsequent reconciles. The actor is the deciding
// approver(s); session-level (self-reported) evidence is the gateway's job.
func (r *AgentSessionReconciler) auditRuntimeApprovalDecision(ctx context.Context, session *scrutineerv1alpha1.AgentSession, req *scrutineerv1alpha1.ApprovalRequest, target string, granted bool, reason string) {
	audit.Emit(ctx, audit.ApprovalDecision(session.Namespace, session.Name, target,
		runtimeApprovalActor(req), reason, granted, time.Now()))
}

// runtimeApprovalActor is the best-effort approver identity for a runtime decision.
func runtimeApprovalActor(req *scrutineerv1alpha1.ApprovalRequest) string {
	if len(req.Status.ApprovedBy) > 0 {
		return strings.Join(req.Status.ApprovedBy, ",")
	}
	if by := strings.TrimSpace(req.Status.DecidedBy); by != "" {
		return by
	}
	if by := strings.TrimSpace(req.Spec.DecidedBy); by != "" {
		return by
	}
	return "scrutineer-controller"
}

// runtimeApprovalTarget is the human-readable subject of a runtime hold: the
// scoped tool target if set, else the action.
func runtimeApprovalTarget(req *scrutineerv1alpha1.ApprovalRequest) string {
	if t := strings.TrimSpace(req.Spec.Scope.Target); t != "" {
		return t
	}
	return req.Spec.Action
}

// lookupApprovalPolicy fetches the named ApprovalPolicy, returning nil when the
// name is empty or the policy is absent (runtime holds may be policy-less, in
// which case RBAC is the only approver gate and there is no decision deadline).
func (r *AgentSessionReconciler) lookupApprovalPolicy(ctx context.Context, namespace, name string) *scrutineerv1alpha1.ApprovalPolicy {
	if name == "" {
		return nil
	}
	var p scrutineerv1alpha1.ApprovalPolicy
	if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &p); err != nil {
		if !apierrors.IsNotFound(err) {
			// Treat transient errors as "no policy" for this pass; the request stays
			// pending and a later reconcile retries. Avoids failing the whole loop.
			return nil
		}
		return nil
	}
	return &p
}

// approvalStateDecided reports whether a request has reached a final state.
func approvalStateDecided(s scrutineerv1alpha1.ApprovalState) bool {
	switch s {
	case scrutineerv1alpha1.ApprovalStateGranted,
		scrutineerv1alpha1.ApprovalStateDenied,
		scrutineerv1alpha1.ApprovalStateExpired:
		return true
	}
	return false
}
