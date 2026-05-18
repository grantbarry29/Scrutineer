/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

var _ = Describe("validateSpec", func() {
	It("requires task content", func() {
		session := &relayv1alpha1.AgentSession{
			Spec: relayv1alpha1.AgentSessionSpec{
				Task:    relayv1alpha1.SessionTaskSpec{},
				Model:   relayv1alpha1.ModelSpec{Provider: "openai", Name: "gpt-4"},
				Runtime: relayv1alpha1.RuntimeSpec{Image: "busybox:latest"},
			},
		}
		Expect(validateSpec(session)).To(MatchError(ContainSubstring("spec.task.description or spec.task.prompt")))
	})

	It("accepts promptConfigMapRef without inline prompt", func() {
		session := &relayv1alpha1.AgentSession{
			Spec: relayv1alpha1.AgentSessionSpec{
				Task: relayv1alpha1.SessionTaskSpec{
					PromptConfigMapRef: &relayv1alpha1.PromptConfigMapRef{
						Name: "prompts",
						Key:  "task",
					},
				},
				Model:   relayv1alpha1.ModelSpec{Provider: "openai", Name: "gpt-4"},
				Runtime: relayv1alpha1.RuntimeSpec{Image: "busybox:latest"},
			},
		}
		Expect(validateSpec(session)).To(Succeed())
	})

	It("rejects empty promptConfigMapRef name", func() {
		session := &relayv1alpha1.AgentSession{
			Spec: relayv1alpha1.AgentSessionSpec{
				Task: relayv1alpha1.SessionTaskSpec{
					PromptConfigMapRef: &relayv1alpha1.PromptConfigMapRef{Name: " ", Key: "k"},
				},
				Model:   relayv1alpha1.ModelSpec{Provider: "openai", Name: "gpt-4"},
				Runtime: relayv1alpha1.RuntimeSpec{Image: "busybox:latest"},
			},
		}
		Expect(validateSpec(session)).To(MatchError(ContainSubstring("promptConfigMapRef.name")))
	})

	It("rejects unsupported orchestrator", func() {
		session := &relayv1alpha1.AgentSession{
			ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "default"},
			Spec: relayv1alpha1.AgentSessionSpec{
				Task:    relayv1alpha1.SessionTaskSpec{Prompt: "hi"},
				Model:   relayv1alpha1.ModelSpec{Provider: "openai", Name: "gpt-4"},
				Runtime: relayv1alpha1.RuntimeSpec{Image: "busybox:latest", Orchestrator: "tekton"},
			},
		}
		Expect(validateSpec(session)).To(MatchError(ContainSubstring("orchestrator")))
	})

	It("rejects out-of-range temperature", func() {
		bad := "3.0"
		session := &relayv1alpha1.AgentSession{
			Spec: relayv1alpha1.AgentSessionSpec{
				Task:    relayv1alpha1.SessionTaskSpec{Prompt: "hi"},
				Model:   relayv1alpha1.ModelSpec{Provider: "openai", Name: "gpt-4", Temperature: &bad},
				Runtime: relayv1alpha1.RuntimeSpec{Image: "busybox:latest"},
			},
		}
		Expect(validateSpec(session)).To(MatchError(ContainSubstring("temperature")))
	})
})
