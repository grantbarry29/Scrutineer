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
	"unicode/utf8"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// #96: the authority is agent-controlled (CONNECT authorities can approach Envoy's 60KiB
// header cap), and it lands in both Target and Message. Unbounded, a single decision's
// JSON can exceed the reporter's 64KiB body cap and wedge the pipeline. Decisions must be
// bounded at creation — deterministically, so re-delivered records still dedup.
func TestDecision_boundsAgentControlledFields(t *testing.T) {
	huge := strings.Repeat("a", 50<<10) + ".evil.example:443"
	entry := AccessLogEntry{
		Method:       "CONNECT",
		Authority:    huge,
		ResponseCode: 403,
		StartTime:    "2026-07-01T05:00:00.123Z",
	}
	policy := EgressPolicy{Enforce: true, DeniedDomains: []string{"*.evil.example"}}

	d := entry.Decision(policy)
	if len(d.Target) > maxDecisionTargetBytes {
		t.Fatalf("target = %d bytes, want <= %d", len(d.Target), maxDecisionTargetBytes)
	}
	if !strings.HasSuffix(d.Target, truncationMarker) {
		t.Fatalf("truncated target missing marker: %q", d.Target[len(d.Target)-40:])
	}
	if len(d.Message) > maxDecisionMessageBytes {
		t.Fatalf("message = %d bytes, want <= %d", len(d.Message), maxDecisionMessageBytes)
	}
	// Classification must see the FULL authority, not the truncated one.
	if d.Action != scrutineerv1alpha1.PolicyDecisionDeny || d.Reason != ReasonDeniedDomains {
		t.Fatalf("action/reason = %s/%s, want Deny/%s (classified on full authority)", d.Action, d.Reason, ReasonDeniedDomains)
	}
	// Deterministic: a re-parsed (re-delivered) record must be identical for dedup.
	if d2 := entry.Decision(policy); d2 != d {
		t.Fatal("truncation is not deterministic across re-parses")
	}
}

// Truncation must not split a multi-byte rune (CR status strings must stay valid UTF-8).
func TestDecision_truncationKeepsValidUTF8(t *testing.T) {
	entry := AccessLogEntry{
		Method:    "GET",
		Authority: strings.Repeat("ü", maxDecisionTargetBytes), // 2 bytes each
		StartTime: "2026-07-01T05:00:00.123Z",
	}
	d := entry.Decision(EgressPolicy{})
	if !utf8.ValidString(d.Target) {
		t.Fatal("truncated target is not valid UTF-8")
	}
	if !utf8.ValidString(d.Message) {
		t.Fatal("truncated message is not valid UTF-8")
	}
}

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

	d := entry.Decision(EgressPolicy{})
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

func TestDecision_classifiesAgainstPolicy(t *testing.T) {
	entry := func(authority string) AccessLogEntry {
		return AccessLogEntry{Method: "CONNECT", Authority: authority, ResponseCode: 200, StartTime: "2026-07-01T05:00:00.000Z"}
	}

	// Enforced deny-list: a denied host is recorded as Deny (Envoy also blocked it).
	enforced := EgressPolicy{Enforce: true, DeniedDomains: []string{"*.evil.example"}}
	if d := entry("c2.evil.example:443").Decision(enforced); d.Action != scrutineerv1alpha1.PolicyDecisionDeny || d.Reason != ReasonDeniedDomains {
		t.Fatalf("enforced deny: got action=%s reason=%s", d.Action, d.Reason)
	}
	if d := entry("good.example:443").Decision(enforced); d.Action != scrutineerv1alpha1.PolicyDecisionAllow {
		t.Fatalf("enforced allow-through: got action=%s", d.Action)
	}

	// Audit mode: a would-be denial is recorded as DryRun (Envoy let it through).
	audit := EgressPolicy{Enforce: false, DeniedDomains: []string{"evil.example"}}
	if d := entry("evil.example:443").Decision(audit); d.Action != scrutineerv1alpha1.PolicyDecisionDryRun || d.Reason != ReasonDeniedDomains {
		t.Fatalf("audit dry-run: got action=%s reason=%s", d.Action, d.Reason)
	}

	// Enforced allow-list: not-listed host is denied; listed subdomain allowed.
	allow := EgressPolicy{Enforce: true, AllowedDomains: []string{"*.github.com"}}
	if d := entry("api.github.com:443").Decision(allow); d.Action != scrutineerv1alpha1.PolicyDecisionAllow {
		t.Fatalf("allow-list subdomain: got action=%s", d.Action)
	}
	if d := entry("api.gitlab.com:443").Decision(allow); d.Action != scrutineerv1alpha1.PolicyDecisionDeny || d.Reason != ReasonNotInAllowedDomains {
		t.Fatalf("allow-list default-deny: got action=%s reason=%s", d.Action, d.Reason)
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
	d := entry.Decision(EgressPolicy{})
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
	d := entry.Decision(EgressPolicy{})
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
	if d := get.Decision(EgressPolicy{}); d.Time.IsZero() {
		t.Fatal("real %START_TIME% must parse into the decision time")
	}

	connect, err := ParseAccessLogLine([]byte(lines[1]))
	if err != nil {
		t.Fatalf("parse CONNECT line: %v", err)
	}
	if connect.Method != "CONNECT" || connect.Authority != "example.com:443" {
		t.Fatalf("CONNECT entry = %+v", connect)
	}
	if d := connect.Decision(EgressPolicy{}); d.Target != "example.com:443" || d.Time.IsZero() {
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
