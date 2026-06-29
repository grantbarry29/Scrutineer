/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	"context"
	"encoding/json"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

func TestStampDecidedBy(t *testing.T) {
	cases := []struct {
		name        string
		decision    scrutineerv1alpha1.ApprovalDecision
		oldDecision scrutineerv1alpha1.ApprovalDecision
		decidedBy   string
		username    string
		wantChanged bool
		wantBy      string
	}{
		{
			name:        "pending decision is not stamped (controller create)",
			decision:    scrutineerv1alpha1.ApprovalDecisionPending,
			username:    "system:serviceaccount:scrutineer-system:scrutineer-controller",
			wantChanged: false,
			wantBy:      "",
		},
		{
			name:        "fresh grant stamps authenticated user, ignoring client value",
			decision:    scrutineerv1alpha1.ApprovalDecisionGranted,
			decidedBy:   "mallory",
			username:    "alice@example.com",
			wantChanged: true,
			wantBy:      "alice@example.com",
		},
		{
			name:        "deny transition from pending stamps the denier",
			decision:    scrutineerv1alpha1.ApprovalDecisionDenied,
			oldDecision: scrutineerv1alpha1.ApprovalDecisionPending,
			username:    "bob",
			wantChanged: true,
			wantBy:      "bob",
		},
		{
			name:        "no-op resubmit with matching authenticated decidedBy does not re-stamp",
			decision:    scrutineerv1alpha1.ApprovalDecisionGranted,
			oldDecision: scrutineerv1alpha1.ApprovalDecisionGranted,
			decidedBy:   "alice",
			username:    "alice",
			wantChanged: false,
			wantBy:      "alice",
		},
		{
			name:        "spoofed decidedBy on an unchanged decision is corrected",
			decision:    scrutineerv1alpha1.ApprovalDecisionGranted,
			oldDecision: scrutineerv1alpha1.ApprovalDecisionGranted,
			decidedBy:   "mallory",
			username:    "alice",
			wantChanged: true,
			wantBy:      "alice",
		},
		{
			name:        "empty username (should be unreachable post-authn) leaves object untouched",
			decision:    scrutineerv1alpha1.ApprovalDecisionGranted,
			decidedBy:   "mallory",
			username:    "",
			wantChanged: false,
			wantBy:      "mallory",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obj := &scrutineerv1alpha1.ApprovalRequest{
				Spec: scrutineerv1alpha1.ApprovalRequestSpec{
					Decision:  tc.decision,
					DecidedBy: tc.decidedBy,
				},
			}
			changed := stampDecidedBy(obj, tc.oldDecision, tc.username)
			if changed != tc.wantChanged {
				t.Fatalf("changed = %v, want %v", changed, tc.wantChanged)
			}
			if obj.Spec.DecidedBy != tc.wantBy {
				t.Fatalf("decidedBy = %q, want %q", obj.Spec.DecidedBy, tc.wantBy)
			}
		})
	}
}

func newStamper(t *testing.T) *ApprovalRequestIdentityStamper {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := scrutineerv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	return &ApprovalRequestIdentityStamper{decoder: admission.NewDecoder(scheme)}
}

func mustRaw(t *testing.T, ar *scrutineerv1alpha1.ApprovalRequest) []byte {
	t.Helper()
	ar.TypeMeta = metav1.TypeMeta{APIVersion: "scrutineer.sh/v1alpha1", Kind: "ApprovalRequest"}
	b, err := json.Marshal(ar)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestHandle_grantStampsAuthenticatedUser(t *testing.T) {
	s := newStamper(t)
	incoming := &scrutineerv1alpha1.ApprovalRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "sess", Namespace: "team-a"},
		Spec: scrutineerv1alpha1.ApprovalRequestSpec{
			SessionRef: scrutineerv1alpha1.ApprovalSessionRef{Name: "sess"},
			Action:     "deploy",
			Decision:   scrutineerv1alpha1.ApprovalDecisionGranted,
			DecidedBy:  "mallory", // spoof attempt
		},
	}
	req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Operation: admissionv1.Create,
		UserInfo:  authenticationv1.UserInfo{Username: "alice@example.com"},
		Object:    runtime.RawExtension{Raw: mustRaw(t, incoming)},
	}}

	resp := s.Handle(context.Background(), req)
	if !resp.Allowed {
		t.Fatalf("response not allowed: %+v", resp.Result)
	}
	if len(resp.Patches) == 0 {
		t.Fatalf("expected a patch stamping decidedBy, got none")
	}
	// The JSON patch must set decidedBy to the authenticated identity.
	var foundDecidedBy bool
	for _, p := range resp.Patches {
		if p.Path == "/spec/decidedBy" {
			foundDecidedBy = true
			if p.Value != "alice@example.com" {
				t.Fatalf("patched decidedBy = %v, want alice@example.com", p.Value)
			}
		}
	}
	if !foundDecidedBy {
		t.Fatalf("no /spec/decidedBy patch in %+v", resp.Patches)
	}
}

func TestHandle_pendingCreateIsNoOp(t *testing.T) {
	s := newStamper(t)
	incoming := &scrutineerv1alpha1.ApprovalRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "sess", Namespace: "team-a"},
		Spec: scrutineerv1alpha1.ApprovalRequestSpec{
			SessionRef: scrutineerv1alpha1.ApprovalSessionRef{Name: "sess"},
			Action:     "deploy",
			Decision:   scrutineerv1alpha1.ApprovalDecisionPending,
		},
	}
	req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Operation: admissionv1.Create,
		UserInfo:  authenticationv1.UserInfo{Username: "system:serviceaccount:scrutineer-system:scrutineer-controller"},
		Object:    runtime.RawExtension{Raw: mustRaw(t, incoming)},
	}}

	resp := s.Handle(context.Background(), req)
	if !resp.Allowed {
		t.Fatalf("response not allowed: %+v", resp.Result)
	}
	if len(resp.Patches) != 0 {
		t.Fatalf("controller create must not be patched, got %+v", resp.Patches)
	}
}
