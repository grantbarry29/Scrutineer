/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	scrutineerjob "github.com/grantbarry29/scrutineer/internal/controller/job"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
	"github.com/grantbarry29/scrutineer/internal/enforcement/lockverify"
	"github.com/grantbarry29/scrutineer/internal/enforcement/networkpolicy"
)

// The lock-probe verifier creates/deletes canary pods and the deny-all probe policy in
// the controller namespace, and lists kube-dns pods to resolve the probe target.
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;delete

// lockVerifyRecheckInterval is how often a session held by the gate re-checks the
// verifier's cached verdict.
const lockVerifyRecheckInterval = 30 * time.Second

// LockVerdictSource provides the cached routing-lock verdict (verified-or-refused
// gate, docs/design/untamperable-enforcement.md §4). *lockverify.Verifier implements it.
type LockVerdictSource interface {
	Current() lockverify.State
}

// egressLockGate applies the verified-or-refused gate to one session and maintains its
// EgressLockVerified condition. hold=true means runtime creation must not proceed this
// pass (enforced mode on an unverified substrate — fail closed).
//
// Scope: sessions whose enforcement substrate is a NetworkPolicy (the Envoy routing
// lock or the legacy CIDR policy). Sessions without one are untouched. Audit/dry-run
// sessions get the condition (their *observation* strength depends on the lock) but
// are never held. When no verifier is wired (reporter-only, unit suites), the gate is
// inert.
func (r *AgentSessionReconciler) egressLockGate(session *scrutineerv1alpha1.AgentSession, profile *scrutineerv1alpha1.RuntimeProfile) bool {
	if r.LockVerifier == nil || session == nil {
		return false
	}
	enfCtx := enforcement.NewSessionContext(session, profile, scrutineerjob.NameFor(session))
	if networkpolicy.Build(enfCtx) == nil {
		return false
	}

	st := r.LockVerifier.Current()
	enforced := enfCtx.Mode == scrutineerv1alpha1.PolicyModeEnforced

	switch st.Verdict {
	case lockverify.VerdictVerified:
		setCondition(session, ConditionEgressLockVerified, metav1.ConditionTrue, ReasonLockVerified,
			"differential probe confirmed the CNI enforces NetworkPolicy; the routing lock is effective")
		return false
	case lockverify.VerdictRefused:
		r.setLockUnverified(session, ReasonCNINotEnforcing,
			"the CNI accepted but did not enforce a deny-all NetworkPolicy; the routing lock is ineffective on this cluster", enforced)
	default:
		r.setLockUnverified(session, ReasonLockProbeInconclusive,
			"no conclusive NetworkPolicy-enforcement probe has completed yet", enforced)
	}
	return enforced
}

// setLockUnverified sets the False condition and emits a warning event only on
// transition (not on every recheck pass).
func (r *AgentSessionReconciler) setLockUnverified(session *scrutineerv1alpha1.AgentSession, reason, msg string, enforced bool) {
	if enforced {
		msg += "; enforced session held (verified-or-refused, docs/design/untamperable-enforcement.md)"
	}
	prev := meta.FindStatusCondition(session.Status.Conditions, ConditionEgressLockVerified)
	changed := prev == nil || prev.Status != metav1.ConditionFalse || prev.Reason != reason
	setCondition(session, ConditionEgressLockVerified, metav1.ConditionFalse, reason, msg)
	if changed && enforced {
		r.recordWarning(session, EventReasonEgressLockUnverified,
			fmt.Sprintf("Runtime creation held: %s", msg))
	}
}
