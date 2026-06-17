/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package workspace

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

func TestGateway_deniesAndReportsEnforcedPath(t *testing.T) {
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
			DeniedPaths: []string{"/etc/**"},
		},
	}

	gw := httptest.NewServer(&Gateway{
		Env:      env,
		Reporter: NewReporterClient(env.ReporterURL, env.ReporterToken, reporterSrv.Client()),
		Now:      func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	defer gw.Close()

	body, _ := json.Marshal(accessRequest{Path: "/etc/passwd", Operation: "read"})
	resp, err := http.Post(gw.URL+accessPath, "application/json", bytes.NewReader(body))
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
		if report.Decisions[0].Type != "file" {
			t.Fatalf("type = %q", report.Decisions[0].Type)
		}
		if report.Decisions[0].Action != relayv1alpha1.PolicyDecisionDeny {
			t.Fatalf("action = %q", report.Decisions[0].Action)
		}
		if len(report.Violations) != 1 {
			t.Fatalf("violations = %d", len(report.Violations))
		}
		if report.Backend != "fs-gateway" {
			t.Fatalf("backend = %q", report.Backend)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime report")
	}
}

func TestGateway_allowsPermittedPath(t *testing.T) {
	env := RuntimeEnv{
		SessionNamespace: "ns1",
		SessionName:      "sess-a",
		ReporterURL:      "http://unused",
		ReporterToken:    writeTempToken(t, "x"),
		Mode:             relayv1alpha1.PolicyModeEnforced,
		Policy: relayv1alpha1.PolicyRules{
			AllowedPaths: []string{"/workspace/**"},
		},
	}
	gw := httptest.NewServer(&Gateway{Env: env})
	defer gw.Close()

	body, _ := json.Marshal(accessRequest{Path: "/workspace/out.txt"})
	resp, err := http.Post(gw.URL+accessPath, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
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
