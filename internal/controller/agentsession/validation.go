/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// validateSpec enforces the invariants that are most useful in the controller path.
// CRD-level validation (enum, min/max, required fields) is also expressed via Kubebuilder
// markers on the API types; this function exists so the controller can reject malformed
// objects cleanly even when the CRD schema is lax (e.g. older clusters, hot-loaded CRDs).
func validateSpec(session *scrutineerv1alpha1.AgentSession) error {
	spec := session.Spec

	if strings.TrimSpace(spec.Task.Description) == "" &&
		strings.TrimSpace(spec.Task.Prompt) == "" &&
		spec.Task.PromptConfigMapRef == nil {
		return fmt.Errorf("spec.task.description or spec.task.prompt (or promptConfigMapRef) must be set")
	}
	if err := validatePromptConfigMapRef(spec.Task.PromptConfigMapRef); err != nil {
		return err
	}
	for i, ref := range spec.PolicyRefs {
		if strings.TrimSpace(ref.Name) == "" {
			return fmt.Errorf("spec.policyRefs[%d].name is required", i)
		}
		switch ref.Kind {
		case "", "AgentPolicy", "ToolPolicy":
		default:
			return fmt.Errorf("spec.policyRefs[%d].kind %q is not supported (allowed: AgentPolicy, ToolPolicy)", i, ref.Kind)
		}
	}
	if err := validateRuntimeProfileRef(spec.RuntimeProfileRef); err != nil {
		return err
	}

	if strings.TrimSpace(spec.Runtime.Image) == "" {
		return fmt.Errorf("spec.runtime.image is required")
	}

	if strings.TrimSpace(spec.Model.Provider) == "" {
		return fmt.Errorf("spec.model.provider is required")
	}
	if strings.TrimSpace(spec.Model.Name) == "" {
		return fmt.Errorf("spec.model.name is required")
	}
	if bu := strings.TrimSpace(spec.Model.BaseURL); bu != "" {
		u, err := url.Parse(bu)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("spec.model.baseURL %q must be an http(s) URL", spec.Model.BaseURL)
		}
	}

	if strings.TrimSpace(spec.Workspace.Size) != "" {
		if _, err := resource.ParseQuantity(spec.Workspace.Size); err != nil {
			return fmt.Errorf("spec.workspace.size %q is not a valid quantity: %w", spec.Workspace.Size, err)
		}
	}

	orchestrator := spec.Runtime.Orchestrator
	if orchestrator == "" {
		orchestrator = OrchestratorKubernetesJob
	}
	if orchestrator != OrchestratorKubernetesJob && orchestrator != OrchestratorKubernetesPod {
		return fmt.Errorf("spec.runtime.orchestrator %q is not supported (allowed: %q, %q)",
			orchestrator, OrchestratorKubernetesJob, OrchestratorKubernetesPod)
	}

	if spec.Model.Temperature != nil {
		raw := *spec.Model.Temperature
		t, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("spec.model.temperature %q is not a valid number: %w", raw, err)
		}
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
	if spec.Policy.MaxCallsPerMinute != nil && *spec.Policy.MaxCallsPerMinute < 0 {
		return fmt.Errorf("spec.policy.maxCallsPerMinute must be >= 0")
	}

	return nil
}

func validateRuntimeProfileRef(ref *scrutineerv1alpha1.RuntimeProfileRef) error {
	if ref == nil {
		return nil
	}
	if strings.TrimSpace(ref.Name) == "" {
		return fmt.Errorf("spec.runtimeProfileRef.name is required")
	}
	switch ref.Kind {
	case "", "RuntimeProfile":
	default:
		return fmt.Errorf("spec.runtimeProfileRef.kind %q is not supported (allowed: RuntimeProfile)", ref.Kind)
	}
	return nil
}

// equalStatus compares two AgentSessionStatus values, treating nil and empty slices as equal
// where possible. It is intentionally conservative; when in doubt it returns false so that
// the status is re-patched. This is safe because controller-runtime de-duplicates work.
func equalStatus(a, b *scrutineerv1alpha1.AgentSessionStatus) bool {
	return reflect.DeepEqual(a, b)
}
