/*
Copyright 2026 The Relay Authors.

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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

type stubVerifier struct {
	identity CallerIdentity
	err      error
}

func (s stubVerifier) Verify(_ context.Context, _ *http.Request, _ SessionRef) (CallerIdentity, error) {
	if s.err != nil {
		return CallerIdentity{}, s.err
	}
	return s.identity, nil
}

func TestHandler_acceptsDenyReport(t *testing.T) {
	ts := metav1.NewTime(time.Unix(100, 0))
	session := &relayv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Name: "sess-a", Namespace: "ns1"},
		Status: relayv1alpha1.AgentSessionStatus{
			EffectivePolicy: &relayv1alpha1.EffectivePolicyStatus{
				Mode: relayv1alpha1.PolicyModeEnforced,
			},
		},
	}
	cl := newFakeClient(session)

	h := &Handler{
		Writer:   cl.Status(),
		Reader:   cl,
		Verifier: stubVerifier{identity: CallerIdentity{Namespace: "ns1", PodName: "pod-a"}},
		Now:      func() time.Time { return ts.Time },
	}

	body := ReportRequest{
		Session: SessionRef{Namespace: "ns1", Name: "sess-a"},
		Backend: "egress-proxy",
		Decisions: []relayv1alpha1.PolicyDecision{{
			Time:    ts,
			Phase:   relayv1alpha1.PolicyDecisionPhaseRuntime,
			Type:    "network",
			Action:  relayv1alpha1.PolicyDecisionDeny,
			Reason:  "DeniedDomain",
			Target:  "evil.example.com",
			Message: "egress blocked",
		}},
	}
	rec := postReport(t, h, body, "Bearer test-token")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
	}

	var updated relayv1alpha1.AgentSession
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns1", Name: "sess-a"}, &updated); err != nil {
		t.Fatal(err)
	}
	if len(updated.Status.PolicyDecisions) != 1 {
		t.Fatalf("decisions = %d", len(updated.Status.PolicyDecisions))
	}
	if updated.Status.PolicyDecisions[0].Phase != relayv1alpha1.PolicyDecisionPhaseRuntime {
		t.Fatalf("phase = %s", updated.Status.PolicyDecisions[0].Phase)
	}
	if len(updated.Status.Violations) != 1 {
		t.Fatalf("violations = %d", len(updated.Status.Violations))
	}
	if updated.Status.Violations[0].Target != "evil.example.com" {
		t.Fatalf("violation = %+v", updated.Status.Violations[0])
	}
}

func TestHandler_acceptsNetworkEvent(t *testing.T) {
	ts := metav1.NewTime(time.Unix(150, 0))
	session := &relayv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Name: "sess-ev", Namespace: "ns1"},
	}
	cl := newFakeClient(session)
	h := &Handler{
		Writer:   cl.Status(),
		Reader:   cl,
		Verifier: stubVerifier{identity: CallerIdentity{Namespace: "ns1", PodName: "pod-a"}},
		Now:      func() time.Time { return ts.Time },
	}
	rec := postReport(t, h, ReportRequest{
		Session: SessionRef{Namespace: "ns1", Name: "sess-ev"},
		Backend: "egress-proxy",
		Events: []relayv1alpha1.SessionEvent{{
			Time:    ts,
			Type:    relayv1alpha1.SessionEventTypeNetwork,
			Action:  "deny",
			Target:  "evil.example.com",
			Message: "egress blocked",
			EventID: "evt-net-1",
		}},
	}, "Bearer test-token")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
	}
	var updated relayv1alpha1.AgentSession
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns1", Name: "sess-ev"}, &updated); err != nil {
		t.Fatal(err)
	}
	if len(updated.Status.Events) != 1 {
		t.Fatalf("events = %d", len(updated.Status.Events))
	}
	if updated.Status.Events[0].EventID != "evt-net-1" {
		t.Fatalf("event = %+v", updated.Status.Events[0])
	}
}

func TestHandler_rejectsUnauthorized(t *testing.T) {
	session := &relayv1alpha1.AgentSession{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"}}
	cl := newFakeClient(session)
	h := &Handler{
		Writer:   cl.Status(),
		Reader:   cl,
		Verifier: stubVerifier{err: ErrUnauthorized},
	}
	rec := postReport(t, h, ReportRequest{
		Session: SessionRef{Namespace: "ns", Name: "s"},
		Backend: "egress-proxy",
		Decisions: []relayv1alpha1.PolicyDecision{{
			Phase:  relayv1alpha1.PolicyDecisionPhaseRuntime,
			Type:   "network",
			Action: relayv1alpha1.PolicyDecisionDeny,
			Reason: "DeniedDomain",
		}},
	}, "Bearer bad")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	var updated relayv1alpha1.AgentSession
	_ = cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "s"}, &updated)
	if len(updated.Status.PolicyDecisions) != 0 {
		t.Fatalf("expected no decisions, got %d", len(updated.Status.PolicyDecisions))
	}
}

func TestHandler_rejectsForbidden(t *testing.T) {
	session := &relayv1alpha1.AgentSession{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"}}
	cl := newFakeClient(session)
	h := &Handler{
		Writer:   cl.Status(),
		Reader:   cl,
		Verifier: stubVerifier{err: ErrForbidden},
	}
	rec := postReport(t, h, ReportRequest{
		Session: SessionRef{Namespace: "ns", Name: "s"},
		Backend: "egress-proxy",
		Decisions: []relayv1alpha1.PolicyDecision{{
			Phase:  relayv1alpha1.PolicyDecisionPhaseRuntime,
			Type:   "network",
			Action: relayv1alpha1.PolicyDecisionDeny,
			Reason: "DeniedDomain",
		}},
	}, "Bearer ok")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHandler_rejectsNonRuntimePhase(t *testing.T) {
	session := &relayv1alpha1.AgentSession{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"}}
	cl := newFakeClient(session)
	h := &Handler{
		Writer:   cl.Status(),
		Reader:   cl,
		Verifier: stubVerifier{identity: CallerIdentity{Namespace: "ns", PodName: "p"}},
	}
	rec := postReport(t, h, ReportRequest{
		Session: SessionRef{Namespace: "ns", Name: "s"},
		Backend: "egress-proxy",
		Decisions: []relayv1alpha1.PolicyDecision{{
			Phase:  relayv1alpha1.PolicyDecisionPhaseMerge,
			Type:   "network",
			Action: relayv1alpha1.PolicyDecisionDeny,
			Reason: "DeniedDomain",
		}},
	}, "Bearer ok")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
	}
}

func TestHandler_usageOnlyReportIdIdempotent(t *testing.T) {
	ts := metav1.NewTime(time.Unix(300, 0))
	session := &relayv1alpha1.AgentSession{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"}}
	cl := newFakeClient(session)
	cache := newReportIDCache(time.Hour)
	h := &Handler{
		Writer:    cl.Status(),
		Reader:    cl,
		Verifier:  stubVerifier{identity: CallerIdentity{Namespace: "ns", PodName: "p"}},
		ReportIDs: cache,
		Now:       func() time.Time { return ts.Time },
	}
	body := ReportRequest{
		Session:  SessionRef{Namespace: "ns", Name: "s"},
		Backend:  "agent",
		ReportID: "usage-batch-1",
		Usage:    &relayv1alpha1.SessionUsage{InputTokens: 100, OutputTokens: 25},
	}
	if rec := postReport(t, h, body, "Bearer ok"); rec.Code != http.StatusAccepted {
		t.Fatalf("first status = %d", rec.Code)
	}
	if rec := postReport(t, h, body, "Bearer ok"); rec.Code != http.StatusAccepted {
		t.Fatalf("second status = %d", rec.Code)
	}

	var updated relayv1alpha1.AgentSession
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "s"}, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.Usage == nil || updated.Status.Usage.InputTokens != 100 || updated.Status.Usage.OutputTokens != 25 {
		t.Fatalf("usage = %+v", updated.Status.Usage)
	}
}

func TestHandler_usageOnlyWithoutReportIdDoubleCounts(t *testing.T) {
	ts := metav1.NewTime(time.Unix(301, 0))
	session := &relayv1alpha1.AgentSession{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"}}
	cl := newFakeClient(session)
	h := &Handler{
		Writer:   cl.Status(),
		Reader:   cl,
		Verifier: stubVerifier{identity: CallerIdentity{Namespace: "ns", PodName: "p"}},
		Now:      func() time.Time { return ts.Time },
	}
	body := ReportRequest{
		Session: SessionRef{Namespace: "ns", Name: "s"},
		Backend: "agent",
		Usage:   &relayv1alpha1.SessionUsage{InputTokens: 10},
	}
	postReport(t, h, body, "Bearer ok")
	postReport(t, h, body, "Bearer ok")

	var updated relayv1alpha1.AgentSession
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "s"}, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.Usage == nil || updated.Status.Usage.InputTokens != 20 {
		t.Fatalf("without reportId usage should double-count, got %+v", updated.Status.Usage)
	}
}

func TestHandler_idempotentRedelivery(t *testing.T) {
	ts := metav1.NewTime(time.Unix(200, 0))
	session := &relayv1alpha1.AgentSession{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"}}
	cl := newFakeClient(session)
	h := &Handler{
		Writer:   cl.Status(),
		Reader:   cl,
		Verifier: stubVerifier{identity: CallerIdentity{Namespace: "ns", PodName: "p"}},
		Now:      func() time.Time { return ts.Time },
	}
	body := ReportRequest{
		Session: SessionRef{Namespace: "ns", Name: "s"},
		Backend: "egress-proxy",
		Decisions: []relayv1alpha1.PolicyDecision{{
			Time:    ts,
			Phase:   relayv1alpha1.PolicyDecisionPhaseRuntime,
			Type:    "tool",
			Action:  relayv1alpha1.PolicyDecisionDeny,
			Reason:  "DeniedTools",
			Target:  "bash",
			Message: "tool denied",
		}},
	}
	if rec := postReport(t, h, body, "Bearer ok"); rec.Code != http.StatusAccepted {
		t.Fatalf("first status = %d", rec.Code)
	}
	if rec := postReport(t, h, body, "Bearer ok"); rec.Code != http.StatusAccepted {
		t.Fatalf("second status = %d", rec.Code)
	}
	var updated relayv1alpha1.AgentSession
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "s"}, &updated); err != nil {
		t.Fatal(err)
	}
	if len(updated.Status.PolicyDecisions) != 1 {
		t.Fatalf("decisions = %d, want 1", len(updated.Status.PolicyDecisions))
	}
	if len(updated.Status.Violations) != 1 {
		t.Fatalf("violations = %d, want 1", len(updated.Status.Violations))
	}
}

func TestValidateAndNormalizeReport_rejectsTooManyDecisions(t *testing.T) {
	decisions := make([]relayv1alpha1.PolicyDecision, MaxDecisionsPerReport+1)
	for i := range decisions {
		decisions[i] = relayv1alpha1.PolicyDecision{
			Phase:  relayv1alpha1.PolicyDecisionPhaseRuntime,
			Type:   "network",
			Action: relayv1alpha1.PolicyDecisionDeny,
			Reason: "DeniedDomain",
		}
	}
	_, err := ValidateAndNormalizeReport(ReportRequest{
		Session:   SessionRef{Namespace: "ns", Name: "s"},
		Backend:   "egress-proxy",
		Decisions: decisions,
	}, time.Now(), relayv1alpha1.PolicyModeEnforced)
	if err == nil {
		t.Fatal("expected error")
	}
}

func postReport(t *testing.T, h *Handler, body ReportRequest, auth string) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, reportPath, bytes.NewReader(b))
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func newFakeClient(objs ...client.Object) client.Client {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = relayv1alpha1.AddToScheme(s)
	builder := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&relayv1alpha1.AgentSession{}, &relayv1alpha1.ApprovalRequest{})
	for _, obj := range objs {
		builder = builder.WithObjects(obj)
	}
	return builder.Build()
}
