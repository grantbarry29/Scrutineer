/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package workspace

import (
	"testing"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

func TestEvaluateFile_deniedPathEnforced(t *testing.T) {
	ctx := enforcement.SessionContext{
		Mode: scrutineerv1alpha1.PolicyModeEnforced,
		Policy: scrutineerv1alpha1.PolicyRules{
			DeniedPaths: []string{"/etc/**"},
		},
	}
	auth := EvaluateFile(ctx, FileRequest{Path: "/etc/passwd", Operation: "read"})
	if auth.Allowed || !auth.Blocked {
		t.Fatalf("auth = %+v", auth)
	}
	if auth.Reason != ReasonDeniedPaths {
		t.Fatalf("reason = %q", auth.Reason)
	}
}

func TestEvaluateFile_notInAllowedPaths(t *testing.T) {
	ctx := enforcement.SessionContext{
		Mode: scrutineerv1alpha1.PolicyModeEnforced,
		Policy: scrutineerv1alpha1.PolicyRules{
			AllowedPaths: []string{"/workspace/**"},
		},
	}
	auth := EvaluateFile(ctx, FileRequest{Path: "/tmp/cache"})
	if auth.Allowed {
		t.Fatalf("auth = %+v", auth)
	}
	if auth.Reason != ReasonNotInAllowedPaths {
		t.Fatalf("reason = %q", auth.Reason)
	}
}

func TestEvaluateFile_allowedUnderWorkspaceGlob(t *testing.T) {
	ctx := enforcement.SessionContext{
		Mode: scrutineerv1alpha1.PolicyModeEnforced,
		Policy: scrutineerv1alpha1.PolicyRules{
			AllowedPaths: []string{"/workspace/**"},
		},
	}
	auth := EvaluateFile(ctx, FileRequest{Path: "/workspace/out/result.txt"})
	if !auth.Allowed {
		t.Fatalf("auth = %+v", auth)
	}
}

func TestEvaluateFile_dryRunAllowsWithViolationAction(t *testing.T) {
	ctx := enforcement.SessionContext{
		Mode: scrutineerv1alpha1.PolicyModeDryRun,
		Policy: scrutineerv1alpha1.PolicyRules{
			DeniedPaths: []string{"/root/.ssh/**"},
		},
	}
	auth := EvaluateFile(ctx, FileRequest{Path: "/root/.ssh/id_rsa"})
	if !auth.Allowed || auth.Action != scrutineerv1alpha1.PolicyDecisionDryRun {
		t.Fatalf("auth = %+v", auth)
	}
}

func TestHasEnabledSidecar(t *testing.T) {
	disabled := false
	ctx := enforcement.SessionContext{
		Enforcement: []scrutineerv1alpha1.RuntimeProfileEnforcement{
			{Type: EnforcementType, Enabled: &disabled},
		},
	}
	if HasEnabledSidecar(ctx) {
		t.Fatal("disabled sidecar should not count")
	}
	ctx.Enforcement[0].Enabled = nil
	if !HasEnabledSidecar(ctx) {
		t.Fatal("nil enabled should default to enabled")
	}
}

func TestPathMatches_globPatterns(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"/workspace/**", "/workspace/a/b", true},
		{"/workspace/**", "/workspace", true},
		{"/workspace/**", "/tmp/x", false},
		{"/etc/passwd", "/etc/passwd", true},
		{"/tmp/*", "/tmp/a", true},
		{"/tmp/*", "/tmp/a/b", false},
	}
	for _, tc := range cases {
		if got := pathMatches(tc.pattern, tc.path); got != tc.want {
			t.Fatalf("pathMatches(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func TestMergeEnvKeys_matchJobBuilder(t *testing.T) {
	cfg := BuildConfig(enforcement.SessionContext{
		Mode: scrutineerv1alpha1.PolicyModeEnforced,
		Policy: scrutineerv1alpha1.PolicyRules{
			AllowedPaths: []string{"/workspace/**"},
			DeniedPaths:  []string{"/etc/**"},
		},
	})
	env := EnvForConfig(cfg)
	byName := map[string]string{}
	for _, e := range env {
		byName[e.Name] = e.Value
	}
	if byName[EnvPolicyAllowedPaths] != "/workspace/**" {
		t.Fatalf("allowed = %q", byName[EnvPolicyAllowedPaths])
	}
	if byName[EnvPolicyDeniedPaths] != "/etc/**" {
		t.Fatalf("denied = %q", byName[EnvPolicyDeniedPaths])
	}
}
