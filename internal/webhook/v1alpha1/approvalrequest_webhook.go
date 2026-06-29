/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package v1alpha1 holds admission webhooks for scrutineer.sh/v1alpha1
// objects. The ApprovalRequest webhook closes Phase 5 open questions #1/#3 by
// capturing the *authenticated* approver identity at admission time so a grant
// cannot be attributed to someone who did not make it.
package v1alpha1

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// approvalRequestWebhookPath is the admission path served by the manager and
// referenced from the generated MutatingWebhookConfiguration.
const approvalRequestWebhookPath = "/mutate-scrutineer-sh-v1alpha1-approvalrequest"

// +kubebuilder:webhook:path=/mutate-scrutineer-sh-v1alpha1-approvalrequest,mutating=true,failurePolicy=fail,sideEffects=None,groups=scrutineer.sh,resources=approvalrequests,verbs=create;update,versions=v1alpha1,name=mapprovalrequest.scrutineer.sh,admissionReviewVersions=v1

// ApprovalRequestIdentityStamper is a mutating admission webhook that overwrites
// ApprovalRequest spec.decidedBy with the apiserver-authenticated identity of the
// requester whenever a human asserts or changes a decision (spec.decision becomes
// granted/denied). This makes decidedBy non-spoofable: the value an approver (or
// CLI/UI) sends is ignored in favor of req.UserInfo, so the approver-allowlist and
// allOf coverage checks in the controller act on a trustworthy identity.
//
// It is deliberately a no-op for the controller's own writes: the controller
// creates the request with an empty decision and only mutates status, neither of
// which triggers stamping. Status updates never carry a decision transition.
type ApprovalRequestIdentityStamper struct {
	decoder admission.Decoder
}

// SetupApprovalRequestWebhookWithManager registers the identity-stamping webhook
// on the manager's webhook server. It is wired only when webhooks are enabled.
func SetupApprovalRequestWebhookWithManager(mgr ctrl.Manager) error {
	mgr.GetWebhookServer().Register(approvalRequestWebhookPath, &admission.Webhook{
		Handler: &ApprovalRequestIdentityStamper{decoder: admission.NewDecoder(mgr.GetScheme())},
	})
	return nil
}

// Handle stamps the authenticated identity onto a decision-bearing write.
func (s *ApprovalRequestIdentityStamper) Handle(_ context.Context, req admission.Request) admission.Response {
	obj := &scrutineerv1alpha1.ApprovalRequest{}
	if err := s.decoder.Decode(req, obj); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	var oldDecision scrutineerv1alpha1.ApprovalDecision
	if req.Operation == admissionv1.Update && len(req.OldObject.Raw) > 0 {
		old := &scrutineerv1alpha1.ApprovalRequest{}
		if err := s.decoder.DecodeRaw(req.OldObject, old); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		oldDecision = old.Spec.Decision
	}

	if !stampDecidedBy(obj, oldDecision, req.UserInfo.Username) {
		return admission.Allowed("no decision asserted; decidedBy unchanged")
	}

	marshaled, err := json.Marshal(obj)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	return admission.PatchResponseFromRaw(req.Object.Raw, marshaled)
}

// stampDecidedBy sets obj.Spec.DecidedBy to the authenticated username when this
// admission asserts a (new or changed) non-empty decision. It returns whether the
// object was modified. The username is the only trustworthy attribution available
// at admission time; the client-supplied decidedBy is always overwritten.
//
// Stamping fires when the decision is non-empty AND either it is a fresh decision
// (create, or a transition from a different prior decision) or the client tried to
// set a decidedBy that disagrees with the authenticated user (anti-spoof on no-op
// re-submits). It never blanks decidedBy when there is no authenticated username
// (e.g. anonymous requests the apiserver should already have rejected).
func stampDecidedBy(obj *scrutineerv1alpha1.ApprovalRequest, oldDecision scrutineerv1alpha1.ApprovalDecision, username string) bool {
	username = strings.TrimSpace(username)
	if username == "" {
		return false
	}
	if obj.Spec.Decision == scrutineerv1alpha1.ApprovalDecisionPending {
		return false
	}
	decisionChanged := obj.Spec.Decision != oldDecision
	if !decisionChanged && obj.Spec.DecidedBy == username {
		return false
	}
	obj.Spec.DecidedBy = username
	return true
}
