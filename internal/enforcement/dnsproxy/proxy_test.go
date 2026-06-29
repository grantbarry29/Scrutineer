/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package dnsproxy

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
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
		Mode:             scrutineerv1alpha1.PolicyModeEnforced,
		Policy: scrutineerv1alpha1.PolicyRules{
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
		if report.Decisions[0].Action != scrutineerv1alpha1.PolicyDecisionDeny {
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

func TestProxy_allowsHTTPViaDial(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("upstream-ok"))
	}))
	defer upstream.Close()

	env := RuntimeEnv{
		SessionNamespace: "ns1",
		SessionName:      "sess-a",
		Mode:             scrutineerv1alpha1.PolicyModeEnforced,
	}
	proxy := httptest.NewServer(&Proxy{
		Env: env,
		Dial: func(network, address string) (net.Conn, error) {
			return net.Dial("tcp", strings.TrimPrefix(upstream.URL, "http://"))
		},
	})
	defer proxy.Close()

	req, err := http.NewRequest(http.MethodGet, proxy.URL+"/path", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "allowed.example.com"

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%q", resp.StatusCode, body)
	}
}

func TestLoadRuntimeEnv_policyCSV(t *testing.T) {
	t.Setenv(EnvSessionNamespace, "ns")
	t.Setenv(EnvSessionName, "s")
	t.Setenv(EnvReporterURL, "http://reporter")
	t.Setenv(EnvReporterToken, writeTempToken(t, "tok"))
	t.Setenv(EnvPolicyDeniedDomains, " evil.example , bad.test ")
	t.Setenv(EnvPolicyAllowedCIDRs, "10.0.0.0/8,203.0.113.0/24")

	env, err := LoadRuntimeEnv()
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Policy.DeniedDomains) != 2 || env.Policy.DeniedDomains[0] != "evil.example" {
		t.Fatalf("denied = %v", env.Policy.DeniedDomains)
	}
	if len(env.Policy.AllowedCIDRs) != 2 {
		t.Fatalf("cidrs = %v", env.Policy.AllowedCIDRs)
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
