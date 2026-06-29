/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package toolgateway

import (
	"strings"
	"testing"
	"time"
)

func TestRetryAfterSeconds(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want string
	}{
		{"sub-second floors to 1", 250 * time.Millisecond, "1"},
		{"zero floors to 1", 0, "1"},
		{"negative floors to 1", -5 * time.Second, "1"},
		{"exact seconds", 3 * time.Second, "3"},
		{"fractional truncates down", 2900 * time.Millisecond, "2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryAfterSeconds(tc.in); got != tc.want {
				t.Fatalf("retryAfterSeconds(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDeriveApprovalRequestID(t *testing.T) {
	req := ToolRequest{Tool: "kubectl", Server: "ops-mcp"}
	id := deriveApprovalRequestID(req, "sha256:abc")

	if !strings.HasPrefix(id, "tg-") {
		t.Fatalf("id %q missing tg- prefix", id)
	}
	if got := len(strings.TrimPrefix(id, "tg-")); got != 16 {
		t.Fatalf("id hex length = %d, want 16 (id=%q)", got, id)
	}
	// Deterministic: identical inputs map to one hold.
	if again := deriveApprovalRequestID(req, "sha256:abc"); again != id {
		t.Fatalf("not deterministic: %q != %q", again, id)
	}
	// Distinct on each identity component.
	for _, other := range []ToolRequest{
		{Tool: "helm", Server: "ops-mcp"},
		{Tool: "kubectl", Server: "other-mcp"},
	} {
		if deriveApprovalRequestID(other, "sha256:abc") == id {
			t.Fatalf("collision for distinct request %+v", other)
		}
	}
	if deriveApprovalRequestID(req, "sha256:different") == id {
		t.Fatalf("id should change with the arguments digest")
	}
}

func TestArgumentsDigest(t *testing.T) {
	if got := argumentsDigest(nil); got != "" {
		t.Fatalf("nil args digest = %q, want empty", got)
	}
	if got := argumentsDigest(map[string]any{}); got != "" {
		t.Fatalf("empty args digest = %q, want empty", got)
	}

	// Stable regardless of literal map insertion order (encoding/json sorts keys).
	a := argumentsDigest(map[string]any{"namespace": "prod", "verb": "delete"})
	b := argumentsDigest(map[string]any{"verb": "delete", "namespace": "prod"})
	if a != b {
		t.Fatalf("digest not order-independent: %q != %q", a, b)
	}
	if !strings.HasPrefix(a, "sha256:") {
		t.Fatalf("digest %q missing sha256: prefix", a)
	}
	if argumentsDigest(map[string]any{"namespace": "dev", "verb": "delete"}) == a {
		t.Fatalf("digest should change when argument values change")
	}
}

func TestApprovalTarget(t *testing.T) {
	if got := approvalTarget(ToolRequest{Tool: "kubectl"}); got != "kubectl" {
		t.Fatalf("approvalTarget without server = %q, want %q", got, "kubectl")
	}
	if got := approvalTarget(ToolRequest{Tool: "kubectl", Server: "ops-mcp"}); got != "kubectl@ops-mcp" {
		t.Fatalf("approvalTarget with server = %q, want %q", got, "kubectl@ops-mcp")
	}
}
