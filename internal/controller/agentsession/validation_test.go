/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

var _ = Describe("validateSpec", func() {
	It("requires task content", func() {
		session := &scrutineerv1alpha1.AgentSession{
			Spec: scrutineerv1alpha1.AgentSessionSpec{
				Task:    scrutineerv1alpha1.SessionTaskSpec{},
				Model:   scrutineerv1alpha1.ModelSpec{Provider: "openai", Name: "gpt-4"},
				Runtime: scrutineerv1alpha1.RuntimeSpec{Image: "busybox:latest"},
			},
		}
		Expect(validateSpec(session)).To(MatchError(ContainSubstring("spec.task.description or spec.task.prompt")))
	})

	It("accepts promptConfigMapRef without inline prompt", func() {
		session := &scrutineerv1alpha1.AgentSession{
			Spec: scrutineerv1alpha1.AgentSessionSpec{
				Task: scrutineerv1alpha1.SessionTaskSpec{
					PromptConfigMapRef: &scrutineerv1alpha1.PromptConfigMapRef{
						Name: "prompts",
						Key:  "task",
					},
				},
				Model:   scrutineerv1alpha1.ModelSpec{Provider: "openai", Name: "gpt-4"},
				Runtime: scrutineerv1alpha1.RuntimeSpec{Image: "busybox:latest"},
			},
		}
		Expect(validateSpec(session)).To(Succeed())
	})

	It("rejects empty promptConfigMapRef name", func() {
		session := &scrutineerv1alpha1.AgentSession{
			Spec: scrutineerv1alpha1.AgentSessionSpec{
				Task: scrutineerv1alpha1.SessionTaskSpec{
					PromptConfigMapRef: &scrutineerv1alpha1.PromptConfigMapRef{Name: " ", Key: "k"},
				},
				Model:   scrutineerv1alpha1.ModelSpec{Provider: "openai", Name: "gpt-4"},
				Runtime: scrutineerv1alpha1.RuntimeSpec{Image: "busybox:latest"},
			},
		}
		Expect(validateSpec(session)).To(MatchError(ContainSubstring("promptConfigMapRef.name")))
	})

	It("rejects unsupported orchestrator", func() {
		session := &scrutineerv1alpha1.AgentSession{
			ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "default"},
			Spec: scrutineerv1alpha1.AgentSessionSpec{
				Task:    scrutineerv1alpha1.SessionTaskSpec{Prompt: "hi"},
				Model:   scrutineerv1alpha1.ModelSpec{Provider: "openai", Name: "gpt-4"},
				Runtime: scrutineerv1alpha1.RuntimeSpec{Image: "busybox:latest", Orchestrator: "tekton"},
			},
		}
		Expect(validateSpec(session)).To(MatchError(ContainSubstring("orchestrator")))
	})

	It("rejects out-of-range temperature", func() {
		bad := "3.0"
		session := &scrutineerv1alpha1.AgentSession{
			Spec: scrutineerv1alpha1.AgentSessionSpec{
				Task:    scrutineerv1alpha1.SessionTaskSpec{Prompt: "hi"},
				Model:   scrutineerv1alpha1.ModelSpec{Provider: "openai", Name: "gpt-4", Temperature: &bad},
				Runtime: scrutineerv1alpha1.RuntimeSpec{Image: "busybox:latest"},
			},
		}
		Expect(validateSpec(session)).To(MatchError(ContainSubstring("temperature")))
	})

	It("rejects empty runtimeProfileRef name", func() {
		session := &scrutineerv1alpha1.AgentSession{
			Spec: scrutineerv1alpha1.AgentSessionSpec{
				Task:    scrutineerv1alpha1.SessionTaskSpec{Prompt: "hi"},
				Model:   scrutineerv1alpha1.ModelSpec{Provider: "openai", Name: "gpt-4"},
				Runtime: scrutineerv1alpha1.RuntimeSpec{Image: "busybox:latest"},
				RuntimeProfileRef: &scrutineerv1alpha1.RuntimeProfileRef{
					Name: " ",
				},
			},
		}
		Expect(validateSpec(session)).To(MatchError(ContainSubstring("runtimeProfileRef.name")))
	})

	It("accepts a valid model.baseURL", func() {
		session := &scrutineerv1alpha1.AgentSession{
			Spec: scrutineerv1alpha1.AgentSessionSpec{
				Task:    scrutineerv1alpha1.SessionTaskSpec{Prompt: "hi"},
				Model:   scrutineerv1alpha1.ModelSpec{Provider: "openrouter", Name: "anthropic/claude-3.5-sonnet", BaseURL: "https://openrouter.ai/api/v1"},
				Runtime: scrutineerv1alpha1.RuntimeSpec{Image: "busybox:latest"},
			},
		}
		Expect(validateSpec(session)).To(Succeed())
	})

	It("rejects a non-http(s) model.baseURL", func() {
		session := &scrutineerv1alpha1.AgentSession{
			Spec: scrutineerv1alpha1.AgentSessionSpec{
				Task:    scrutineerv1alpha1.SessionTaskSpec{Prompt: "hi"},
				Model:   scrutineerv1alpha1.ModelSpec{Provider: "openrouter", Name: "x", BaseURL: "ftp://example.com"},
				Runtime: scrutineerv1alpha1.RuntimeSpec{Image: "busybox:latest"},
			},
		}
		Expect(validateSpec(session)).To(MatchError(ContainSubstring("baseURL")))
	})

	It("rejects unsupported runtimeProfileRef kind", func() {
		session := &scrutineerv1alpha1.AgentSession{
			Spec: scrutineerv1alpha1.AgentSessionSpec{
				Task:    scrutineerv1alpha1.SessionTaskSpec{Prompt: "hi"},
				Model:   scrutineerv1alpha1.ModelSpec{Provider: "openai", Name: "gpt-4"},
				Runtime: scrutineerv1alpha1.RuntimeSpec{Image: "busybox:latest"},
				RuntimeProfileRef: &scrutineerv1alpha1.RuntimeProfileRef{
					Kind: "OtherProfile",
					Name: "x",
				},
			},
		}
		Expect(validateSpec(session)).To(MatchError(ContainSubstring("runtimeProfileRef.kind")))
	})
})
