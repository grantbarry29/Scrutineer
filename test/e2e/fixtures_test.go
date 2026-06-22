//go:build e2e

/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/controller/agentsession"
	relayjob "github.com/secureai/relay/internal/controller/job"
	"github.com/secureai/relay/internal/enforcement/networkpolicy"
)

// agentSessionOption mutates an AgentSession during construction.
type agentSessionOption func(*relayv1alpha1.AgentSession)

func withTemperature(t string) agentSessionOption {
	return func(s *relayv1alpha1.AgentSession) { s.Spec.Model.Temperature = strPtr(t) }
}

func withCommand(cmd ...string) agentSessionOption {
	return func(s *relayv1alpha1.AgentSession) { s.Spec.Runtime.Command = cmd }
}

func withoutTask() agentSessionOption {
	return func(s *relayv1alpha1.AgentSession) {
		s.Spec.Task = relayv1alpha1.SessionTaskSpec{}
	}
}

func withPromptConfigMapRef(name, key string) agentSessionOption {
	return func(s *relayv1alpha1.AgentSession) {
		s.Spec.Task.Prompt = ""
		s.Spec.Task.PromptConfigMapRef = &relayv1alpha1.PromptConfigMapRef{
			Name: name,
			Key:  key,
		}
	}
}

func withCancelRequested() agentSessionOption {
	return func(s *relayv1alpha1.AgentSession) { s.Spec.CancelRequested = true }
}

func withLongRunningCommand() agentSessionOption {
	return func(s *relayv1alpha1.AgentSession) {
		s.Spec.Runtime.Command = []string{"sh", "-c", "echo running; sleep 300"}
	}
}

func withRuntimeProfileRef(name string) agentSessionOption {
	return func(s *relayv1alpha1.AgentSession) {
		s.Spec.RuntimeProfileRef = &relayv1alpha1.RuntimeProfileRef{Name: name}
	}
}

func withPolicyRef(kind, name string) agentSessionOption {
	return func(s *relayv1alpha1.AgentSession) {
		s.Spec.PolicyRefs = append(s.Spec.PolicyRefs, relayv1alpha1.PolicyRef{Kind: kind, Name: name})
	}
}

func withExitCommand(exitCode int) agentSessionOption {
	return func(s *relayv1alpha1.AgentSession) {
		s.Spec.Runtime.Command = []string{"sh", "-c", fmt.Sprintf("exit %d", exitCode)}
	}
}

func withTimeoutSeconds(sec int64) agentSessionOption {
	return func(s *relayv1alpha1.AgentSession) {
		s.Spec.Runtime.TimeoutSeconds = &sec
	}
}

// withSleepExceedingTimeout runs longer than typical timeoutSeconds used in TimedOut e2e.
func withSleepExceedingTimeout() agentSessionOption {
	return func(s *relayv1alpha1.AgentSession) {
		s.Spec.Runtime.Command = []string{"sh", "-c", "sleep 120"}
	}
}

// withDeniedToolInvokeProbe waits for sidecars then POSTs denied tool invokes to RELAY_TOOL_GATEWAY_URL.
func withDeniedToolInvokeProbe(tool string) agentSessionOption {
	return func(s *relayv1alpha1.AgentSession) {
		s.Spec.Runtime.Command = []string{"sh", "-c", fmt.Sprintf(
			`sleep 15; for i in $(seq 1 25); do wget -q -O /dev/null --post-data='{"tool":"%s"}' --header='Content-Type: application/json' "${RELAY_TOOL_GATEWAY_URL}/v1/tools/invoke" 2>/dev/null || true; sleep 2; done; sleep 120`,
			tool,
		)}
	}
}

// withArgumentDeniedToolInvokeProbe waits for sidecars then repeatedly POSTs a tool call
// (with arguments) that an argument rule should deny. argsJSON is the raw JSON for the
// "arguments" object, e.g. `{"path":"/etc/shadow"}`.
func withArgumentDeniedToolInvokeProbe(tool, argsJSON string) agentSessionOption {
	return func(s *relayv1alpha1.AgentSession) {
		body := fmt.Sprintf(`{"tool":"%s","arguments":%s}`, tool, argsJSON)
		s.Spec.Runtime.Command = []string{"sh", "-c", fmt.Sprintf(
			`sleep 15; for i in $(seq 1 25); do wget -q -O /dev/null --post-data='%s' --header='Content-Type: application/json' "${RELAY_TOOL_GATEWAY_URL}/v1/tools/invoke" 2>/dev/null || true; sleep 2; done; sleep 120`,
			body,
		)}
	}
}

// withDeniedDomainEgressProbe waits for sidecars then probes a denied domain via HTTP_PROXY.
func withDeniedDomainEgressProbe(domain string) agentSessionOption {
	return func(s *relayv1alpha1.AgentSession) {
		s.Spec.Runtime.Command = []string{"sh", "-c", fmt.Sprintf(
			`sleep 15; for i in $(seq 1 25); do wget -q -O /dev/null http://%s/ 2>/dev/null || true; sleep 2; done; sleep 120`,
			domain,
		)}
	}
}

// withDeniedPathAccessProbe waits for sidecars then POSTs denied file access to RELAY_FS_GATEWAY_URL.
func withDeniedPathAccessProbe(path string) agentSessionOption {
	return func(s *relayv1alpha1.AgentSession) {
		s.Spec.Runtime.Command = []string{"sh", "-c", fmt.Sprintf(
			`sleep 15; for i in $(seq 1 25); do wget -q -O /dev/null --post-data='{"path":"%s","operation":"read"}' --header='Content-Type: application/json' "${RELAY_FS_GATEWAY_URL}/v1/files/access" 2>/dev/null || true; sleep 2; done; sleep 120`,
			path,
		)}
	}
}

// newTestNamespace creates a uniquely-named namespace for one It block.
func newTestNamespace(prefix string) string {
	name := fmt.Sprintf("%s-%s", prefix, rand.String(5))
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())

	DeferCleanup(func(ctx SpecContext) {
		_ = k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})
	}, NodeTimeout(60*time.Second))

	return name
}

// newAgentSession builds a valid baseline AgentSession and applies opts.
func newAgentSession(namespace, name string, opts ...agentSessionOption) *relayv1alpha1.AgentSession {
	s := &relayv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: relayv1alpha1.AgentSessionSpec{
			Task: relayv1alpha1.SessionTaskSpec{
				Description: "e2e test session",
				Prompt:      "noop",
			},
			Model: relayv1alpha1.ModelSpec{
				Provider: "openai",
				Name:     "gpt-4.1",
			},
			Runtime: relayv1alpha1.RuntimeSpec{
				Orchestrator: agentsession.OrchestratorKubernetesJob,
				Image:        "busybox:latest",
				Command:      []string{"sh", "-c", "echo ok"},
			},
		},
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// createAgentSession creates the session in the cluster and returns its object key.
func createAgentSession(ctx context.Context, session *relayv1alpha1.AgentSession) client.ObjectKey {
	GinkgoHelper()
	Expect(k8sClient.Create(ctx, session)).To(Succeed())
	return client.ObjectKeyFromObject(session)
}

// jobNameForSession returns the deterministic Job name the controller creates.
func jobNameForSession(session *relayv1alpha1.AgentSession) string {
	return relayjob.NameFor(session)
}

// netpolNameForSession returns the deterministic NetworkPolicy name the controller creates.
func netpolNameForSession(session *relayv1alpha1.AgentSession) string {
	return networkpolicy.NameFor(session.Namespace, session.Name)
}

// createEnforcedCIDRPolicy creates an AgentPolicy with enforced mode and an allowed CIDR.
func createEnforcedCIDRPolicy(ctx context.Context, namespace, name, cidr string) {
	GinkgoHelper()
	ap := &relayv1alpha1.AgentPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: relayv1alpha1.AgentPolicySpec{
			Mode: relayv1alpha1.PolicyModeEnforced,
			PolicyRules: relayv1alpha1.PolicyRules{
				AllowedCIDRs: []string{cidr},
			},
		},
	}
	Expect(k8sClient.Create(ctx, ap)).To(Succeed())
}

func strPtr(s string) *string { return &s }

func boolPtr(b bool) *bool { return &b }

func createEnforcedDeniedDomainPolicy(ctx context.Context, namespace, name, domain string) {
	GinkgoHelper()
	ap := &relayv1alpha1.AgentPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: relayv1alpha1.AgentPolicySpec{
			Mode: relayv1alpha1.PolicyModeEnforced,
			PolicyRules: relayv1alpha1.PolicyRules{
				DeniedDomains: []string{domain},
			},
		},
	}
	Expect(k8sClient.Create(ctx, ap)).To(Succeed())
}

func createEnforcedDeniedToolPolicy(ctx context.Context, namespace, name, tool string) {
	GinkgoHelper()
	tp := &relayv1alpha1.ToolPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: relayv1alpha1.ToolPolicySpec{
			Mode:        relayv1alpha1.PolicyModeEnforced,
			DeniedTools: []string{tool},
		},
	}
	Expect(k8sClient.Create(ctx, tp)).To(Succeed())
}

// createEnforcedArgumentRuleToolPolicy creates an enforced ToolPolicy that allows the tool
// by name but denies it when arg has the given prefix (an argument-level constraint).
func createEnforcedArgumentRuleToolPolicy(ctx context.Context, namespace, name, tool, arg, denyPrefix string) {
	GinkgoHelper()
	tp := &relayv1alpha1.ToolPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: relayv1alpha1.ToolPolicySpec{
			Mode:         relayv1alpha1.PolicyModeEnforced,
			AllowedTools: []string{tool},
			ArgumentRules: []relayv1alpha1.ToolArgumentRule{{
				Tools: []string{tool},
				Constraints: []relayv1alpha1.ArgumentConstraint{{
					Arg:    arg,
					Op:     relayv1alpha1.ArgOpHasPrefix,
					Values: []string{denyPrefix},
					Effect: relayv1alpha1.ConstraintEffectDeny,
				}},
			}},
		},
	}
	Expect(k8sClient.Create(ctx, tp)).To(Succeed())
}

func createEnforcedDeniedPathPolicy(ctx context.Context, namespace, name, path string) {
	GinkgoHelper()
	ap := &relayv1alpha1.AgentPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: relayv1alpha1.AgentPolicySpec{
			Mode: relayv1alpha1.PolicyModeEnforced,
			PolicyRules: relayv1alpha1.PolicyRules{
				DeniedPaths: []string{path},
			},
		},
	}
	Expect(k8sClient.Create(ctx, ap)).To(Succeed())
}

func createRuntimeProfileWithToolGateway(ctx context.Context, namespace, name string) {
	GinkgoHelper()
	enabled := true
	rp := &relayv1alpha1.RuntimeProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: relayv1alpha1.RuntimeProfileSpec{
			Pod: &relayv1alpha1.RuntimeProfilePodSpec{
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
			},
			Container: &relayv1alpha1.RuntimeProfileContainerSpec{
				AllowPrivilegeEscalation: boolPtr(false),
			},
			Sidecars: []relayv1alpha1.RuntimeProfileSidecar{{
				Name:    "tools",
				Type:    relayjob.SidecarTypeToolGateway,
				Enabled: &enabled,
			}},
		},
	}
	Expect(k8sClient.Create(ctx, rp)).To(Succeed())
}

func createRuntimeProfileWithDNSProxy(ctx context.Context, namespace, name string) {
	GinkgoHelper()
	enabled := true
	rp := &relayv1alpha1.RuntimeProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: relayv1alpha1.RuntimeProfileSpec{
			Pod: &relayv1alpha1.RuntimeProfilePodSpec{
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
			},
			Container: &relayv1alpha1.RuntimeProfileContainerSpec{
				AllowPrivilegeEscalation: boolPtr(false),
			},
			Sidecars: []relayv1alpha1.RuntimeProfileSidecar{{
				Name:    "egress",
				Type:    relayjob.SidecarTypeDNSProxy,
				Enabled: &enabled,
			}},
		},
	}
	Expect(k8sClient.Create(ctx, rp)).To(Succeed())
}

func createRuntimeProfileWithFSGateway(ctx context.Context, namespace, name string) {
	GinkgoHelper()
	enabled := true
	rp := &relayv1alpha1.RuntimeProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: relayv1alpha1.RuntimeProfileSpec{
			Pod: &relayv1alpha1.RuntimeProfilePodSpec{
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
			},
			Container: &relayv1alpha1.RuntimeProfileContainerSpec{
				AllowPrivilegeEscalation: boolPtr(false),
			},
			Sidecars: []relayv1alpha1.RuntimeProfileSidecar{{
				Name:    "files",
				Type:    relayjob.SidecarTypeFSGateway,
				Enabled: &enabled,
			}},
		},
	}
	Expect(k8sClient.Create(ctx, rp)).To(Succeed())
}

// createRuntimeProfile creates a RuntimeProfile in the test namespace.
// Uses pod seccomp only so busybox samples can still succeed in e2e.
func createRuntimeProfile(ctx context.Context, namespace, name string) {
	GinkgoHelper()
	rp := &relayv1alpha1.RuntimeProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: relayv1alpha1.RuntimeProfileSpec{
			Pod: &relayv1alpha1.RuntimeProfilePodSpec{
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
			},
			Container: &relayv1alpha1.RuntimeProfileContainerSpec{
				AllowPrivilegeEscalation: boolPtr(false),
			},
		},
	}
	Expect(k8sClient.Create(ctx, rp)).To(Succeed())
}
