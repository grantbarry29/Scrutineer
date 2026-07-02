/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package reporter

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
)

// egressProxyPod builds the pod the controller provisions for a session's egress proxy:
// deterministic name, controller owner-ref to the AgentSession, dedicated per-session SA.
func egressProxyPod(ns, sessionName string) *corev1.Pod {
	yes := true
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      envoy.ResourceName(sessionName),
			Namespace: ns,
			Labels:    envoy.Labels(sessionName),
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: scrutineerv1alpha1.GroupVersion.String(),
				Kind:       "AgentSession",
				Name:       sessionName,
				Controller: &yes,
			}},
		},
		Spec: corev1.PodSpec{ServiceAccountName: envoy.ResourceName(sessionName)},
	}
}

func saUsername(ns, sa string) string { return "system:serviceaccount:" + ns + ":" + sa }

func TestAuthorize_egressProxyPodGetsProxyClass(t *testing.T) {
	pod := egressProxyPod("ns1", "sess-a")

	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(pod).Build()
	v := &KubeIdentityVerifier{Client: cl, Reader: cl, Audience: TokenAudience}

	class, err := v.authorizePodForSession(context.Background(), "ns1", pod.Name, "sess-a",
		saUsername("ns1", pod.Spec.ServiceAccountName))
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if class != CallerEgressProxy {
		t.Fatalf("class = %q, want %q", class, CallerEgressProxy)
	}
}

func TestAuthorize_egressProxyPodForWrongSessionForbidden(t *testing.T) {
	// The pod belongs to sess-a; reporting against sess-b must be forbidden even though
	// the name/SA/token are internally consistent.
	pod := egressProxyPod("ns1", "sess-a")

	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(pod).Build()
	v := &KubeIdentityVerifier{Client: cl, Reader: cl, Audience: TokenAudience}

	if _, err := v.authorizePodForSession(context.Background(), "ns1", pod.Name, "sess-b",
		saUsername("ns1", pod.Spec.ServiceAccountName)); err == nil {
		t.Fatal("expected forbidden")
	} else if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestAuthorize_lookalikePodWithoutSessionOwnerForbidden(t *testing.T) {
	// A pod that copies the envoy name/labels but is not controller-owned by the
	// AgentSession (e.g. created by a namespace tenant) must not gain the proxy class.
	pod := egressProxyPod("ns1", "sess-a")
	pod.OwnerReferences = nil

	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(pod).Build()
	v := &KubeIdentityVerifier{Client: cl, Reader: cl, Audience: TokenAudience}

	if _, err := v.authorizePodForSession(context.Background(), "ns1", pod.Name, "sess-a",
		saUsername("ns1", pod.Spec.ServiceAccountName)); err == nil {
		t.Fatal("expected forbidden for lookalike pod without AgentSession owner ref")
	} else if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestAuthorize_egressProxyPodWrongServiceAccountForbidden(t *testing.T) {
	// Right name + owner ref, but the pod runs under a different SA than the dedicated
	// per-session identity (defense against SA swaps).
	pod := egressProxyPod("ns1", "sess-a")
	pod.Spec.ServiceAccountName = "default"

	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(pod).Build()
	v := &KubeIdentityVerifier{Client: cl, Reader: cl, Audience: TokenAudience}

	if _, err := v.authorizePodForSession(context.Background(), "ns1", pod.Name, "sess-a",
		saUsername("ns1", "default")); err == nil {
		t.Fatal("expected forbidden for wrong service account")
	} else if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestCallerIdentity_assurance(t *testing.T) {
	if got := (CallerIdentity{}).Assurance(); got != scrutineerv1alpha1.EvidenceSelfReported {
		t.Fatalf("default assurance = %q", got)
	}
	if got := (CallerIdentity{Class: CallerEgressProxy}).Assurance(); got != scrutineerv1alpha1.EvidenceObserved {
		t.Fatalf("egress-proxy assurance = %q", got)
	}
}

// Assurance is derived from identity, never from the payload: an agent-adjacent caller
// claiming observed is downgraded; the egress proxy's identity upgrades to observed.
func TestValidateAndNormalizeReport_stampsAssuranceFromIdentity(t *testing.T) {
	now := time.Unix(1000, 0)
	req := ReportRequest{
		Session: SessionRef{Namespace: "ns", Name: "s"},
		Backend: "egress-proxy",
		Decisions: []scrutineerv1alpha1.PolicyDecision{{
			Type: "network", Action: scrutineerv1alpha1.PolicyDecisionAllow,
			Reason: "EgressObserved", Message: "m",
			// Hostile payload: claims the highest assurance.
			AssuranceLevel: scrutineerv1alpha1.EvidenceObserved,
		}},
		Violations: []scrutineerv1alpha1.PolicyViolation{{
			Type: "network", Target: "evil.example", Message: "v",
			AssuranceLevel: scrutineerv1alpha1.EvidenceObserved,
		}},
	}

	agent, err := ValidateAndNormalizeReport(req, now, "", scrutineerv1alpha1.EvidenceSelfReported)
	if err != nil {
		t.Fatalf("normalize (agent): %v", err)
	}
	if agent.Decisions[0].AssuranceLevel != scrutineerv1alpha1.EvidenceSelfReported {
		t.Fatalf("agent decision assurance = %q", agent.Decisions[0].AssuranceLevel)
	}
	if agent.Violations[0].AssuranceLevel != scrutineerv1alpha1.EvidenceSelfReported {
		t.Fatalf("agent violation assurance = %q", agent.Violations[0].AssuranceLevel)
	}

	proxy, err := ValidateAndNormalizeReport(req, now, "", scrutineerv1alpha1.EvidenceObserved)
	if err != nil {
		t.Fatalf("normalize (proxy): %v", err)
	}
	if proxy.Decisions[0].AssuranceLevel != scrutineerv1alpha1.EvidenceObserved {
		t.Fatalf("proxy decision assurance = %q", proxy.Decisions[0].AssuranceLevel)
	}
	if proxy.Violations[0].AssuranceLevel != scrutineerv1alpha1.EvidenceObserved {
		t.Fatalf("proxy violation assurance = %q", proxy.Violations[0].AssuranceLevel)
	}

	// Defensive default: an empty assurance never passes through as empty/high.
	def, err := ValidateAndNormalizeReport(req, now, "", "")
	if err != nil {
		t.Fatalf("normalize (default): %v", err)
	}
	if def.Decisions[0].AssuranceLevel != scrutineerv1alpha1.EvidenceSelfReported {
		t.Fatalf("default decision assurance = %q", def.Decisions[0].AssuranceLevel)
	}
}

// End-to-end through the handler: the verifier's class decides what lands in status.
func TestHandler_stampsObservedOnlyForEgressProxyIdentity(t *testing.T) {
	ts := metav1.NewTime(time.Unix(200, 0))

	post := func(class CallerClass, sessionName string) scrutineerv1alpha1.AgentSession {
		t.Helper()
		session := &scrutineerv1alpha1.AgentSession{
			ObjectMeta: metav1.ObjectMeta{Name: sessionName, Namespace: "ns1"},
		}
		cl := newFakeClient(session)
		h := &Handler{
			Writer:   cl.Status(),
			Reader:   cl,
			Verifier: stubVerifier{identity: CallerIdentity{Namespace: "ns1", PodName: "p", Class: class}},
			Now:      func() time.Time { return ts.Time },
		}
		body := ReportRequest{
			Session: SessionRef{Namespace: "ns1", Name: sessionName},
			Backend: "egress-proxy",
			Decisions: []scrutineerv1alpha1.PolicyDecision{{
				Type: "network", Action: scrutineerv1alpha1.PolicyDecisionAllow,
				Reason: "EgressObserved", Message: "egress GET example.com",
				AssuranceLevel: scrutineerv1alpha1.EvidenceObserved, // ignored: identity wins
			}},
		}
		rec := postReport(t, h, body, "Bearer tok")
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
		}
		var updated scrutineerv1alpha1.AgentSession
		if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns1", Name: sessionName}, &updated); err != nil {
			t.Fatal(err)
		}
		return updated
	}

	fromProxy := post(CallerEgressProxy, "sess-proxy")
	if got := fromProxy.Status.PolicyDecisions[0].AssuranceLevel; got != scrutineerv1alpha1.EvidenceObserved {
		t.Fatalf("proxy-identity assurance = %q, want observed", got)
	}

	fromAgent := post("", "sess-agent")
	if got := fromAgent.Status.PolicyDecisions[0].AssuranceLevel; got != scrutineerv1alpha1.EvidenceSelfReported {
		t.Fatalf("agent-identity assurance = %q, want self-reported", got)
	}
}
