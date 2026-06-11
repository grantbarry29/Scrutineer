/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package dnsproxy

import (
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

func TestProxy_deniesAndReportsEnforcedEgress(t *testing.T) {
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
			DeniedDomains: []string{"evil.example"},
		},
	}

	proxy := httptest.NewServer(&Proxy{
		Env:      env,
		Reporter: NewReporterClient(env.ReporterURL, env.ReporterToken, reporterSrv.Client()),
		Now:      func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	defer proxy.Close()

	req, err := http.NewRequest(http.MethodGet, proxy.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "evil.example"

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%q", resp.StatusCode, body)
	}

	select {
	case report := <-reports:
		if len(report.Decisions) != 1 {
			t.Fatalf("decisions = %d", len(report.Decisions))
		}
		if report.Decisions[0].Action != relayv1alpha1.PolicyDecisionDeny {
			t.Fatalf("action = %q", report.Decisions[0].Action)
		}
		if len(report.Violations) != 1 {
			t.Fatalf("violations = %d", len(report.Violations))
		}
		if report.Session.Name != "sess-a" || report.Session.Namespace != "ns1" {
			t.Fatalf("session = %+v", report.Session)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime report")
	}
}

func TestLoadRuntimeEnv_requiredFields(t *testing.T) {
	t.Setenv(EnvSessionNamespace, "ns")
	t.Setenv(EnvSessionName, "s")
	t.Setenv(EnvReporterURL, "http://reporter")
	t.Setenv(EnvReporterToken, "/token")

	env, err := LoadRuntimeEnv()
	if err != nil {
		t.Fatal(err)
	}
	if env.ListenAddr != DefaultListenAddr {
		t.Fatalf("listen = %q", env.ListenAddr)
	}
	if len(env.Policy.DeniedDomains) != 0 {
		t.Fatalf("policy = %+v", env.Policy)
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
