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
)

func TestLoadRuntimeEnv_full(t *testing.T) {
	t.Setenv(EnvSessionNamespace, "ns")
	t.Setenv(EnvSessionName, "sess")
	t.Setenv(EnvReporterURL, "http://reporter")
	t.Setenv(EnvReporterToken, writeTempToken(t, "tok"))
	t.Setenv(EnvPolicyMode, string(relayv1alpha1.PolicyModeEnforced))
	t.Setenv(EnvPolicyDeniedTools, "kubectl, deploy")
	t.Setenv(EnvPolicyMaxToolCalls, "25")
	t.Setenv(EnvPolicyMaxToolCallsPerMinute, "not-a-number")

	env, err := LoadRuntimeEnv()
	if err != nil {
		t.Fatal(err)
	}
	if env.Mode != relayv1alpha1.PolicyModeEnforced {
		t.Fatalf("mode = %q", env.Mode)
	}
	if len(env.Policy.DeniedTools) != 2 {
		t.Fatalf("denied = %v", env.Policy.DeniedTools)
	}
	if env.Policy.MaxToolCalls == nil || *env.Policy.MaxToolCalls != 25 {
		t.Fatalf("max tool calls = %v", env.Policy.MaxToolCalls)
	}
	if env.Policy.MaxCallsPerMinute != nil {
		t.Fatalf("invalid int should be nil, got %v", env.Policy.MaxCallsPerMinute)
	}
	if env.ListenHost != DefaultListenHost {
		t.Fatalf("listen = %q", env.ListenHost)
	}
}

func TestLoadRuntimeEnv_missingRequired(t *testing.T) {
	t.Setenv(EnvSessionNamespace, "")
	t.Setenv(EnvSessionName, "")
	if _, err := LoadRuntimeEnv(); err == nil {
		t.Fatal("expected error for missing session identity")
	}
}
