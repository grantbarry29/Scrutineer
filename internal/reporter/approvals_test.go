/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package reporter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

func newApprovalSession(ns, name string) *scrutineerv1alpha1.AgentSession {
	return &scrutineerv1alpha1.AgentSession{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}
}

func postApproval(t *testing.T, h *ApprovalHandler, body ApprovalRegisterRequest) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, approvalsPath, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func getApprovalHTTP(t *testing.T, h *ApprovalHandler, id, namespace string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, approvalsPrefix+id+"?namespace="+namespace, nil)
	req.Header.Set("Authorization", "Bearer test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeApproval(t *testing.T, rec *httptest.ResponseRecorder) ApprovalResponse {
	t.Helper()
	var resp ApprovalResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return resp
}

func TestApprovalHandler_registerCreatesPendingAndIsIdempotent(t *testing.T) {
	cl := newFakeClient(newApprovalSession("ns1", "sess-a"))
	h := &ApprovalHandler{Client: cl, Reader: cl, Verifier: stubVerifier{identity: CallerIdentity{Namespace: "ns1", PodName: "p"}}}

	body := ApprovalRegisterRequest{
		Session:   SessionRef{Namespace: "ns1", Name: "sess-a"},
		RequestID: "call-123",
		Action:    "deploy",
		Target:    "deploy-prod",
		ArgDigest: "sha256:abc",
		Window:    "15m",
	}

	rec := postApproval(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("first status = %d body=%q", rec.Code, rec.Body.String())
	}
	first := decodeApproval(t, rec)
	if first.State != string(scrutineerv1alpha1.ApprovalStatePending) {
		t.Fatalf("state = %q, want Pending", first.State)
	}
	if first.ApprovalID == "" {
		t.Fatal("approvalId empty")
	}

	rec2 := postApproval(t, h, body)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second status = %d", rec2.Code)
	}
	second := decodeApproval(t, rec2)
	if second.ApprovalID != first.ApprovalID {
		t.Fatalf("idempotency: ids differ %q vs %q", first.ApprovalID, second.ApprovalID)
	}

	var list scrutineerv1alpha1.ApprovalRequestList
	if err := cl.List(context.Background(), &list, client.InNamespace("ns1")); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 ApprovalRequest, got %d", len(list.Items))
	}
	ar := list.Items[0]
	if !ar.Spec.IsRuntime() {
		t.Fatal("created request should be a runtime trigger")
	}
	if ar.Spec.RequestID != "call-123" || ar.Spec.Scope.ArgDigest != "sha256:abc" || ar.Spec.Scope.Window == nil {
		t.Fatalf("spec not propagated: %+v", ar.Spec)
	}
}

func TestApprovalHandler_lookupReturnsControllerState(t *testing.T) {
	name := RuntimeApprovalName("sess-a", "call-9")
	ar := &scrutineerv1alpha1.ApprovalRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns1"},
		Spec: scrutineerv1alpha1.ApprovalRequestSpec{
			SessionRef: scrutineerv1alpha1.ApprovalSessionRef{Name: "sess-a"},
			Trigger:    scrutineerv1alpha1.ApprovalTriggerRuntime,
			RequestID:  "call-9",
			Action:     "deploy",
		},
		Status: scrutineerv1alpha1.ApprovalRequestStatus{State: scrutineerv1alpha1.ApprovalStateGranted},
	}
	cl := newFakeClient(newApprovalSession("ns1", "sess-a"), ar)
	h := &ApprovalHandler{Client: cl, Reader: cl, Verifier: stubVerifier{identity: CallerIdentity{Namespace: "ns1", PodName: "p"}}}

	rec := getApprovalHTTP(t, h, name, "ns1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
	}
	resp := decodeApproval(t, rec)
	if resp.State != string(scrutineerv1alpha1.ApprovalStateGranted) {
		t.Fatalf("state = %q, want Granted", resp.State)
	}
}

func TestApprovalHandler_rejectsUnauthorizedRegister(t *testing.T) {
	cl := newFakeClient(newApprovalSession("ns1", "sess-a"))
	h := &ApprovalHandler{Client: cl, Reader: cl, Verifier: stubVerifier{err: ErrUnauthorized}}
	rec := postApproval(t, h, ApprovalRegisterRequest{
		Session: SessionRef{Namespace: "ns1", Name: "sess-a"}, RequestID: "c1", Action: "deploy",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	var list scrutineerv1alpha1.ApprovalRequestList
	_ = cl.List(context.Background(), &list, client.InNamespace("ns1"))
	if len(list.Items) != 0 {
		t.Fatalf("unauthorized register must not create requests, got %d", len(list.Items))
	}
}

func TestApprovalHandler_rejectsForbiddenCrossSessionLookup(t *testing.T) {
	name := RuntimeApprovalName("sess-a", "call-x")
	ar := &scrutineerv1alpha1.ApprovalRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns1"},
		Spec:       scrutineerv1alpha1.ApprovalRequestSpec{SessionRef: scrutineerv1alpha1.ApprovalSessionRef{Name: "sess-a"}, Trigger: scrutineerv1alpha1.ApprovalTriggerRuntime, Action: "deploy"},
	}
	cl := newFakeClient(ar)
	h := &ApprovalHandler{Client: cl, Reader: cl, Verifier: stubVerifier{err: ErrForbidden}}
	rec := getApprovalHTTP(t, h, name, "ns1")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestApprovalHandler_badRequestMissingFields(t *testing.T) {
	cl := newFakeClient(newApprovalSession("ns1", "sess-a"))
	h := &ApprovalHandler{Client: cl, Reader: cl, Verifier: stubVerifier{identity: CallerIdentity{Namespace: "ns1", PodName: "p"}}}
	// Missing requestId and action.
	rec := postApproval(t, h, ApprovalRegisterRequest{Session: SessionRef{Namespace: "ns1", Name: "sess-a"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestApprovalHandler_notFoundSession(t *testing.T) {
	cl := newFakeClient() // no session
	h := &ApprovalHandler{Client: cl, Reader: cl, Verifier: stubVerifier{identity: CallerIdentity{Namespace: "ns1", PodName: "p"}}}
	rec := postApproval(t, h, ApprovalRegisterRequest{
		Session: SessionRef{Namespace: "ns1", Name: "ghost"}, RequestID: "c1", Action: "deploy",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
	}
}

func TestApprovalHandler_lookupNotFound(t *testing.T) {
	cl := newFakeClient()
	h := &ApprovalHandler{Client: cl, Reader: cl, Verifier: stubVerifier{identity: CallerIdentity{Namespace: "ns1", PodName: "p"}}}
	rec := getApprovalHTTP(t, h, "missing-rt-deadbeef", "ns1")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestApprovalHandler_outstandingCapRejectsNewHolds(t *testing.T) {
	cl := newFakeClient(newApprovalSession("ns1", "sess-a"))
	h := &ApprovalHandler{
		Client: cl, Reader: cl,
		Verifier:       stubVerifier{identity: CallerIdentity{Namespace: "ns1", PodName: "p"}},
		MaxOutstanding: 2,
	}

	// Two distinct holds fit under the cap.
	for _, id := range []string{"c1", "c2"} {
		rec := postApproval(t, h, ApprovalRegisterRequest{
			Session: SessionRef{Namespace: "ns1", Name: "sess-a"}, RequestID: id, Action: "deploy",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("hold %s: status = %d body=%q", id, rec.Code, rec.Body.String())
		}
	}

	// A third NEW hold is over the cap.
	rec := postApproval(t, h, ApprovalRegisterRequest{
		Session: SessionRef{Namespace: "ns1", Name: "sess-a"}, RequestID: "c3", Action: "deploy",
	})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third hold status = %d, want 429; body=%q", rec.Code, rec.Body.String())
	}

	// Re-registering an EXISTING hold is exempt from the cap (keepalive path).
	rec = postApproval(t, h, ApprovalRegisterRequest{
		Session: SessionRef{Namespace: "ns1", Name: "sess-a"}, RequestID: "c1", Action: "deploy",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("re-register existing hold status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}

	// A decided hold no longer counts: grant c1, then a new hold fits again.
	grantHold(t, cl, RuntimeApprovalName("sess-a", "c1"))
	rec = postApproval(t, h, ApprovalRegisterRequest{
		Session: SessionRef{Namespace: "ns1", Name: "sess-a"}, RequestID: "c3", Action: "deploy",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("new hold after a grant status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
}

func TestApprovalHandler_rateLimitsNewHolds(t *testing.T) {
	cl := newFakeClient(newApprovalSession("ns1", "sess-a"))
	frozen := time.Unix(1_700_000_000, 0)
	h := &ApprovalHandler{
		Client: cl, Reader: cl,
		Verifier: stubVerifier{identity: CallerIdentity{Namespace: "ns1", PodName: "p"}},
		Limiter:  newSessionRateLimiter(time.Second, 1),
		Now:      func() time.Time { return frozen },
	}

	rec := postApproval(t, h, ApprovalRegisterRequest{
		Session: SessionRef{Namespace: "ns1", Name: "sess-a"}, RequestID: "c1", Action: "deploy",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("first hold status = %d", rec.Code)
	}
	// A second NEW hold at the same instant is throttled.
	rec = postApproval(t, h, ApprovalRegisterRequest{
		Session: SessionRef{Namespace: "ns1", Name: "sess-a"}, RequestID: "c2", Action: "deploy",
	})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second hold status = %d, want 429; body=%q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After on rate-limited register")
	}
	// Re-registering the first (existing) hold is exempt from the limiter.
	rec = postApproval(t, h, ApprovalRegisterRequest{
		Session: SessionRef{Namespace: "ns1", Name: "sess-a"}, RequestID: "c1", Action: "deploy",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("re-register existing hold status = %d, want 200", rec.Code)
	}
}

func grantHold(t *testing.T, cl client.Client, name string) {
	t.Helper()
	var ar scrutineerv1alpha1.ApprovalRequest
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "ns1", Name: name}, &ar); err != nil {
		t.Fatal(err)
	}
	ar.Status.State = scrutineerv1alpha1.ApprovalStateGranted
	if err := cl.Status().Update(context.Background(), &ar); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeApprovalName_deterministicAndBounded(t *testing.T) {
	a := RuntimeApprovalName("sess", "req-1")
	b := RuntimeApprovalName("sess", "req-1")
	c := RuntimeApprovalName("sess", "req-2")
	if a != b {
		t.Fatalf("name should be deterministic: %q vs %q", a, b)
	}
	if a == c {
		t.Fatal("different requestIds should yield different names")
	}
	long := RuntimeApprovalName(string(make([]byte, 400)), "req-1")
	if len(long) > 253 {
		t.Fatalf("name exceeds DNS-1123 limit: %d", len(long))
	}
	// Sanity: a known key resolves via the same helper used by the handler.
	if got := RuntimeApprovalName("sess-a", "call-9"); !bytes.HasPrefix([]byte(got), []byte("sess-a-rt-")) {
		t.Fatalf("unexpected name shape: %q", got)
	}
}
