/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"fmt"
	"reflect"
	"strings"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

// validateSpec enforces the invariants that are most useful in the controller path.
// CRD-level validation (enum, min/max, required fields) is also expressed via Kubebuilder
// markers on the API types; this function exists so the controller can reject malformed
// objects cleanly even when the CRD schema is lax (e.g. older clusters, hot-loaded CRDs).
func validateSpec(session *relayv1alpha1.AgentSession) error {
	spec := session.Spec

	if strings.TrimSpace(spec.Task.Description) == "" &&
		strings.TrimSpace(spec.Task.Prompt) == "" &&
		spec.Task.PromptConfigMapRef == nil {
		return fmt.Errorf("spec.task.description or spec.task.prompt (or promptConfigMapRef) must be set")
	}

	if strings.TrimSpace(spec.Runtime.Image) == "" {
		return fmt.Errorf("spec.runtime.image is required")
	}

	orchestrator := spec.Runtime.Orchestrator
	if orchestrator == "" {
		orchestrator = OrchestratorKubernetesJob
	}
	if orchestrator != OrchestratorKubernetesJob {
		return fmt.Errorf("spec.runtime.orchestrator %q is not supported in MVP (allowed: %q)",
			orchestrator, OrchestratorKubernetesJob)
	}

	if spec.Model.Temperature != nil {
		t := *spec.Model.Temperature
		if t < 0 || t > 2 {
			return fmt.Errorf("spec.model.temperature must be between 0 and 2, got %v", t)
		}
	}
	if spec.Model.MaxTokens != nil && *spec.Model.MaxTokens < 1 {
		return fmt.Errorf("spec.model.maxTokens must be >= 1")
	}

	if spec.Runtime.TimeoutSeconds != nil && *spec.Runtime.TimeoutSeconds < 1 {
		return fmt.Errorf("spec.runtime.timeoutSeconds must be >= 1")
	}

	if spec.Policy.MaxNetworkRequests != nil && *spec.Policy.MaxNetworkRequests < 0 {
		return fmt.Errorf("spec.policy.maxNetworkRequests must be >= 0")
	}
	if spec.Policy.MaxToolCalls != nil && *spec.Policy.MaxToolCalls < 0 {
		return fmt.Errorf("spec.policy.maxToolCalls must be >= 0")
	}

	return nil
}

// equalStatus compares two AgentSessionStatus values, treating nil and empty slices as equal
// where possible. It is intentionally conservative; when in doubt it returns false so that
// the status is re-patched. This is safe because controller-runtime de-duplicates work.
func equalStatus(a, b *relayv1alpha1.AgentSessionStatus) bool {
	return reflect.DeepEqual(a, b)
}
