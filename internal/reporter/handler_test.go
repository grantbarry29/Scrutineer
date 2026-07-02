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
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
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
	session := &scrutineerv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Name: "sess-a", Namespace: "ns1"},
		Status: scrutineerv1alpha1.AgentSessionStatus{
			EffectivePolicy: &scrutineerv1alpha1.EffectivePolicyStatus{
				Mode: scrutineerv1alpha1.PolicyModeEnforced,
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
		Decisions: []scrutineerv1alpha1.PolicyDecision{{
			Time:    ts,
			Phase:   scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
			Type:    "network",
			Action:  scrutineerv1alpha1.PolicyDecisionDeny,
			Reason:  "DeniedDomain",
			Target:  "evil.example.com",
			Message: "egress blocked",
		}},
	}
	rec := postReport(t, h, body, "Bearer test-token")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
	}

	var updated scrutineerv1alpha1.AgentSession
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns1", Name: "sess-a"}, &updated); err != nil {
		t.Fatal(err)
	}
	if len(updated.Status.PolicyDecisions) != 1 {
		t.Fatalf("decisions = %d", len(updated.Status.PolicyDecisions))
	}
	if updated.Status.PolicyDecisions[0].Phase != scrutineerv1alpha1.PolicyDecisionPhaseRuntime {
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
	session := &scrutineerv1alpha1.AgentSession{
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
		Events: []scrutineerv1alpha1.SessionEvent{{
			Time:    ts,
			Type:    scrutineerv1alpha1.SessionEventTypeNetwork,
			Action:  "deny",
			Target:  "evil.example.com",
			Message: "egress blocked",
			EventID: "evt-net-1",
		}},
	}, "Bearer test-token")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
	}
	var updated scrutineerv1alpha1.AgentSession
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
	session := &scrutineerv1alpha1.AgentSession{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"}}
	cl := newFakeClient(session)
	h := &Handler{
		Writer:   cl.Status(),
		Reader:   cl,
		Verifier: stubVerifier{err: ErrUnauthorized},
	}
	rec := postReport(t, h, ReportRequest{
		Session: SessionRef{Namespace: "ns", Name: "s"},
		Backend: "egress-proxy",
		Decisions: []scrutineerv1alpha1.PolicyDecision{{
			Phase:  scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
			Type:   "network",
			Action: scrutineerv1alpha1.PolicyDecisionDeny,
			Reason: "DeniedDomain",
		}},
	}, "Bearer bad")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	var updated scrutineerv1alpha1.AgentSession
	_ = cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "s"}, &updated)
	if len(updated.Status.PolicyDecisions) != 0 {
		t.Fatalf("expected no decisions, got %d", len(updated.Status.PolicyDecisions))
	}
}

func TestHandler_rejectsForbidden(t *testing.T) {
	session := &scrutineerv1alpha1.AgentSession{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"}}
	cl := newFakeClient(session)
	h := &Handler{
		Writer:   cl.Status(),
		Reader:   cl,
		Verifier: stubVerifier{err: ErrForbidden},
	}
	rec := postReport(t, h, ReportRequest{
		Session: SessionRef{Namespace: "ns", Name: "s"},
		Backend: "egress-proxy",
		Decisions: []scrutineerv1alpha1.PolicyDecision{{
			Phase:  scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
			Type:   "network",
			Action: scrutineerv1alpha1.PolicyDecisionDeny,
			Reason: "DeniedDomain",
		}},
	}, "Bearer ok")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHandler_rejectsNonRuntimePhase(t *testing.T) {
	session := &scrutineerv1alpha1.AgentSession{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"}}
	cl := newFakeClient(session)
	h := &Handler{
		Writer:   cl.Status(),
		Reader:   cl,
		Verifier: stubVerifier{identity: CallerIdentity{Namespace: "ns", PodName: "p"}},
	}
	rec := postReport(t, h, ReportRequest{
		Session: SessionRef{Namespace: "ns", Name: "s"},
		Backend: "egress-proxy",
		Decisions: []scrutineerv1alpha1.PolicyDecision{{
			Phase:  scrutineerv1alpha1.PolicyDecisionPhaseMerge,
			Type:   "network",
			Action: scrutineerv1alpha1.PolicyDecisionDeny,
			Reason: "DeniedDomain",
		}},
	}, "Bearer ok")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
	}
}

func TestHandler_usageOnlyReportIdIdempotent(t *testing.T) {
	ts := metav1.NewTime(time.Unix(300, 0))
	session := &scrutineerv1alpha1.AgentSession{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"}}
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
		Usage:    &scrutineerv1alpha1.SessionUsage{InputTokens: 100, OutputTokens: 25},
	}
	if rec := postReport(t, h, body, "Bearer ok"); rec.Code != http.StatusAccepted {
		t.Fatalf("first status = %d", rec.Code)
	}
	if rec := postReport(t, h, body, "Bearer ok"); rec.Code != http.StatusAccepted {
		t.Fatalf("second status = %d", rec.Code)
	}

	var updated scrutineerv1alpha1.AgentSession
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "s"}, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.Usage == nil || updated.Status.Usage.InputTokens != 100 || updated.Status.Usage.OutputTokens != 25 {
		t.Fatalf("usage = %+v", updated.Status.Usage)
	}
}

func TestHandler_usageOnlyWithoutReportIdDoubleCounts(t *testing.T) {
	ts := metav1.NewTime(time.Unix(301, 0))
	session := &scrutineerv1alpha1.AgentSession{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"}}
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
		Usage:   &scrutineerv1alpha1.SessionUsage{InputTokens: 10},
	}
	postReport(t, h, body, "Bearer ok")
	postReport(t, h, body, "Bearer ok")

	var updated scrutineerv1alpha1.AgentSession
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "s"}, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.Usage == nil || updated.Status.Usage.InputTokens != 20 {
		t.Fatalf("without reportId usage should double-count, got %+v", updated.Status.Usage)
	}
}

func TestHandler_idempotentRedelivery(t *testing.T) {
	ts := metav1.NewTime(time.Unix(200, 0))
	session := &scrutineerv1alpha1.AgentSession{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"}}
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
		Decisions: []scrutineerv1alpha1.PolicyDecision{{
			Time:    ts,
			Phase:   scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
			Type:    "tool",
			Action:  scrutineerv1alpha1.PolicyDecisionDeny,
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
	var updated scrutineerv1alpha1.AgentSession
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

// Regression for #37: a RuntimeViolation event must be emitted for every report
// carrying a violating decision, even after the session already has prior
// violations. The old gate keyed off the prior violation count and suppressed
// events for the second and later violating reports.
func TestHandler_emitsViolationEventForEachViolatingReport(t *testing.T) {
	ts := metav1.NewTime(time.Unix(400, 0))
	session := &scrutineerv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Name: "sess-v", Namespace: "ns1"},
		Status: scrutineerv1alpha1.AgentSessionStatus{
			EffectivePolicy: &scrutineerv1alpha1.EffectivePolicyStatus{
				Mode: scrutineerv1alpha1.PolicyModeEnforced,
			},
		},
	}
	cl := newFakeClient(session)
	rec := record.NewFakeRecorder(10)
	h := &Handler{
		Writer:   cl.Status(),
		Reader:   cl,
		Verifier: stubVerifier{identity: CallerIdentity{Namespace: "ns1", PodName: "pod-a"}},
		Recorder: rec,
		Now:      func() time.Time { return ts.Time },
	}

	mkReport := func(target string) ReportRequest {
		return ReportRequest{
			Session: SessionRef{Namespace: "ns1", Name: "sess-v"},
			Backend: "egress-proxy",
			Decisions: []scrutineerv1alpha1.PolicyDecision{{
				Time:    ts,
				Phase:   scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
				Type:    "network",
				Action:  scrutineerv1alpha1.PolicyDecisionDeny,
				Reason:  "DeniedDomain",
				Target:  target,
				Message: "egress blocked: " + target,
			}},
		}
	}

	// Two distinct violating reports; the second arrives after the session already
	// has a recorded violation from the first.
	if r := postReport(t, h, mkReport("evil1.example.com"), "Bearer ok"); r.Code != http.StatusAccepted {
		t.Fatalf("first report status = %d body=%q", r.Code, r.Body.String())
	}
	if r := postReport(t, h, mkReport("evil2.example.com"), "Bearer ok"); r.Code != http.StatusAccepted {
		t.Fatalf("second report status = %d body=%q", r.Code, r.Body.String())
	}

	events := drainEvents(rec.Events)
	if len(events) != 2 {
		t.Fatalf("RuntimeViolation events = %d, want 2: %v", len(events), events)
	}
	for _, e := range events {
		if !strings.Contains(e, "RuntimeViolation") {
			t.Fatalf("unexpected event: %q", e)
		}
	}
}

// Regression for #42: two concurrent reports with the same reportId must be
// processed exactly once. Each violating report emits one RuntimeViolation event,
// so exactly-once processing means exactly one event.
func TestHandler_concurrentDuplicateReportIdProcessedOnce(t *testing.T) {
	ts := metav1.NewTime(time.Unix(500, 0))
	session := &scrutineerv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Name: "sess-c", Namespace: "ns1"},
		Status: scrutineerv1alpha1.AgentSessionStatus{
			EffectivePolicy: &scrutineerv1alpha1.EffectivePolicyStatus{
				Mode: scrutineerv1alpha1.PolicyModeEnforced,
			},
		},
	}
	cl := newFakeClient(session)
	rec := record.NewFakeRecorder(10)
	h := &Handler{
		Writer:    cl.Status(),
		Reader:    cl,
		Verifier:  stubVerifier{identity: CallerIdentity{Namespace: "ns1", PodName: "pod-a"}},
		Recorder:  rec,
		ReportIDs: newReportIDCache(time.Hour),
		Now:       func() time.Time { return ts.Time },
	}
	body := ReportRequest{
		Session:  SessionRef{Namespace: "ns1", Name: "sess-c"},
		Backend:  "egress-proxy",
		ReportID: "dup-1",
		Decisions: []scrutineerv1alpha1.PolicyDecision{{
			Time:    ts,
			Phase:   scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
			Type:    "network",
			Action:  scrutineerv1alpha1.PolicyDecisionDeny,
			Reason:  "DeniedDomain",
			Target:  "evil.example.com",
			Message: "egress blocked",
		}},
	}
	// Pre-marshal so the goroutines never call t.Fatal off the test goroutine.
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodPost, reportPath, bytes.NewReader(raw))
			req.Header.Set("Authorization", "Bearer ok")
			req.Header.Set("Content-Type", "application/json")
			h.ServeHTTP(httptest.NewRecorder(), req)
		}()
	}
	close(start)
	wg.Wait()

	if got := len(drainEvents(rec.Events)); got != 1 {
		t.Fatalf("RuntimeViolation events = %d, want 1 (reportId must dedupe under concurrency)", got)
	}
}

func drainEvents(ch <-chan string) []string {
	var out []string
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}

func TestValidateAndNormalizeReport_rejectsTooManyDecisions(t *testing.T) {
	decisions := make([]scrutineerv1alpha1.PolicyDecision, MaxDecisionsPerReport+1)
	for i := range decisions {
		decisions[i] = scrutineerv1alpha1.PolicyDecision{
			Phase:  scrutineerv1alpha1.PolicyDecisionPhaseRuntime,
			Type:   "network",
			Action: scrutineerv1alpha1.PolicyDecisionDeny,
			Reason: "DeniedDomain",
		}
	}
	_, err := ValidateAndNormalizeReport(ReportRequest{
		Session:   SessionRef{Namespace: "ns", Name: "s"},
		Backend:   "egress-proxy",
		Decisions: decisions,
	}, time.Now(), scrutineerv1alpha1.PolicyModeEnforced, scrutineerv1alpha1.EvidenceSelfReported)
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
	_ = scrutineerv1alpha1.AddToScheme(s)
	builder := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&scrutineerv1alpha1.AgentSession{}, &scrutineerv1alpha1.ApprovalRequest{})
	for _, obj := range objs {
		builder = builder.WithObjects(obj)
	}
	return builder.Build()
}
