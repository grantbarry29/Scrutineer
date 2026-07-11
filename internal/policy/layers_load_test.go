/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package policy

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

func TestLoadPolicyLayers_agentPolicies(t *testing.T) {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = scrutineerv1alpha1.AddToScheme(s)

	ap := &scrutineerv1alpha1.AgentPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "net", Namespace: "ns"},
		Spec: scrutineerv1alpha1.AgentPolicySpec{
			Mode: scrutineerv1alpha1.PolicyModeAuditOnly,
			PolicyRules: scrutineerv1alpha1.PolicyRules{
				DeniedDomains: []string{"evil.example"},
			},
		},
	}
	strict := &scrutineerv1alpha1.AgentPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "strict", Namespace: "ns"},
		Spec: scrutineerv1alpha1.AgentPolicySpec{
			Mode: scrutineerv1alpha1.PolicyModeEnforced,
			PolicyRules: scrutineerv1alpha1.PolicyRules{
				DeniedDomains: []string{"tracker.example"},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(ap, strict).Build()

	session := &scrutineerv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "sess"},
		Spec: scrutineerv1alpha1.AgentSessionSpec{
			PolicyRefs: []scrutineerv1alpha1.PolicyRef{
				{Kind: "AgentPolicy", Name: "net"},
				{Name: "strict"}, // empty kind defaults to AgentPolicy
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
	if layers[1].Mode != scrutineerv1alpha1.PolicyModeEnforced {
		t.Fatalf("layer1 mode = %q", layers[1].Mode)
	}
}

func TestLoadPolicyLayers_validationErrors(t *testing.T) {
	s := runtime.NewScheme()
	_ = scrutineerv1alpha1.AddToScheme(s)
	cl := fake.NewClientBuilder().WithScheme(s).Build()

	_, err := LoadPolicyLayers(context.Background(), cl, &scrutineerv1alpha1.AgentSession{
		Spec: scrutineerv1alpha1.AgentSessionSpec{
			PolicyRefs: []scrutineerv1alpha1.PolicyRef{{Name: ""}},
		},
	})
	if err == nil {
		t.Fatal("expected empty name error")
	}

	_, err = LoadPolicyLayers(context.Background(), cl, &scrutineerv1alpha1.AgentSession{
		Spec: scrutineerv1alpha1.AgentSessionSpec{
			PolicyRefs: []scrutineerv1alpha1.PolicyRef{{Kind: "BadKind", Name: "x"}},
		},
	})
	if err == nil {
		t.Fatal("expected unsupported kind error")
	}

	_, err = LoadPolicyLayers(context.Background(), cl, &scrutineerv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "s"},
		Spec: scrutineerv1alpha1.AgentSessionSpec{
			PolicyRefs: []scrutineerv1alpha1.PolicyRef{{Kind: "AgentPolicy", Name: "missing"}},
		},
	})
	if err == nil {
		t.Fatal("expected not found error")
	}
}

// #103: a referenced AgentPolicy carrying a hostile domain pattern must fail the load
// (→ session Denied with InvalidPolicy) before the pattern ever reaches the Envoy
// bootstrap YAML or the CSV env.
func TestLoadPolicyLayers_rejectsHostileDomainPatterns(t *testing.T) {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = scrutineerv1alpha1.AddToScheme(s)

	ap := &scrutineerv1alpha1.AgentPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "bad", Namespace: "ns"},
		Spec: scrutineerv1alpha1.AgentPolicySpec{
			Mode: scrutineerv1alpha1.PolicyModeEnforced,
			PolicyRules: scrutineerv1alpha1.PolicyRules{
				AllowedDomains: []string{"good.example", "a,b.example"},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(ap).Build()

	session := &scrutineerv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "sess"},
		Spec: scrutineerv1alpha1.AgentSessionSpec{
			PolicyRefs: []scrutineerv1alpha1.PolicyRef{{Name: "bad"}},
		},
	}
	_, err := LoadPolicyLayers(context.Background(), cl, session)
	if err == nil {
		t.Fatal("expected hostile pattern in referenced AgentPolicy to fail the load")
	}
	for _, want := range []string{`AgentPolicy "bad"`, "allowedDomains[1]", "a,b.example"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must contain %q", err.Error(), want)
		}
	}
}

// #125: a referenced AgentPolicy carrying an invalid CIDR pattern must fail the load
// (→ session Denied with InvalidPolicy) before the pattern ever reaches the Envoy
// bootstrap YAML or the CSV env, exactly like hostile domain patterns (#103).
func TestLoadPolicyLayers_rejectsInvalidCIDRPatterns(t *testing.T) {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = scrutineerv1alpha1.AddToScheme(s)

	ap := &scrutineerv1alpha1.AgentPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-cidr", Namespace: "ns"},
		Spec: scrutineerv1alpha1.AgentPolicySpec{
			Mode: scrutineerv1alpha1.PolicyModeEnforced,
			PolicyRules: scrutineerv1alpha1.PolicyRules{
				AllowedCIDRs: []string{"10.0.0.0/8", "10.1.2.3/8"},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(ap).Build()

	session := &scrutineerv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "sess"},
		Spec: scrutineerv1alpha1.AgentSessionSpec{
			PolicyRefs: []scrutineerv1alpha1.PolicyRef{{Name: "bad-cidr"}},
		},
	}
	_, err := LoadPolicyLayers(context.Background(), cl, session)
	if err == nil {
		t.Fatal("expected invalid CIDR pattern in referenced AgentPolicy to fail the load")
	}
	for _, want := range []string{`AgentPolicy "bad-cidr"`, "allowedCIDRs[1]", "10.1.2.3/8"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must contain %q", err.Error(), want)
		}
	}
}
