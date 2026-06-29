/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package toolgateway

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

func baseCtx(mode scrutineerv1alpha1.PolicyMode, rules scrutineerv1alpha1.PolicyRules) enforcement.SessionContext {
	return enforcement.SessionContext{
		SessionNamespace: "team-a",
		SessionName:      "demo",
		Mode:             mode,
		Policy:           rules,
	}
}

func TestEvaluateTool_enforcedDeniedTool(t *testing.T) {
	ctx := baseCtx(scrutineerv1alpha1.PolicyModeEnforced, scrutineerv1alpha1.PolicyRules{
		DeniedTools: []string{"kubectl"},
	})
	auth := EvaluateTool(ctx, ToolRequest{Tool: "kubectl"})
	if auth.Allowed || !auth.Blocked {
		t.Fatalf("got %+v", auth)
	}
	if auth.Action != scrutineerv1alpha1.PolicyDecisionDeny || auth.Reason != ReasonDeniedTools {
		t.Fatalf("action=%q reason=%q", auth.Action, auth.Reason)
	}
}

func TestEvaluateTool_dryRunDeniedTool(t *testing.T) {
	ctx := baseCtx(scrutineerv1alpha1.PolicyModeDryRun, scrutineerv1alpha1.PolicyRules{
		DeniedTools: []string{"kubectl"},
	})
	auth := EvaluateTool(ctx, ToolRequest{Tool: "kubectl"})
	if !auth.Allowed || auth.Blocked || !auth.WouldDeny {
		t.Fatalf("got %+v", auth)
	}
	if auth.Action != scrutineerv1alpha1.PolicyDecisionDryRun {
		t.Fatalf("action=%q", auth.Action)
	}
}

func TestEvaluateTool_auditOnlyDeniedTool(t *testing.T) {
	ctx := baseCtx(scrutineerv1alpha1.PolicyModeAuditOnly, scrutineerv1alpha1.PolicyRules{
		DeniedTools: []string{"kubectl"},
	})
	auth := EvaluateTool(ctx, ToolRequest{Tool: "kubectl"})
	if !auth.Allowed || auth.Blocked {
		t.Fatalf("got %+v", auth)
	}
	if auth.Action != scrutineerv1alpha1.PolicyDecisionAudit {
		t.Fatalf("action=%q", auth.Action)
	}
}

func TestEvaluateTool_allowlistBlocksUnknown(t *testing.T) {
	ctx := baseCtx(scrutineerv1alpha1.PolicyModeEnforced, scrutineerv1alpha1.PolicyRules{
		AllowedTools: []string{"shell"},
	})
	auth := EvaluateTool(ctx, ToolRequest{Tool: "deploy"})
	if auth.Allowed || auth.Reason != ReasonNotInAllowedTools {
		t.Fatalf("got %+v", auth)
	}
}

func TestEvaluateTool_allowed(t *testing.T) {
	ctx := baseCtx(scrutineerv1alpha1.PolicyModeEnforced, scrutineerv1alpha1.PolicyRules{
		AllowedTools: []string{"shell"},
		DeniedTools:  []string{"kubectl"},
	})
	auth := EvaluateTool(ctx, ToolRequest{Tool: "shell"})
	if !auth.Allowed || auth.Reason != ReasonAllowed {
		t.Fatalf("got %+v", auth)
	}
}

func TestBuildConfig_fromToolPolicy(t *testing.T) {
	max := int32(10)
	cfg := BuildConfig(baseCtx(scrutineerv1alpha1.PolicyModeDryRun, scrutineerv1alpha1.PolicyRules{
		AllowedTools:      []string{"shell"},
		MaxCallsPerMinute: &max,
	}))
	if cfg == nil || cfg.ListenAddr != DefaultListenAddr {
		t.Fatalf("cfg=%+v", cfg)
	}
	if len(cfg.AllowedTools) != 1 || *cfg.MaxCallsPerMinute != 10 {
		t.Fatalf("cfg=%+v", cfg)
	}
}

func TestBackend_metadata(t *testing.T) {
	b := Backend{}
	if b.Kind() != enforcement.BackendToolGateway {
		t.Fatalf("kind = %q", b.Kind())
	}
	if !b.Capabilities().Tools {
		t.Fatal("expected tools capability")
	}
}

func TestHasEnabledSidecar(t *testing.T) {
	disabled := false
	ctx := enforcement.SessionContext{
		Sidecars: []scrutineerv1alpha1.RuntimeProfileSidecar{
			{Type: SidecarType, Enabled: &disabled},
		},
	}
	if HasEnabledSidecar(ctx) {
		t.Fatal("disabled sidecar")
	}
}

func TestEnvForConfig(t *testing.T) {
	max := int32(5)
	cfg := BuildConfig(baseCtx(scrutineerv1alpha1.PolicyModeDryRun, scrutineerv1alpha1.PolicyRules{
		AllowedTools:         []string{"shell"},
		DeniedTools:          []string{"kubectl"},
		RequireHumanApproval: []string{"deploy"},
		MaxToolCalls:         &max,
		MaxCallsPerMinute:    &max,
	}))
	env := envMap(EnvForConfig(cfg))
	if env[EnvPolicyAllowedTools] != "shell" || env[EnvPolicyDeniedTools] != "kubectl" {
		t.Fatalf("env = %+v", env)
	}
	if env[EnvPolicyMaxToolCalls] != "5" || env[EnvPolicyMaxToolCallsPerMinute] != "5" {
		t.Fatalf("caps = %+v", env)
	}
	if EnvForConfig(nil) != nil {
		t.Fatal("nil cfg")
	}
}

func envMap(vars []corev1.EnvVar) map[string]string {
	out := make(map[string]string, len(vars))
	for _, e := range vars {
		out[e.Name] = e.Value
	}
	return out
}

func TestBackendDesiredState_nilWithoutPolicy(t *testing.T) {
	raw, err := (Backend{}).DesiredState(enforcement.SessionContext{
		SessionNamespace: "ns",
		SessionName:      "s",
	})
	if err != nil || raw != nil {
		t.Fatalf("got %v err=%v", raw, err)
	}
}
