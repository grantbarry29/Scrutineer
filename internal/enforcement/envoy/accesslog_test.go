/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package envoy

import (
	"os"
	"strings"
	"testing"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

func TestParseAccessLogLine_connect(t *testing.T) {
	line := `{"method":"CONNECT","authority":"api.example.com:443","response_code":200,"flags":"-","bytes_sent":5120,"bytes_received":940,"duration_ms":812,"start_time":"2026-07-01T05:00:00.123Z"}`

	entry, err := ParseAccessLogLine([]byte(line))
	if err != nil {
		t.Fatalf("ParseAccessLogLine: %v", err)
	}
	if entry.Method != "CONNECT" || entry.Authority != "api.example.com:443" {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.ResponseCode != 200 {
		t.Fatalf("response code = %d", entry.ResponseCode)
	}

	d := entry.Decision()
	if d.Phase != scrutineerv1alpha1.PolicyDecisionPhaseRuntime {
		t.Fatalf("phase = %q", d.Phase)
	}
	if d.Type != "network" {
		t.Fatalf("type = %q", d.Type)
	}
	if d.Action != scrutineerv1alpha1.PolicyDecisionAllow {
		t.Fatalf("action = %q", d.Action)
	}
	if d.Target != "api.example.com:443" {
		t.Fatalf("target = %q", d.Target)
	}
	if d.Actor != AccessLogActor {
		t.Fatalf("actor = %q", d.Actor)
	}
	if d.Time.IsZero() {
		t.Fatal("expected start_time to be parsed into the decision time")
	}
	if got := d.Time.UTC().Format("2006-01-02T15:04:05"); got != "2026-07-01T05:00:00" {
		t.Fatalf("time = %q", got)
	}
	// The proxy never self-attests assurance: the reporter stamps it from identity.
	if d.AssuranceLevel != "" {
		t.Fatalf("assurance must be left empty by the data plane, got %q", d.AssuranceLevel)
	}
	if !strings.Contains(d.Message, "CONNECT") || !strings.Contains(d.Message, "200") {
		t.Fatalf("message = %q", d.Message)
	}
}

func TestParseAccessLogLine_httpAndUpstreamFailure(t *testing.T) {
	// Plain-HTTP forward proxying with an upstream connection failure: still observed
	// egress *intent* — recorded with the response detail in the message.
	line := `{"method":"GET","authority":"downloads.example.org","response_code":503,"flags":"UF","bytes_sent":91,"bytes_received":0,"duration_ms":31,"start_time":"2026-07-01T05:01:02.000Z"}`

	entry, err := ParseAccessLogLine([]byte(line))
	if err != nil {
		t.Fatalf("ParseAccessLogLine: %v", err)
	}
	d := entry.Decision()
	if d.Target != "downloads.example.org" {
		t.Fatalf("target = %q", d.Target)
	}
	if !strings.Contains(d.Message, "503") || !strings.Contains(d.Message, "UF") {
		t.Fatalf("message should carry code+flags, got %q", d.Message)
	}
}

func TestParseAccessLogLine_rejects(t *testing.T) {
	cases := map[string]string{
		"empty":        "",
		"not json":     "scrutineer-egress CONNECT example.com:443 -> 200 -",
		"no authority": `{"method":"GET","response_code":200}`,
	}
	for name, line := range cases {
		if _, err := ParseAccessLogLine([]byte(line)); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

func TestParseAccessLogLine_badStartTimeFallsBackToZero(t *testing.T) {
	line := `{"method":"GET","authority":"a.example","response_code":200,"start_time":"not-a-time"}`
	entry, err := ParseAccessLogLine([]byte(line))
	if err != nil {
		t.Fatalf("ParseAccessLogLine: %v", err)
	}
	// A zero time is pinned to receipt time by the reporter's normalization.
	d := entry.Decision()
	if !d.Time.IsZero() {
		t.Fatal("unparseable start_time should yield a zero decision time")
	}
}

// TestParseAccessLogLine_realEnvoyFixture parses lines captured verbatim from a running
// envoyproxy/envoy:distroless-v1.31-latest loaded with BootstrapYAML (one plain-HTTP GET
// and one HTTPS CONNECT through the proxy). Guards the parser against schema drift from
// what Envoy actually emits — regenerate by running Envoy with the rendered bootstrap and
// proxying a request through it.
func TestParseAccessLogLine_realEnvoyFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/access_log_envoy_1_31.jsonl")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("fixture lines = %d, want 2", len(lines))
	}

	get, err := ParseAccessLogLine([]byte(lines[0]))
	if err != nil {
		t.Fatalf("parse GET line: %v", err)
	}
	if get.Method != "GET" || get.Authority != "example.com" || get.ResponseCode != 200 {
		t.Fatalf("GET entry = %+v", get)
	}
	if d := get.Decision(); d.Time.IsZero() {
		t.Fatal("real %START_TIME% must parse into the decision time")
	}

	connect, err := ParseAccessLogLine([]byte(lines[1]))
	if err != nil {
		t.Fatalf("parse CONNECT line: %v", err)
	}
	if connect.Method != "CONNECT" || connect.Authority != "example.com:443" {
		t.Fatalf("CONNECT entry = %+v", connect)
	}
	if d := connect.Decision(); d.Target != "example.com:443" || d.Time.IsZero() {
		t.Fatalf("CONNECT decision = %+v", d)
	}
}

// The bootstrap must write the same JSON schema the parser reads, into the shared
// access-log volume, while keeping the human-readable stdout log.
func TestBootstrapYAML_fileAccessLog(t *testing.T) {
	cfg := BootstrapYAML(BootstrapConfig{Port: ProxyPort})
	for _, s := range []string{
		"envoy.access_loggers.file",
		AccessLogPath,
		// json_format renders numeric operators as JSON numbers (or null), so the
		// parser never sees a "-" placeholder inside a number field.
		`method: "%REQ(:METHOD)%"`,
		`authority: "%REQ(:AUTHORITY)%"`,
		`response_code: "%RESPONSE_CODE%"`,
		`start_time: "%START_TIME%"`,
		"envoy.access_loggers.stdout", // still present for kubectl-logs visibility
	} {
		if !strings.Contains(cfg, s) {
			t.Fatalf("BootstrapYAML missing %q", s)
		}
	}
}
