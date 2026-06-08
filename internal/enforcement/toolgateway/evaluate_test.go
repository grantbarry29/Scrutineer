/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package toolgateway

import (
	"testing"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/enforcement"
)

func baseCtx(mode relayv1alpha1.PolicyMode, rules relayv1alpha1.PolicyRules) enforcement.SessionContext {
	return enforcement.SessionContext{
		SessionNamespace: "team-a",
		SessionName:      "demo",
		Mode:             mode,
		Policy:           rules,
	}
}

func TestEvaluateTool_enforcedDeniedTool(t *testing.T) {
	ctx := baseCtx(relayv1alpha1.PolicyModeEnforced, relayv1alpha1.PolicyRules{
		DeniedTools: []string{"kubectl"},
	})
	auth := EvaluateTool(ctx, ToolRequest{Tool: "kubectl"})
	if auth.Allowed || !auth.Blocked {
		t.Fatalf("got %+v", auth)
	}
	if auth.Action != relayv1alpha1.PolicyDecisionDeny || auth.Reason != ReasonDeniedTools {
		t.Fatalf("action=%q reason=%q", auth.Action, auth.Reason)
	}
}

func TestEvaluateTool_dryRunDeniedTool(t *testing.T) {
	ctx := baseCtx(relayv1alpha1.PolicyModeDryRun, relayv1alpha1.PolicyRules{
		DeniedTools: []string{"kubectl"},
	})
	auth := EvaluateTool(ctx, ToolRequest{Tool: "kubectl"})
	if !auth.Allowed || auth.Blocked || !auth.WouldDeny {
		t.Fatalf("got %+v", auth)
	}
	if auth.Action != relayv1alpha1.PolicyDecisionDryRun {
		t.Fatalf("action=%q", auth.Action)
	}
}

func TestEvaluateTool_auditOnlyDeniedTool(t *testing.T) {
	ctx := baseCtx(relayv1alpha1.PolicyModeAuditOnly, relayv1alpha1.PolicyRules{
		DeniedTools: []string{"kubectl"},
	})
	auth := EvaluateTool(ctx, ToolRequest{Tool: "kubectl"})
	if !auth.Allowed || auth.Blocked {
		t.Fatalf("got %+v", auth)
	}
	if auth.Action != relayv1alpha1.PolicyDecisionAudit {
		t.Fatalf("action=%q", auth.Action)
	}
}

func TestEvaluateTool_allowlistBlocksUnknown(t *testing.T) {
	ctx := baseCtx(relayv1alpha1.PolicyModeEnforced, relayv1alpha1.PolicyRules{
		AllowedTools: []string{"shell"},
	})
	auth := EvaluateTool(ctx, ToolRequest{Tool: "deploy"})
	if auth.Allowed || auth.Reason != ReasonNotInAllowedTools {
		t.Fatalf("got %+v", auth)
	}
}

func TestEvaluateTool_allowed(t *testing.T) {
	ctx := baseCtx(relayv1alpha1.PolicyModeEnforced, relayv1alpha1.PolicyRules{
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
	cfg := BuildConfig(baseCtx(relayv1alpha1.PolicyModeDryRun, relayv1alpha1.PolicyRules{
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

func TestBackendDesiredState_nilWithoutPolicy(t *testing.T) {
	raw, err := (Backend{}).DesiredState(enforcement.SessionContext{
		SessionNamespace: "ns",
		SessionName:      "s",
	})
	if err != nil || raw != nil {
		t.Fatalf("got %v err=%v", raw, err)
	}
}
