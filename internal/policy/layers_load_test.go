/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package policy

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

func TestLoadPolicyLayers_agentAndTool(t *testing.T) {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = relayv1alpha1.AddToScheme(s)

	ap := &relayv1alpha1.AgentPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "net", Namespace: "ns"},
		Spec: relayv1alpha1.AgentPolicySpec{
			Mode: relayv1alpha1.PolicyModeAuditOnly,
			PolicyRules: relayv1alpha1.PolicyRules{
				DeniedDomains: []string{"evil.example"},
			},
		},
	}
	tp := &relayv1alpha1.ToolPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "tools", Namespace: "ns"},
		Spec: relayv1alpha1.ToolPolicySpec{
			Mode:        relayv1alpha1.PolicyModeEnforced,
			DeniedTools: []string{"kubectl"},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(ap, tp).Build()

	session := &relayv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "sess"},
		Spec: relayv1alpha1.AgentSessionSpec{
			PolicyRefs: []relayv1alpha1.PolicyRef{
				{Kind: "AgentPolicy", Name: "net"},
				{Kind: "ToolPolicy", Name: "tools"},
			},
		},
	}
	layers, err := LoadPolicyLayers(context.Background(), cl, session)
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 2 {
		t.Fatalf("layers = %d", len(layers))
	}
	if layers[0].Match == nil || layers[0].Match.Kind != "AgentPolicy" {
		t.Fatalf("layer0 = %+v", layers[0])
	}
	if layers[1].Mode != relayv1alpha1.PolicyModeEnforced {
		t.Fatalf("layer1 mode = %q", layers[1].Mode)
	}
}

func TestLoadPolicyLayers_validationErrors(t *testing.T) {
	s := runtime.NewScheme()
	_ = relayv1alpha1.AddToScheme(s)
	cl := fake.NewClientBuilder().WithScheme(s).Build()

	_, err := LoadPolicyLayers(context.Background(), cl, &relayv1alpha1.AgentSession{
		Spec: relayv1alpha1.AgentSessionSpec{
			PolicyRefs: []relayv1alpha1.PolicyRef{{Name: ""}},
		},
	})
	if err == nil {
		t.Fatal("expected empty name error")
	}

	_, err = LoadPolicyLayers(context.Background(), cl, &relayv1alpha1.AgentSession{
		Spec: relayv1alpha1.AgentSessionSpec{
			PolicyRefs: []relayv1alpha1.PolicyRef{{Kind: "BadKind", Name: "x"}},
		},
	})
	if err == nil {
		t.Fatal("expected unsupported kind error")
	}

	_, err = LoadPolicyLayers(context.Background(), cl, &relayv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "s"},
		Spec: relayv1alpha1.AgentSessionSpec{
			PolicyRefs: []relayv1alpha1.PolicyRef{{Kind: "AgentPolicy", Name: "missing"}},
		},
	})
	if err == nil {
		t.Fatal("expected not found error")
	}
}
