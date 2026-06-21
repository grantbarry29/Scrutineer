/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package reporter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	relayjob "github.com/secureai/relay/internal/controller/job"
)

func TestHandler_rejectsNonPostAndBadPath(t *testing.T) {
	h := &Handler{Verifier: stubVerifier{}}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, reportPath, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/other", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bad path status = %d", rec.Code)
	}
}

func TestHandler_rejectsInvalidJSON(t *testing.T) {
	h := &Handler{Verifier: stubVerifier{}}
	req := httptest.NewRequest(http.MethodPost, reportPath, strings.NewReader("{not json"))
	req.Header.Set("Authorization", "Bearer x")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHandler_rejectsOversizePayload(t *testing.T) {
	h := &Handler{Verifier: stubVerifier{}}
	// Valid JSON whose oversized string value forces the decoder past the byte cap.
	big := strings.Repeat("a", MaxReportBytes+1024)
	payload := `{"session":{"namespace":"ns","name":"s"},"backend":"` + big + `"}`
	req := httptest.NewRequest(http.MethodPost, reportPath, strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer x")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHandler_notFoundSession(t *testing.T) {
	cl := newFakeClient()
	h := &Handler{
		Writer:   cl.Status(),
		Reader:   cl,
		Verifier: stubVerifier{identity: CallerIdentity{Namespace: "ns", PodName: "p"}},
	}
	rec := postReport(t, h, ReportRequest{
		Session: SessionRef{Namespace: "ns", Name: "missing"},
		Backend: "egress-proxy",
		Decisions: []relayv1alpha1.PolicyDecision{{
			Phase: relayv1alpha1.PolicyDecisionPhaseRuntime, Type: "network",
			Action: relayv1alpha1.PolicyDecisionDeny, Reason: "DeniedDomain",
		}},
	}, "Bearer ok")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHandler_rateLimited(t *testing.T) {
	ts := metav1.NewTime(time.Unix(500, 0))
	session := &relayv1alpha1.AgentSession{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"}}
	cl := newFakeClient(session)
	h := &Handler{
		Writer:   cl.Status(),
		Reader:   cl,
		Verifier: stubVerifier{identity: CallerIdentity{Namespace: "ns", PodName: "p"}},
		Limiter:  newSessionRateLimiter(time.Minute),
		Now:      func() time.Time { return ts.Time },
	}
	body := ReportRequest{
		Session: SessionRef{Namespace: "ns", Name: "s"},
		Backend: "egress-proxy",
		Decisions: []relayv1alpha1.PolicyDecision{{
			Time: ts, Phase: relayv1alpha1.PolicyDecisionPhaseRuntime, Type: "network",
			Action: relayv1alpha1.PolicyDecisionDeny, Reason: "DeniedDomain", Target: "a",
		}},
	}
	if rec := postReport(t, h, body, "Bearer ok"); rec.Code != http.StatusAccepted {
		t.Fatalf("first = %d", rec.Code)
	}
	rec := postReport(t, h, body, "Bearer ok")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second = %d (want 429)", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After header")
	}
}

func TestValidateAndNormalizeReport_fillsAndValidates(t *testing.T) {
	now := time.Unix(1000, 0)
	future := metav1.NewTime(now.Add(time.Hour))

	// Violation time fill + future-skew clamp on decisions.
	report, err := ValidateAndNormalizeReport(ReportRequest{
		Session: SessionRef{Namespace: "ns", Name: "s"},
		Backend: "egress-proxy",
		Decisions: []relayv1alpha1.PolicyDecision{{
			Time: future, Type: "network", Action: relayv1alpha1.PolicyDecisionDeny, Reason: "x",
			// A malicious client tries to self-attest a higher assurance level;
			// the controller-side reporter must override it.
			AssuranceLevel: relayv1alpha1.EvidenceObserved,
		}},
		Violations: []relayv1alpha1.PolicyViolation{{Type: "network", Message: "m"}},
	}, now, relayv1alpha1.PolicyModeEnforced)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decisions[0].Time.Time.After(now.Add(maxFutureSkew)) {
		t.Fatal("future decision time not clamped")
	}
	if report.Decisions[0].Actor != "egress-proxy" {
		t.Fatalf("actor default = %q", report.Decisions[0].Actor)
	}
	if report.Decisions[0].Mode != relayv1alpha1.PolicyModeEnforced {
		t.Fatalf("mode override = %q", report.Decisions[0].Mode)
	}
	if report.Decisions[0].AssuranceLevel != relayv1alpha1.EvidenceSelfReported {
		t.Fatalf("decision assurance = %q, want self-reported (client value must be overridden)", report.Decisions[0].AssuranceLevel)
	}
	if report.Violations[0].Time.IsZero() {
		t.Fatal("violation time not filled")
	}
	if report.Violations[0].AssuranceLevel != relayv1alpha1.EvidenceSelfReported {
		t.Fatalf("violation assurance = %q, want self-reported", report.Violations[0].AssuranceLevel)
	}
}

func TestValidateAndNormalizeReport_rejectsBadInput(t *testing.T) {
	now := time.Unix(1, 0)
	cases := []struct {
		name string
		req  ReportRequest
	}{
		{"missing session", ReportRequest{Backend: "b", Decisions: []relayv1alpha1.PolicyDecision{{Type: "network"}}}},
		{"missing backend", ReportRequest{Session: SessionRef{Namespace: "n", Name: "s"}, Decisions: []relayv1alpha1.PolicyDecision{{Type: "network"}}}},
		{"empty report", ReportRequest{Session: SessionRef{Namespace: "n", Name: "s"}, Backend: "b"}},
		{"event missing type", ReportRequest{
			Session: SessionRef{Namespace: "n", Name: "s"}, Backend: "b",
			Events: []relayv1alpha1.SessionEvent{{Message: "no type"}},
		}},
		{"too many events", ReportRequest{
			Session: SessionRef{Namespace: "n", Name: "s"}, Backend: "b",
			Events: make([]relayv1alpha1.SessionEvent, MaxEventsPerReport+1),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateAndNormalizeReport(tc.req, now, ""); err == nil {
				t.Fatal("expected error")
			} else if !errors.Is(err, ErrBadRequest) {
				t.Fatalf("want ErrBadRequest, got %v", err)
			}
		})
	}
}

func TestValidateAndNormalizeReport_acceptsUsageOnlyReport(t *testing.T) {
	now := time.Unix(1, 0)
	report, err := ValidateAndNormalizeReport(ReportRequest{
		Session: SessionRef{Namespace: "ns", Name: "s"},
		Backend: "agent",
		Usage:   &relayv1alpha1.SessionUsage{InputTokens: 10, OutputTokens: 5},
	}, now, "")
	if err != nil {
		t.Fatal(err)
	}
	if report.Usage == nil || report.Usage.InputTokens != 10 {
		t.Fatalf("usage = %+v", report.Usage)
	}
}

func TestValidateAndNormalizeReport_pinsTimesToSecondPrecision(t *testing.T) {
	// Sub-second precision must be dropped so the apiserver round-trip (which
	// stores RFC3339 second precision) keeps dedup keys stable on re-delivery.
	received := time.Unix(1_700_000_000, 123_456_789)
	report, err := ValidateAndNormalizeReport(ReportRequest{
		Session:   SessionRef{Namespace: "ns", Name: "s"},
		Backend:   "egress-proxy",
		Decisions: []relayv1alpha1.PolicyDecision{{Type: "network", Action: relayv1alpha1.PolicyDecisionDeny, Reason: "x"}},
		Events:    []relayv1alpha1.SessionEvent{{Type: relayv1alpha1.SessionEventTypeNetwork}},
	}, received, "")
	if err != nil {
		t.Fatal(err)
	}
	if report.Decisions[0].Time.Time.Nanosecond() != 0 {
		t.Fatalf("decision time has sub-second precision: %v", report.Decisions[0].Time)
	}
	if report.Events[0].Time.Time.Nanosecond() != 0 {
		t.Fatalf("event time has sub-second precision: %v", report.Events[0].Time)
	}
}

func TestKubeIdentityVerifier_Verify_happyPath(t *testing.T) {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: relayjob.NamePrefix + "s", Namespace: "ns1",
			Labels: map[string]string{relayjob.LabelSessionRef: "s"},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pod-a", Namespace: "ns1",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: batchv1.SchemeGroupVersion.String(), Kind: "Job", Name: job.Name,
			}},
		},
		Spec: corev1.PodSpec{ServiceAccountName: "default"},
	}
	cl := newAuthClient(t, true, "pod-a", []corev1.Pod{*pod}, []batchv1.Job{*job})

	v := &KubeIdentityVerifier{Client: cl, Reader: cl, Audience: TokenAudience}
	req := httptest.NewRequest(http.MethodPost, reportPath, nil)
	req.Header.Set("Authorization", "Bearer good-token")
	id, err := v.Verify(context.Background(), req, SessionRef{Namespace: "ns1", Name: "s"})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if id.PodName != "pod-a" {
		t.Fatalf("pod = %q", id.PodName)
	}
}

func TestKubeIdentityVerifier_Verify_unauthenticated(t *testing.T) {
	cl := newAuthClient(t, false, "", nil, nil)
	v := &KubeIdentityVerifier{Client: cl, Reader: cl, Audience: TokenAudience}
	req := httptest.NewRequest(http.MethodPost, reportPath, nil)
	req.Header.Set("Authorization", "Bearer bad")
	_, err := v.Verify(context.Background(), req, SessionRef{Namespace: "ns1", Name: "s"})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestKubeIdentityVerifier_Verify_missingToken(t *testing.T) {
	cl := newAuthClient(t, true, "pod-a", nil, nil)
	v := &KubeIdentityVerifier{Client: cl, Reader: cl, Audience: TokenAudience}
	req := httptest.NewRequest(http.MethodPost, reportPath, nil)
	_, err := v.Verify(context.Background(), req, SessionRef{Namespace: "ns1", Name: "s"})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestServer_startAndShutdown(t *testing.T) {
	cl := newFakeClient(&relayv1alpha1.AgentSession{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"}})
	run := NewRunnable(Options{
		BindAddress: "127.0.0.1:18099",
		Client:      cl,
		APIReader:   cl,
		Verifier:    stubVerifier{identity: CallerIdentity{Namespace: "ns", PodName: "p"}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run.Start(ctx) }()

	// Wait for listener.
	var resp *http.Response
	var err error
	for i := 0; i < 50; i++ {
		body := `{"session":{"namespace":"ns","name":"s"},"backend":"egress-proxy","decisions":[{"phase":"runtime","type":"network","action":"deny","reason":"r"}]}`
		resp, err = http.Post("http://127.0.0.1:18099/v1/report", "application/json", strings.NewReader(body))
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down")
	}
}

// newAuthClient builds a fake client whose TokenReview Create is intercepted to
// return a fixed authentication result, plus optional pods/jobs for the ownership walk.
func newAuthClient(t *testing.T, authenticated bool, podName string, pods []corev1.Pod, jobs []batchv1.Job) client.Client {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	objs := make([]client.Object, 0, len(pods)+len(jobs))
	for i := range pods {
		objs = append(objs, &pods[i])
	}
	for i := range jobs {
		objs = append(objs, &jobs[i])
	}
	return fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if tr, ok := obj.(*authenticationv1.TokenReview); ok {
					tr.Status.Authenticated = authenticated
					if !authenticated {
						tr.Status.Error = "invalid token"
						return nil
					}
					tr.Status.User = authenticationv1.UserInfo{
						Username: "system:serviceaccount:ns1:default",
					}
					if podName != "" {
						tr.Status.User.Extra = map[string]authenticationv1.ExtraValue{
							podNameExtraKey: {podName},
						}
					}
					return nil
				}
				return c.Create(ctx, obj, opts...)
			},
		}).
		Build()
}
