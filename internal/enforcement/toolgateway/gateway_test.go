/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package toolgateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

func TestGateway_deniesAndReportsEnforcedTool(t *testing.T) {
	reports := make(chan reportRequest, 1)
	reporterSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/report" {
			http.NotFound(w, r)
			return
		}
		var body reportRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		reports <- body
		w.WriteHeader(http.StatusAccepted)
	}))
	defer reporterSrv.Close()

	tokenPath := writeTempToken(t, "test-token")
	env := RuntimeEnv{
		SessionNamespace: "ns1",
		SessionName:      "sess-a",
		ReporterURL:      reporterSrv.URL,
		ReporterToken:    tokenPath,
		Mode:             relayv1alpha1.PolicyModeEnforced,
		Policy: relayv1alpha1.PolicyRules{
			DeniedTools: []string{"kubectl"},
		},
	}

	gw := httptest.NewServer(&Gateway{
		Env:      env,
		Reporter: NewReporterClient(env.ReporterURL, env.ReporterToken, reporterSrv.Client()),
		Now:      func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	defer gw.Close()

	body, _ := json.Marshal(invokeRequest{Tool: "kubectl", RequestID: "req-1"})
	resp, err := http.Post(gw.URL+invokePath, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%q", resp.StatusCode, b)
	}

	select {
	case report := <-reports:
		if len(report.Decisions) != 1 {
			t.Fatalf("decisions = %d", len(report.Decisions))
		}
		if report.Decisions[0].Type != "tool" {
			t.Fatalf("type = %q", report.Decisions[0].Type)
		}
		if report.Decisions[0].Action != relayv1alpha1.PolicyDecisionDeny {
			t.Fatalf("action = %q", report.Decisions[0].Action)
		}
		if len(report.Violations) != 1 {
			t.Fatalf("violations = %d", len(report.Violations))
		}
		if report.Backend != "tool-gateway" {
			t.Fatalf("backend = %q", report.Backend)
		}
		if report.Session.Name != "sess-a" || report.Session.Namespace != "ns1" {
			t.Fatalf("session = %+v", report.Session)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime report")
	}
}

func TestGateway_allowsPermittedTool(t *testing.T) {
	env := RuntimeEnv{
		SessionNamespace: "ns1",
		SessionName:      "sess-a",
		ReporterURL:      "http://unused",
		ReporterToken:    writeTempToken(t, "x"),
		Mode:             relayv1alpha1.PolicyModeEnforced,
		Policy: relayv1alpha1.PolicyRules{
			AllowedTools: []string{"read_file"},
		},
	}
	gw := httptest.NewServer(&Gateway{Env: env})
	defer gw.Close()

	body, _ := json.Marshal(invokeRequest{Tool: "read_file"})
	resp, err := http.Post(gw.URL+invokePath, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%q", resp.StatusCode, b)
	}
}

// approvalStub serves the reporter approval channel + report sink for hold tests.
type approvalStub struct {
	mu        sync.Mutex
	pollState string // state returned by GET /v1/approvals/{id}
	reports   chan reportRequest
}

func (s *approvalStub) handler(registerState string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/report":
			var body reportRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.reports <- body
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/approvals":
			writeJSON(w, approvalResponse{ApprovalID: "appr-1", State: registerState})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/approvals/"):
			s.mu.Lock()
			st := s.pollState
			s.mu.Unlock()
			writeJSON(w, approvalResponse{ApprovalID: "appr-1", State: st})
		default:
			http.NotFound(w, r)
		}
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func newApprovalGateway(t *testing.T, registerState, pollState string, hold, poll time.Duration) (*httptest.Server, *approvalStub) {
	t.Helper()
	stub := &approvalStub{pollState: pollState, reports: make(chan reportRequest, 2)}
	srv := httptest.NewServer(stub.handler(registerState))
	t.Cleanup(srv.Close)

	env := RuntimeEnv{
		SessionNamespace: "ns1",
		SessionName:      "sess-a",
		ReporterURL:      srv.URL,
		ReporterToken:    writeTempToken(t, "tok"),
		Mode:             relayv1alpha1.PolicyModeEnforced,
		Policy:           relayv1alpha1.PolicyRules{RequireHumanApproval: []string{"deploy"}},
	}
	gw := httptest.NewServer(&Gateway{
		Env:                  env,
		Reporter:             NewReporterClient(env.ReporterURL, env.ReporterToken, srv.Client()),
		Now:                  func() time.Time { return time.Unix(1_700_000_000, 0) },
		ApprovalHoldTimeout:  hold,
		ApprovalPollInterval: poll,
	})
	t.Cleanup(gw.Close)
	return gw, stub
}

func postInvoke(t *testing.T, gw *httptest.Server, req invokeRequest) *http.Response {
	t.Helper()
	body, _ := json.Marshal(req)
	resp, err := http.Post(gw.URL+invokePath, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestGateway_approvalGrantedAllowsAndReportsRedacted(t *testing.T) {
	gw, stub := newApprovalGateway(t, "Pending", "Granted", 2*time.Second, 5*time.Millisecond)

	resp := postInvoke(t, gw, invokeRequest{
		Tool:      "deploy",
		Arguments: map[string]any{"target": "prod-cluster-supersecret"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%q", resp.StatusCode, b)
	}

	select {
	case report := <-stub.reports:
		if len(report.Decisions) != 1 {
			t.Fatalf("decisions = %d", len(report.Decisions))
		}
		d := report.Decisions[0]
		if d.Type != "approval" || d.Action != relayv1alpha1.PolicyDecisionAllow || d.Reason != ReasonApprovalGranted {
			t.Fatalf("decision = %+v", d)
		}
		if d.Rule != "requireHumanApproval" {
			t.Fatalf("rule = %q", d.Rule)
		}
		if strings.Contains(d.Message, "prod-cluster-supersecret") {
			t.Fatalf("message leaked raw argument: %q", d.Message)
		}
		if !strings.Contains(d.Message, "argDigest=sha256:") {
			t.Fatalf("message missing argDigest: %q", d.Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for resolved report")
	}
}

func TestGateway_approvalDeniedForbids(t *testing.T) {
	gw, stub := newApprovalGateway(t, "Pending", "Denied", 2*time.Second, 5*time.Millisecond)

	resp := postInvoke(t, gw, invokeRequest{Tool: "deploy"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%q", resp.StatusCode, b)
	}

	select {
	case report := <-stub.reports:
		d := report.Decisions[0]
		if d.Action != relayv1alpha1.PolicyDecisionDeny || d.Reason != ReasonApprovalDenied {
			t.Fatalf("decision = %+v", d)
		}
		if len(report.Violations) != 1 {
			t.Fatalf("violations = %d", len(report.Violations))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for resolved report")
	}
}

func TestGateway_approvalPendingReturns202(t *testing.T) {
	gw, _ := newApprovalGateway(t, "Pending", "Pending", 60*time.Millisecond, 10*time.Millisecond)

	resp := postInvoke(t, gw, invokeRequest{Tool: "deploy"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%q", resp.StatusCode, b)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("missing Retry-After header")
	}
	var body invokeResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "pending" || body.ApprovalID != "appr-1" {
		t.Fatalf("body = %+v", body)
	}
}

func TestGateway_approvalNoChannelFailsClosed(t *testing.T) {
	env := RuntimeEnv{
		SessionNamespace: "ns1",
		SessionName:      "sess-a",
		Mode:             relayv1alpha1.PolicyModeEnforced,
		Policy:           relayv1alpha1.PolicyRules{RequireHumanApproval: []string{"deploy"}},
	}
	gw := httptest.NewServer(&Gateway{Env: env})
	defer gw.Close()

	resp := postInvoke(t, gw, invokeRequest{Tool: "deploy"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%q", resp.StatusCode, b)
	}
}

func writeTempToken(t *testing.T, token string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
