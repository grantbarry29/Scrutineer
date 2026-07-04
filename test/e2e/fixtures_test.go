//go:build e2e

/*
Copyright 2026 The Scrutineer Authors.

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

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/controller/agentsession"
	scrutineerjob "github.com/grantbarry29/scrutineer/internal/controller/job"
	"github.com/grantbarry29/scrutineer/internal/enforcement/networkpolicy"
)

// agentSessionOption mutates an AgentSession during construction.
type agentSessionOption func(*scrutineerv1alpha1.AgentSession)

func withTemperature(t string) agentSessionOption {
	return func(s *scrutineerv1alpha1.AgentSession) { s.Spec.Model.Temperature = strPtr(t) }
}

func withCommand(cmd ...string) agentSessionOption {
	return func(s *scrutineerv1alpha1.AgentSession) { s.Spec.Runtime.Command = cmd }
}

func withOrchestrator(orchestrator string) agentSessionOption {
	return func(s *scrutineerv1alpha1.AgentSession) { s.Spec.Runtime.Orchestrator = orchestrator }
}

func withoutTask() agentSessionOption {
	return func(s *scrutineerv1alpha1.AgentSession) {
		s.Spec.Task = scrutineerv1alpha1.SessionTaskSpec{}
	}
}

func withPromptConfigMapRef(name, key string) agentSessionOption {
	return func(s *scrutineerv1alpha1.AgentSession) {
		s.Spec.Task.Prompt = ""
		s.Spec.Task.PromptConfigMapRef = &scrutineerv1alpha1.PromptConfigMapRef{
			Name: name,
			Key:  key,
		}
	}
}

func withCancelRequested() agentSessionOption {
	return func(s *scrutineerv1alpha1.AgentSession) { s.Spec.CancelRequested = true }
}

func withLongRunningCommand() agentSessionOption {
	return func(s *scrutineerv1alpha1.AgentSession) {
		s.Spec.Runtime.Command = []string{"sh", "-c", "echo running; sleep 300"}
	}
}

func withRuntimeProfileRef(name string) agentSessionOption {
	return func(s *scrutineerv1alpha1.AgentSession) {
		s.Spec.RuntimeProfileRef = &scrutineerv1alpha1.RuntimeProfileRef{Name: name}
	}
}

func withPolicyRef(kind, name string) agentSessionOption {
	return func(s *scrutineerv1alpha1.AgentSession) {
		s.Spec.PolicyRefs = append(s.Spec.PolicyRefs, scrutineerv1alpha1.PolicyRef{Kind: kind, Name: name})
	}
}

func withExitCommand(exitCode int) agentSessionOption {
	return func(s *scrutineerv1alpha1.AgentSession) {
		s.Spec.Runtime.Command = []string{"sh", "-c", fmt.Sprintf("exit %d", exitCode)}
	}
}

func withTimeoutSeconds(sec int64) agentSessionOption {
	return func(s *scrutineerv1alpha1.AgentSession) {
		s.Spec.Runtime.TimeoutSeconds = &sec
	}
}

// withSleepExceedingTimeout runs longer than typical timeoutSeconds used in TimedOut e2e.
func withSleepExceedingTimeout() agentSessionOption {
	return func(s *scrutineerv1alpha1.AgentSession) {
		s.Spec.Runtime.Command = []string{"sh", "-c", "sleep 120"}
	}
}

// withEnvoyEgressProbe makes the busybox agent exercise its per-session Envoy egress
// proxy after startup: an HTTP GET via the injected proxy env, plus a raw CONNECT sent
// straight at the proxy (proves the HTTPS tunnel path deterministically — busybox's own
// wget resolves https hosts locally rather than tunneling via a proxy). Both target Envoy
// by ClusterIP (derived from the injected proxy env), never a DNS name: under the routing
// lock direct DNS is denied, so a name would not resolve. host is intentionally
// non-resolvable — the assertion is on Envoy's access log, not upstream success.
func withEnvoyEgressProbe(host string) agentSessionOption {
	return func(s *scrutineerv1alpha1.AgentSession) {
		script := fmt.Sprintf(`sleep 12
ENVOY_IP=$(printf '%%s' "${http_proxy:-$HTTP_PROXY}" | sed 's|^http://||; s|:.*$||')
for i in $(seq 1 40); do
  wget -q -O /dev/null "http://%[1]s/" 2>/dev/null || true
  printf 'CONNECT %[1]s:443 HTTP/1.1\r\nHost: %[1]s:443\r\n\r\n' | nc -w 3 "$ENVOY_IP" 15001 2>/dev/null || true
  sleep 2
done
sleep 120`, host)
		s.Spec.Runtime.Command = []string{"sh", "-c", script}
	}
}

// withNetpolEgressProbe exercises the Slice B routing lock on a NetworkPolicy-enforcing
// cluster. The agent (busybox) continuously: (1) requests host via its injected proxy
// (should reach Envoy — Envoy's access log is the proof); (2) TCP-connects to its Envoy by
// ClusterIP (positive control — the lock allows Envoy, and confirms nc itself works); and
// direct-egress NEGATIVES that must be dropped by the lock: (3) DNS resolution, and (4) a
// TCP connect to a non-Envoy in-cluster pod ($PROBE_TARGET_IP, e.g. a kube-dns pod). It prints
// PROBE_* markers to stdout for the spec to assert on. Envoy's ClusterIP is derived from the
// injected proxy env, so nothing about the (dynamic) proxy address needs to be known upfront.
func withNetpolEgressProbe(host string) agentSessionOption {
	return func(s *scrutineerv1alpha1.AgentSession) {
		script := fmt.Sprintf(`sleep 12
ENVOY_IP=$(printf '%%s' "${http_proxy:-$HTTP_PROXY}" | sed 's|^http://||; s|:.*$||')
i=0
while [ $i -lt 40 ]; do
  i=$((i+1))
  wget -q -O /dev/null "http://%[1]s/" 2>/dev/null || true
  if timeout 5 nc -w 3 "$ENVOY_IP" 15001 </dev/null >/dev/null 2>&1; then echo "PROBE_ENVOY_TCP=OK"; else echo "PROBE_ENVOY_TCP=FAIL"; fi
  if timeout 5 nslookup kubernetes.default.svc.cluster.local >/dev/null 2>&1; then echo "PROBE_DNS=OK"; else echo "PROBE_DNS=BLOCKED"; fi
  if timeout 5 nc -w 3 "$PROBE_TARGET_IP" 53 </dev/null >/dev/null 2>&1; then echo "PROBE_DIRECT=OK"; else echo "PROBE_DIRECT=BLOCKED"; fi
  sleep 3
done
sleep 120`, host)
		s.Spec.Runtime.Command = []string{"sh", "-c", script}
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
func newAgentSession(namespace, name string, opts ...agentSessionOption) *scrutineerv1alpha1.AgentSession {
	s := &scrutineerv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: scrutineerv1alpha1.AgentSessionSpec{
			Task: scrutineerv1alpha1.SessionTaskSpec{
				Description: "e2e test session",
				Prompt:      "noop",
			},
			Model: scrutineerv1alpha1.ModelSpec{
				Provider: "openai",
				Name:     "gpt-4.1",
			},
			Runtime: scrutineerv1alpha1.RuntimeSpec{
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
func createAgentSession(ctx context.Context, session *scrutineerv1alpha1.AgentSession) client.ObjectKey {
	GinkgoHelper()
	Expect(k8sClient.Create(ctx, session)).To(Succeed())
	return client.ObjectKeyFromObject(session)
}

// jobNameForSession returns the deterministic Job name the controller creates.
func jobNameForSession(session *scrutineerv1alpha1.AgentSession) string {
	return scrutineerjob.NameFor(session)
}

// netpolNameForSession returns the deterministic NetworkPolicy name the controller creates.
func netpolNameForSession(session *scrutineerv1alpha1.AgentSession) string {
	return networkpolicy.NameFor(session.Namespace, session.Name)
}

// createEnforcedCIDRPolicy creates an AgentPolicy with enforced mode and an allowed CIDR.
func createEnforcedCIDRPolicy(ctx context.Context, namespace, name, cidr string) {
	GinkgoHelper()
	ap := &scrutineerv1alpha1.AgentPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: scrutineerv1alpha1.AgentPolicySpec{
			Mode: scrutineerv1alpha1.PolicyModeEnforced,
			PolicyRules: scrutineerv1alpha1.PolicyRules{
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
	ap := &scrutineerv1alpha1.AgentPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: scrutineerv1alpha1.AgentPolicySpec{
			Mode: scrutineerv1alpha1.PolicyModeEnforced,
			PolicyRules: scrutineerv1alpha1.PolicyRules{
				DeniedDomains: []string{domain},
			},
		},
	}
	Expect(k8sClient.Create(ctx, ap)).To(Succeed())
}

// createRuntimeProfileWithEnvoy enables the out-of-pod per-session Envoy egress proxy.
func createRuntimeProfileWithEnvoy(ctx context.Context, namespace, name string) {
	GinkgoHelper()
	enabled := true
	rp := &scrutineerv1alpha1.RuntimeProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Pod: &scrutineerv1alpha1.RuntimeProfilePodSpec{
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
			},
			Container: &scrutineerv1alpha1.RuntimeProfileContainerSpec{
				AllowPrivilegeEscalation: boolPtr(false),
			},
			Enforcement: []scrutineerv1alpha1.RuntimeProfileEnforcement{{
				Name:    "envoy",
				Type:    scrutineerjob.EnforcementTypeEnvoy,
				Enabled: &enabled,
			}},
		},
	}
	Expect(k8sClient.Create(ctx, rp)).To(Succeed())
}

// createRuntimeProfileWithEnvoyAutomount is the envoy egress profile plus the Slice D
// (#63) SA-token automount opt-in, for agents that legitimately need the Kubernetes API.
func createRuntimeProfileWithEnvoyAutomount(ctx context.Context, namespace, name string) {
	GinkgoHelper()
	enabled := true
	rp := &scrutineerv1alpha1.RuntimeProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Pod: &scrutineerv1alpha1.RuntimeProfilePodSpec{
				SeccompProfile:               &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
				AutomountServiceAccountToken: boolPtr(true),
			},
			Container: &scrutineerv1alpha1.RuntimeProfileContainerSpec{
				AllowPrivilegeEscalation: boolPtr(false),
			},
			Enforcement: []scrutineerv1alpha1.RuntimeProfileEnforcement{{
				Name:    "envoy",
				Type:    scrutineerjob.EnforcementTypeEnvoy,
				Enabled: &enabled,
			}},
		},
	}
	Expect(k8sClient.Create(ctx, rp)).To(Succeed())
}

// withApiserverViaEnvoyProbe (Slice D, #63) proves an API-needing agent works under the
// routing lock: it reports whether its SA token is mounted (TOKEN=present/absent) and
// repeatedly CONNECTs to the apiserver by name *through* its Envoy proxy (Envoy resolves
// the name and the lock permits only Envoy), so the apiserver authority shows up in
// Envoy's access log — apiserver access transits the chokepoint like all other egress.
func withApiserverViaEnvoyProbe() agentSessionOption {
	return func(s *scrutineerv1alpha1.AgentSession) {
		s.Spec.Runtime.Command = []string{"sh", "-c", `sleep 12
if [ -s /var/run/secrets/kubernetes.io/serviceaccount/token ]; then echo TOKEN=present; else echo TOKEN=absent; fi
ENVOY_IP=$(printf '%s' "${http_proxy:-$HTTP_PROXY}" | sed 's|^http://||; s|:.*$||')
for i in $(seq 1 40); do
  printf 'CONNECT kubernetes.default.svc:443 HTTP/1.1\r\nHost: kubernetes.default.svc:443\r\n\r\n' | nc -w 3 "$ENVOY_IP" 15001 2>/dev/null || true
  sleep 2
done
sleep 120`}
	}
}

// createRuntimeProfile creates a RuntimeProfile in the test namespace.
// Uses pod seccomp only so busybox samples can still succeed in e2e.
func createRuntimeProfile(ctx context.Context, namespace, name string) {
	GinkgoHelper()
	rp := &scrutineerv1alpha1.RuntimeProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: scrutineerv1alpha1.RuntimeProfileSpec{
			Pod: &scrutineerv1alpha1.RuntimeProfilePodSpec{
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
			},
			Container: &scrutineerv1alpha1.RuntimeProfileContainerSpec{
				AllowPrivilegeEscalation: boolPtr(false),
			},
		},
	}
	Expect(k8sClient.Create(ctx, rp)).To(Succeed())
}
