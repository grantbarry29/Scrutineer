/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// ResolvedTask holds the task fields used when building the runtime Job. Prompt may
// be sourced from spec.task.prompt or from spec.task.promptConfigMapRef.
type ResolvedTask struct {
	Description string
	Prompt      string
}

// resolveTask loads the effective task content for an AgentSession. When
// promptConfigMapRef is set, the prompt is read from the referenced ConfigMap key
// in the same namespace as the AgentSession.
func (r *AgentSessionReconciler) resolveTask(ctx context.Context, session *scrutineerv1alpha1.AgentSession) (*ResolvedTask, error) {
	task := session.Spec.Task
	resolved := &ResolvedTask{
		Description: task.Description,
		Prompt:      task.Prompt,
	}

	ref := task.PromptConfigMapRef
	if ref == nil {
		return resolved, nil
	}

	var cm corev1.ConfigMap
	key := client.ObjectKey{Namespace: session.Namespace, Name: ref.Name}
	if err := r.Get(ctx, key, &cm); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("spec.task.promptConfigMapRef: ConfigMap %q not found", ref.Name)
		}
		return nil, fmt.Errorf("spec.task.promptConfigMapRef: get ConfigMap %q: %w", ref.Name, err)
	}

	val, ok := cm.Data[ref.Key]
	if !ok {
		// BinaryData is not supported for prompts; keep the contract simple.
		return nil, fmt.Errorf("spec.task.promptConfigMapRef: key %q not found in ConfigMap %q", ref.Key, ref.Name)
	}
	resolved.Prompt = val
	return resolved, nil
}

// validatePromptConfigMapRef checks ref fields when present. Called from validateSpec.
func validatePromptConfigMapRef(ref *scrutineerv1alpha1.PromptConfigMapRef) error {
	if ref == nil {
		return nil
	}
	if strings.TrimSpace(ref.Name) == "" {
		return fmt.Errorf("spec.task.promptConfigMapRef.name is required")
	}
	if strings.TrimSpace(ref.Key) == "" {
		return fmt.Errorf("spec.task.promptConfigMapRef.key is required")
	}
	return nil
}
