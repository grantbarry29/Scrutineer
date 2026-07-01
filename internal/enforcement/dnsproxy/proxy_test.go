/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package dnsproxy

import (
	"bufio"
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
	"github.com/grantbarry29/scrutineer/internal/enforcement/sidecarenv"
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
		Base: sidecarenv.Base{
			SessionNamespace: "ns1",
			SessionName:      "sess-a",
			ReporterURL:      reporterSrv.URL,
			ReporterToken:    tokenPath,
			Mode:             scrutineerv1alpha1.PolicyModeEnforced,
		},
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
		Base: sidecarenv.Base{
			SessionNamespace: "ns1",
			SessionName:      "sess-a",
			Mode:             scrutineerv1alpha1.PolicyModeEnforced,
		},
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

// TestProxy_connectHalfClose verifies the CONNECT tunnel propagates a client
// write-side half-close to the upstream. The upstream replies only after it reads
// EOF, so without CloseWrite propagation the upstream never unblocks and the
// client read times out.
func TestProxy_connectHalfClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// Upstream: drain to EOF, then send a sentinel and close.
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(io.Discard, conn) // returns only when the client half-closes
		_, _ = conn.Write([]byte("BYE"))
	}()

	env := RuntimeEnv{
		Base: sidecarenv.Base{
			SessionNamespace: "ns1",
			SessionName:      "sess-a",
			Mode:             scrutineerv1alpha1.PolicyModeEnforced,
		},
	}
	proxy := httptest.NewServer(&Proxy{
		Env: env,
		Dial: func(network, address string) (net.Conn, error) {
			return net.Dial("tcp", ln.Addr().String())
		},
	})
	defer proxy.Close()

	raw, err := net.Dial("tcp", strings.TrimPrefix(proxy.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	client := raw.(*net.TCPConn)
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))

	if _, err := client.Write([]byte("CONNECT allowed.example.com:443 HTTP/1.1\r\nHost: allowed.example.com:443\r\n\r\n")); err != nil {
		t.Fatal(err)
	}

	br := bufio.NewReader(client)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read status line: %v", err)
	}
	if !strings.Contains(status, "200") {
		t.Fatalf("CONNECT status = %q", status)
	}
	// Consume the rest of the response header (blank line terminator).
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read header: %v", err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	// Send a payload, then half-close the write side while still reading.
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	if err := client.CloseWrite(); err != nil {
		t.Fatal(err)
	}

	got, err := io.ReadAll(br)
	if err != nil {
		t.Fatalf("read upstream reply after half-close: %v", err)
	}
	if string(got) != "BYE" {
		t.Fatalf("reply = %q, want \"BYE\" (upstream did not observe client EOF)", got)
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
	if env.BindAddr != DefaultBindAddr {
		t.Fatalf("listen = %q", env.BindAddr)
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
