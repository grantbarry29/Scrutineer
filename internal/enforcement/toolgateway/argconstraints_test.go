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

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

func TestResolveArg_paths(t *testing.T) {
	args := map[string]any{
		"path": "/workspace/file.txt",
		"opts": map[string]any{"recursive": true},
		"args": []any{"delete", "ns"},
		"n":    float64(3),
	}
	cases := []struct {
		path    string
		want    string
		present bool
	}{
		{"path", "/workspace/file.txt", true},
		{"opts.recursive", "true", true},
		{"args[0]", "delete", true},
		{"args[1]", "ns", true},
		{"args[5]", "", false},
		{"n", "3", true},
		{"missing", "", false},
		{"opts.missing", "", false},
	}
	for _, tc := range cases {
		got, present := resolveArg(args, tc.path)
		if got != tc.want || present != tc.present {
			t.Errorf("resolveArg(%q) = (%q,%v), want (%q,%v)", tc.path, got, present, tc.want, tc.present)
		}
	}
}

func TestConstraintMatches_operators(t *testing.T) {
	args := map[string]any{"path": "/etc/shadow", "verb": "delete"}
	cases := []struct {
		name string
		c    scrutineerv1alpha1.ArgumentConstraint
		want bool
	}{
		{"equals", scrutineerv1alpha1.ArgumentConstraint{Arg: "verb", Op: scrutineerv1alpha1.ArgOpEquals, Values: []string{"delete"}}, true},
		{"in", scrutineerv1alpha1.ArgumentConstraint{Arg: "verb", Op: scrutineerv1alpha1.ArgOpIn, Values: []string{"get", "delete"}}, true},
		{"notIn", scrutineerv1alpha1.ArgumentConstraint{Arg: "verb", Op: scrutineerv1alpha1.ArgOpNotIn, Values: []string{"get"}}, true},
		{"hasPrefix", scrutineerv1alpha1.ArgumentConstraint{Arg: "path", Op: scrutineerv1alpha1.ArgOpHasPrefix, Values: []string{"/etc/"}}, true},
		{"notHasPrefix", scrutineerv1alpha1.ArgumentConstraint{Arg: "path", Op: scrutineerv1alpha1.ArgOpNotHasPrefix, Values: []string{"/workspace/"}}, true},
		{"matches", scrutineerv1alpha1.ArgumentConstraint{Arg: "path", Op: scrutineerv1alpha1.ArgOpMatches, Values: []string{"shadow$"}}, true},
		{"notMatches", scrutineerv1alpha1.ArgumentConstraint{Arg: "path", Op: scrutineerv1alpha1.ArgOpNotMatches, Values: []string{"^/workspace"}}, true},
		{"exists", scrutineerv1alpha1.ArgumentConstraint{Arg: "path", Op: scrutineerv1alpha1.ArgOpExists}, true},
		{"notExists", scrutineerv1alpha1.ArgumentConstraint{Arg: "absent", Op: scrutineerv1alpha1.ArgOpNotExists}, true},
		{"missing value op is non-match", scrutineerv1alpha1.ArgumentConstraint{Arg: "absent", Op: scrutineerv1alpha1.ArgOpEquals, Values: []string{"x"}}, false},
		{"missing negated op is non-match", scrutineerv1alpha1.ArgumentConstraint{Arg: "absent", Op: scrutineerv1alpha1.ArgOpNotEquals, Values: []string{"x"}}, false},
	}
	for _, tc := range cases {
		if got := constraintMatches(tc.c, args); got != tc.want {
			t.Errorf("%s: constraintMatches = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestEvaluateArgumentRules_denyAndAllowlist(t *testing.T) {
	rules := []scrutineerv1alpha1.ToolArgumentRule{
		{
			Tools: []string{"read_file"},
			Constraints: []scrutineerv1alpha1.ArgumentConstraint{
				{Arg: "path", Op: scrutineerv1alpha1.ArgOpHasPrefix, Values: []string{"/workspace/"}, Effect: scrutineerv1alpha1.ConstraintEffectAllow},
				{Arg: "path", Op: scrutineerv1alpha1.ArgOpMatches, Values: []string{`\.\.`}, Effect: scrutineerv1alpha1.ConstraintEffectDeny},
			},
		},
		{
			Tools: []string{"*"},
			Constraints: []scrutineerv1alpha1.ArgumentConstraint{
				{Arg: "args[0]", Op: scrutineerv1alpha1.ArgOpIn, Values: []string{"delete"}, Effect: scrutineerv1alpha1.ConstraintEffectDeny},
			},
		},
	}

	// Allowed: under /workspace, no traversal, no destructive verb.
	if reason, _ := evaluateArgumentRules(rules, ToolRequest{Tool: "read_file", Arguments: map[string]any{"path": "/workspace/ok.txt"}}); reason != "" {
		t.Fatalf("expected allow, got %q", reason)
	}
	// Denied by allowlist: path outside /workspace.
	if reason, m := evaluateArgumentRules(rules, ToolRequest{Tool: "read_file", Arguments: map[string]any{"path": "/etc/shadow"}}); reason != ReasonArgumentNotAllowed || m == nil {
		t.Fatalf("expected ArgumentNotAllowed, got %q (%+v)", reason, m)
	}
	// Denied by deny constraint: path traversal even under workspace.
	if reason, _ := evaluateArgumentRules(rules, ToolRequest{Tool: "read_file", Arguments: map[string]any{"path": "/workspace/../etc"}}); reason != ReasonArgumentDenied {
		t.Fatalf("expected ArgumentDenied, got %q", reason)
	}
	// Wildcard rule denies destructive verb on a different tool.
	if reason, _ := evaluateArgumentRules(rules, ToolRequest{Tool: "kubectl", Arguments: map[string]any{"args": []any{"delete"}}}); reason != ReasonArgumentDenied {
		t.Fatalf("expected ArgumentDenied for kubectl delete, got %q", reason)
	}
	// Non-matching tool with no wildcard hit is allowed.
	if reason, _ := evaluateArgumentRules(rules, ToolRequest{Tool: "kubectl", Arguments: map[string]any{"args": []any{"get"}}}); reason != "" {
		t.Fatalf("expected allow for kubectl get, got %q", reason)
	}
}

func TestEvaluateArgumentRules_serverScope(t *testing.T) {
	rules := []scrutineerv1alpha1.ToolArgumentRule{{
		Tools:       []string{"query"},
		Server:      "prod-db",
		Constraints: []scrutineerv1alpha1.ArgumentConstraint{{Arg: "sql", Op: scrutineerv1alpha1.ArgOpMatches, Values: []string{"(?i)drop"}, Effect: scrutineerv1alpha1.ConstraintEffectDeny}},
	}}
	// Same tool, different server: rule does not apply.
	if reason, _ := evaluateArgumentRules(rules, ToolRequest{Tool: "query", Server: "dev-db", Arguments: map[string]any{"sql": "DROP TABLE t"}}); reason != "" {
		t.Fatalf("expected allow on non-matching server, got %q", reason)
	}
	// Matching server: denied.
	if reason, _ := evaluateArgumentRules(rules, ToolRequest{Tool: "query", Server: "prod-db", Arguments: map[string]any{"sql": "DROP TABLE t"}}); reason != ReasonArgumentDenied {
		t.Fatalf("expected ArgumentDenied on prod-db, got %q", reason)
	}
}

func TestEvaluateTool_argumentDeny_modes(t *testing.T) {
	rules := scrutineerv1alpha1.PolicyRules{
		AllowedTools: []string{"read_file"},
		ArgumentRules: []scrutineerv1alpha1.ToolArgumentRule{{
			Tools:       []string{"read_file"},
			Constraints: []scrutineerv1alpha1.ArgumentConstraint{{Arg: "path", Op: scrutineerv1alpha1.ArgOpHasPrefix, Values: []string{"/etc/"}, Effect: scrutineerv1alpha1.ConstraintEffectDeny}},
		}},
	}
	req := ToolRequest{Tool: "read_file", Arguments: map[string]any{"path": "/etc/shadow"}}

	enforced := EvaluateTool(baseCtx(scrutineerv1alpha1.PolicyModeEnforced, rules), req)
	if enforced.Allowed || !enforced.Blocked || enforced.Reason != ReasonArgumentDenied || enforced.ArgMatch == nil {
		t.Fatalf("enforced: %+v", enforced)
	}
	dry := EvaluateTool(baseCtx(scrutineerv1alpha1.PolicyModeDryRun, rules), req)
	if !dry.Allowed || dry.Blocked || !dry.WouldDeny || dry.Action != scrutineerv1alpha1.PolicyDecisionDryRun {
		t.Fatalf("dry-run: %+v", dry)
	}
	audit := EvaluateTool(baseCtx(scrutineerv1alpha1.PolicyModeAuditOnly, rules), req)
	if !audit.Allowed || audit.Action != scrutineerv1alpha1.PolicyDecisionAudit {
		t.Fatalf("audit: %+v", audit)
	}
	// Compliant path passes argument rules.
	ok := EvaluateTool(baseCtx(scrutineerv1alpha1.PolicyModeEnforced, rules), ToolRequest{Tool: "read_file", Arguments: map[string]any{"path": "/workspace/ok"}})
	if !ok.Allowed || ok.Reason != ReasonAllowed {
		t.Fatalf("compliant: %+v", ok)
	}
}

func TestEvaluateTool_nameDenyTakesPrecedence(t *testing.T) {
	rules := scrutineerv1alpha1.PolicyRules{
		DeniedTools: []string{"read_file"},
		ArgumentRules: []scrutineerv1alpha1.ToolArgumentRule{{
			Tools:       []string{"read_file"},
			Constraints: []scrutineerv1alpha1.ArgumentConstraint{{Arg: "path", Op: scrutineerv1alpha1.ArgOpHasPrefix, Values: []string{"/etc/"}, Effect: scrutineerv1alpha1.ConstraintEffectDeny}},
		}},
	}
	auth := EvaluateTool(baseCtx(scrutineerv1alpha1.PolicyModeEnforced, rules), ToolRequest{Tool: "read_file", Arguments: map[string]any{"path": "/workspace/ok"}})
	if auth.Reason != ReasonDeniedTools {
		t.Fatalf("expected DeniedTools precedence, got %+v", auth)
	}
}

func TestEvaluateTool_argDenyBeatsApproval(t *testing.T) {
	// A tool both requiring approval and matching a deny argument rule must be
	// auto-denied (never escalated to a human).
	rules := scrutineerv1alpha1.PolicyRules{
		RequireHumanApproval: []string{"deploy"},
		ArgumentRules: []scrutineerv1alpha1.ToolArgumentRule{{
			Tools:       []string{"deploy"},
			Constraints: []scrutineerv1alpha1.ArgumentConstraint{{Arg: "env", Op: scrutineerv1alpha1.ArgOpEquals, Values: []string{"prod"}, Effect: scrutineerv1alpha1.ConstraintEffectDeny}},
		}},
	}
	auth := EvaluateTool(baseCtx(scrutineerv1alpha1.PolicyModeEnforced, rules), ToolRequest{Tool: "deploy", Arguments: map[string]any{"env": "prod"}})
	if auth.Reason != ReasonArgumentDenied {
		t.Fatalf("expected ArgumentDenied to beat approval, got %+v", auth)
	}
	// Same tool that does NOT match the deny rule still routes to approval.
	hold := EvaluateTool(baseCtx(scrutineerv1alpha1.PolicyModeEnforced, rules), ToolRequest{Tool: "deploy", Arguments: map[string]any{"env": "staging"}})
	if hold.Reason != ReasonApprovalRequired {
		t.Fatalf("expected ApprovalRequired, got %+v", hold)
	}
}

func TestRuntimeReport_argumentDeny_redactsValue(t *testing.T) {
	ctx := baseCtx(scrutineerv1alpha1.PolicyModeEnforced, scrutineerv1alpha1.PolicyRules{})
	secret := "/etc/shadow-supersecret"
	auth := ToolAuthorization{
		Reason: ReasonArgumentDenied,
		ArgMatch: &ArgConstraintMatch{
			Arg:          "path",
			Op:           scrutineerv1alpha1.ArgOpHasPrefix,
			Effect:       scrutineerv1alpha1.ConstraintEffectDeny,
			PolicyValues: []string{"/etc/"},
		},
	}
	auth.Action = scrutineerv1alpha1.PolicyDecisionDeny
	auth.Blocked = true

	report := RuntimeReport(ctx, ToolRequest{Tool: "read_file", Arguments: map[string]any{"path": secret}}, auth, time.Unix(0, 0))
	if len(report.Decisions) != 1 {
		t.Fatalf("decisions = %+v", report.Decisions)
	}
	d := report.Decisions[0]
	if d.Reason != ReasonArgumentDenied || d.Rule != "argumentRules" {
		t.Fatalf("decision = %+v", d)
	}
	if strings.Contains(d.Message, secret) {
		t.Fatalf("message leaks request value: %q", d.Message)
	}
	if !strings.Contains(d.Message, "/etc/") || !strings.Contains(d.Message, "path") {
		t.Fatalf("message missing redacted policy detail: %q", d.Message)
	}
	if len(report.Violations) != 1 {
		t.Fatalf("expected a violation under enforced, got %+v", report.Violations)
	}
	if strings.Contains(report.Violations[0].Message, secret) {
		t.Fatalf("violation leaks request value: %q", report.Violations[0].Message)
	}
}

func TestArgumentRules_envRoundTrip(t *testing.T) {
	rules := []scrutineerv1alpha1.ToolArgumentRule{{
		Tools:       []string{"read_file"},
		Constraints: []scrutineerv1alpha1.ArgumentConstraint{{Arg: "path", Op: scrutineerv1alpha1.ArgOpHasPrefix, Values: []string{"/workspace/"}, Effect: scrutineerv1alpha1.ConstraintEffectAllow}},
	}}
	cfg := BuildConfig(baseCtx(scrutineerv1alpha1.PolicyModeEnforced, scrutineerv1alpha1.PolicyRules{ArgumentRules: rules}))
	env := envMap(EnvForConfig(cfg))
	raw, ok := env[EnvPolicyArgumentRules]
	if !ok || raw == "" {
		t.Fatalf("argument rules env not emitted: %+v", env)
	}
	parsed := argumentRulesEnv(raw)
	if len(parsed) != 1 || parsed[0].Tools[0] != "read_file" || parsed[0].Constraints[0].Effect != scrutineerv1alpha1.ConstraintEffectAllow {
		t.Fatalf("round-trip mismatch: %+v", parsed)
	}
}
