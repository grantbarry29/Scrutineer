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
	"net"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	scrutineerjob "github.com/grantbarry29/scrutineer/internal/controller/job"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
)

// egressBackend forces agent egress through the per-session chokepoint of the
// evidence-integrity design (docs/design/evidence-integrity.md, #8). The interim
// explicitProxyEgressBackend provisions a per-session Envoy pod and relies on the agent's
// explicit-proxy env (set by the job builder). A future portable node interceptor (#64)
// implements this interface with transparent redirect — swapping the mechanism without
// touching the reconciler's create/reconcile/teardown loop.
//
// desiredObjects is a function of the session alone (deterministic names/specs), so the
// same set drives both provisioning and teardown; manages(profile) is the enablement gate.
type egressBackend interface {
	// manages reports whether this backend should provision egress for the profile.
	manages(profile *scrutineerv1alpha1.RuntimeProfile) bool
	// desiredObjects returns the per-session objects to reconcile. The reconciler owns
	// owner references, creation, and deletion; the backend owns only what to create.
	desiredObjects(session *scrutineerv1alpha1.AgentSession) []client.Object
}

// explicitProxyEgressBackend provisions a per-session, out-of-pod Envoy forward proxy
// (own identity, own netns) and routes the agent to it via explicit-proxy env. It is the
// interim routing mechanism; the mandatory, non-bypassable lock is the default-deny egress
// NetworkPolicy (Slice B, #61).
type explicitProxyEgressBackend struct {
	// rotateAfterBytes overrides the egress-reporter's access-log rotation threshold
	// (#98); zero keeps the reporter's default. Plumbed from the manager env so e2e and
	// operators can tune it without an image rebuild.
	rotateAfterBytes int64
}

func (explicitProxyEgressBackend) manages(profile *scrutineerv1alpha1.RuntimeProfile) bool {
	return profileEnablesEnvoy(profile)
}

func (b explicitProxyEgressBackend) desiredObjects(session *scrutineerv1alpha1.AgentSession) []client.Object {
	ns := session.Namespace
	name := session.Name
	// The Envoy config carries the session's effective FQDN policy so denied/not-allowed
	// domains are blocked at the chokepoint in enforced mode (#32). Read from
	// status.effectivePolicy via the shared session context.
	bootstrap := egressBootstrapConfig(session)
	// The Pod runs as its dedicated per-session ServiceAccount (envoy.ResourceName). The
	// egress-reporter container beside Envoy submits observed evidence with that
	// identity's projected token (Slice C, #62); URL/audience come from the job package
	// because envoy cannot import it back.
	return []client.Object{
		envoy.ServiceAccount(name, ns),
		envoy.ConfigMap(name, ns, bootstrap),
		envoy.Service(name, ns),
		envoy.Pod(name, ns, envoy.PodConfig{
			ServiceAccount:   envoy.ResourceName(name),
			Image:            envoy.DefaultEnvoyImage,
			ReporterImage:    envoy.DefaultEgressReporterImage(),
			ReporterURL:      scrutineerjob.DefaultReporterURL,
			ReporterAudience: scrutineerjob.ReporterTokenAudience,
			Bootstrap:        bootstrap,
			RotateAfterBytes: b.rotateAfterBytes,
		}),
	}
}

// egressBootstrapConfig derives the Envoy bootstrap FQDN policy from the session's
// effective policy in status. Enforcement is on only in enforced mode; audit/dry-run leave
// Envoy forwarding freely (the egress-reporter records would-be-denials as dry-run, #32).
func egressBootstrapConfig(session *scrutineerv1alpha1.AgentSession) envoy.BootstrapConfig {
	cfg := envoy.BootstrapConfig{}
	ep := session.Status.EffectivePolicy
	if ep == nil {
		return cfg
	}
	cfg.Enforce = ep.Mode == scrutineerv1alpha1.PolicyModeEnforced
	cfg.AllowedDomains = ep.PolicyRules.AllowedDomains
	cfg.DeniedDomains = ep.PolicyRules.DeniedDomains
	return cfg
}

// profileEnablesEnvoy reports whether the RuntimeProfile opts the session into the
// out-of-pod Envoy egress proxy.
func profileEnablesEnvoy(profile *scrutineerv1alpha1.RuntimeProfile) bool {
	if profile == nil {
		return false
	}
	for _, sc := range profile.Spec.Enforcement {
		if sc.Type == scrutineerjob.EnforcementTypeEnvoy && (sc.Enabled == nil || *sc.Enabled) {
			return true
		}
	}
	return false
}

func (r *AgentSessionReconciler) ensureEgressBackend() {
	if r.egress == nil {
		r.egress = explicitProxyEgressBackend{rotateAfterBytes: r.EgressRotateAfterBytes}
	}
}

// ensureEgressProxy reconciles the per-session egress-proxy objects. It provisions them
// while the session is live and the profile enables egress, and tears them down when the
// session is terminal or egress is disabled. Idempotent: re-running against an unchanged
// cluster makes no API mutations.
func (r *AgentSessionReconciler) ensureEgressProxy(ctx context.Context, session *scrutineerv1alpha1.AgentSession, profile *scrutineerv1alpha1.RuntimeProfile) error {
	if session == nil {
		return nil
	}
	r.ensureEgressBackend()
	objs := r.egress.desiredObjects(session)

	if !r.egress.manages(profile) || isTerminal(session.Status.Phase) {
		return r.deleteEgressObjects(ctx, objs)
	}
	for _, obj := range objs {
		if err := r.ensureEgressObject(ctx, session, obj); err != nil {
			return err
		}
	}
	return nil
}

// resolveEgressProxyEndpoint records status.egressProxyEndpoint as the Envoy Service's
// ClusterIP URL so the agent reaches the proxy by IP and needs no DNS (the Slice B routing
// lock denies direct DNS). It must run after the Envoy Service is provisioned and before the
// agent runtime is built, so the agent's proxy env is correct on first creation. The
// uncached APIReader avoids a first-reconcile cache miss on the just-created Service.
func (r *AgentSessionReconciler) resolveEgressProxyEndpoint(ctx context.Context, session *scrutineerv1alpha1.AgentSession, profile *scrutineerv1alpha1.RuntimeProfile) error {
	if !profileEnablesEnvoy(profile) {
		session.Status.EgressProxyEndpoint = ""
		return nil
	}
	key := client.ObjectKey{Namespace: session.Namespace, Name: envoy.ResourceName(session.Name)}
	var svc corev1.Service
	reader := client.Reader(r.Client)
	if r.APIReader != nil {
		reader = r.APIReader
	}
	if err := reader.Get(ctx, key, &svc); err != nil {
		if apierrors.IsNotFound(err) {
			return nil // not provisioned yet; a later reconcile resolves it
		}
		return fmt.Errorf("get egress proxy Service %s: %w", key, err)
	}
	if ip := svc.Spec.ClusterIP; ip != "" && ip != corev1.ClusterIPNone {
		// net.JoinHostPort brackets IPv6 literals (dual-stack clusters); "1.2.3.4" and
		// "fd00::1" both render into a valid proxy URL.
		session.Status.EgressProxyEndpoint = "http://" + net.JoinHostPort(ip, strconv.Itoa(envoy.ProxyPort))
	}
	return nil
}

// ensureEgressObject creates obj (owner-referenced to the session) if it does not yet
// exist, and reconciles FQDN-policy drift (#32). An existing, session-owned object whose
// egress-config hash still matches is left untouched. On a hash change: the ConfigMap is
// updated in place, and the Pod is deleted so it is recreated with the new config on the
// next reconcile (Envoy reads its bootstrap once at start; a mounted ConfigMap does not hot
// reload). A Pod that failed in place (kubelet eviction) gets cause-aware handling before
// any of that (#99, handleFailedEgressPod). Service/ServiceAccount carry no policy, so
// their hash never changes. An object of the same name not owned by the session is a hard
// conflict.
func (r *AgentSessionReconciler) ensureEgressObject(ctx context.Context, session *scrutineerv1alpha1.AgentSession, obj client.Object) error {
	existing, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return fmt.Errorf("egress object %T is not a client.Object", obj)
	}
	err := r.Get(ctx, client.ObjectKeyFromObject(obj), existing)
	if err == nil {
		if !metav1.IsControlledBy(existing, session) {
			return fmt.Errorf("egress-proxy %T %q is not owned by AgentSession %q",
				obj, obj.GetName(), session.Name)
		}
		if pod, isPod := existing.(*corev1.Pod); isPod {
			if pod.Status.Phase == corev1.PodFailed {
				return r.handleFailedEgressPod(ctx, session, pod)
			}
			setCondition(session, ConditionEgressProxyHealthy, metav1.ConditionTrue,
				ReasonProxyProvisioned, "egress-proxy pod is provisioned")
		}
		if egressConfigHash(existing) == egressConfigHash(obj) {
			return nil
		}
		return r.reconcileEgressDrift(ctx, session, obj, existing)
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get egress-proxy object %q: %w", obj.GetName(), err)
	}

	if err := controllerutil.SetControllerReference(session, obj, r.Scheme); err != nil {
		return fmt.Errorf("set owner reference on egress-proxy object %q: %w", obj.GetName(), err)
	}
	// Do not block session deletion on the owned object being GC'd first (avoids the
	// finalizer/GC deadlock the Job and NetworkPolicy paths also guard against).
	setBlockOwnerDeletion(obj, false)
	if err := r.Create(ctx, obj); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("create egress-proxy object %q: %w", obj.GetName(), err)
	}
	r.recordNormal(session, EventReasonEgressProxySynced,
		fmt.Sprintf("Created egress proxy %T %q", obj, obj.GetName()))
	return nil
}

// egressConfigHash returns the FQDN-policy hash annotation, or "" if absent.
func egressConfigHash(obj client.Object) string {
	return obj.GetAnnotations()[envoy.ConfigHashAnnotation]
}

// handleFailedEgressPod is the cause-aware response to an egress-proxy pod the kubelet
// failed in place — deletion-based eviction (drain, API-initiated) already recovers via
// the pod watch, but a Failed pod object otherwise satisfies the existence check and the
// session's egress would stay silently dead for its remaining lifetime (#99).
//
// The cause decides recovery (deliberate, per the #98/#99 threat reasoning):
//   - Access-log volume overflow: NOT recreated. The log volume is the session's
//     tamper-evidence; an agent that floods it to force eviction must not be rewarded
//     with a fresh, empty evidence volume and restored egress. The session stays
//     fail-closed (routing lock denies egress without the proxy) with the condition and
//     a Warning event naming the overflow; a human decides.
//   - Anything else (node memory/PID pressure, preemption, node shutdown): the pod is
//     deleted so the next reconcile recreates it — governance continuity outweighs the
//     (already lost) un-ingested tail of the old volume, and the gap is recorded in the
//     event.
func (r *AgentSessionReconciler) handleFailedEgressPod(ctx context.Context, session *scrutineerv1alpha1.AgentSession, pod *corev1.Pod) error {
	cause := pod.Status.Reason
	if cause == "" {
		cause = "PodFailed"
	}
	detail := fmt.Sprintf("egress-proxy pod %q failed in place (%s: %s)", pod.Name, cause, pod.Status.Message)

	if isEvidenceOverflowEviction(pod) {
		setCondition(session, ConditionEgressProxyHealthy, metav1.ConditionFalse,
			ReasonProxyEvidenceOverflow, detail+" — not recreated: evidence volume overflow fails closed")
		r.recordWarning(session, EventReasonEgressProxyFailed,
			detail+"; not recreating (evidence-volume overflow fails closed, #98/#99)")
		return nil
	}

	setCondition(session, ConditionEgressProxyHealthy, metav1.ConditionFalse,
		ReasonProxyPodFailed, detail+" — replacing")
	r.recordWarning(session, EventReasonEgressProxyFailed,
		detail+"; replacing the pod (evidence not yet ingested from the old volume is lost)")
	if err := r.Delete(ctx, pod, client.GracePeriodSeconds(0)); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete failed egress-proxy Pod %q: %w", pod.Name, err)
	}
	return nil
}

// isEvidenceOverflowEviction reports whether the kubelet evicted the pod because the
// access-log emptyDir exceeded its size limit. The kubelet encodes this only in the
// human-readable message ("Usage of EmptyDir volume \"access-log\" exceeds the limit
// ..."), so match the quoted volume name; anything ambiguous is treated as a
// non-overflow failure (recreate) — the overflow hold is a targeted anti-flooding
// stance, not the default.
func isEvidenceOverflowEviction(pod *corev1.Pod) bool {
	return pod.Status.Reason == "Evicted" &&
		strings.Contains(pod.Status.Message, `"`+envoy.AccessLogVolumeName+`"`)
}

// reconcileEgressDrift propagates an FQDN-policy change to an existing egress object. The
// ConfigMap is updated in place; the Pod is deleted so a subsequent reconcile recreates it
// with the new bootstrap/env (the Owns(Pod) watch triggers that reconcile). Other kinds
// carry no policy and never reach here.
func (r *AgentSessionReconciler) reconcileEgressDrift(ctx context.Context, session *scrutineerv1alpha1.AgentSession, desired, existing client.Object) error {
	switch existing.(type) {
	case *corev1.ConfigMap:
		desiredCM := desired.(*corev1.ConfigMap)
		cm := existing.(*corev1.ConfigMap)
		cm.Data = desiredCM.Data
		cm.Annotations = desiredCM.Annotations
		if err := r.Update(ctx, cm); err != nil {
			return fmt.Errorf("update egress-proxy ConfigMap %q: %w", cm.Name, err)
		}
		r.recordNormal(session, EventReasonEgressProxySynced,
			fmt.Sprintf("Updated egress proxy config %q for policy change", cm.Name))
		return nil
	case *corev1.Pod:
		if err := r.Delete(ctx, existing, client.GracePeriodSeconds(0)); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete egress-proxy Pod %q for policy change: %w", existing.GetName(), err)
		}
		r.recordNormal(session, EventReasonEgressProxySynced,
			fmt.Sprintf("Recreating egress proxy pod %q for policy change", existing.GetName()))
		return nil
	default:
		// Non-policy objects (Service/ServiceAccount) never drift; nothing to do.
		return nil
	}
}

// deleteEgressObjects removes the per-session egress objects. Idempotent; NotFound is
// success. A cached Get guards each Delete so the steady state (the many sessions with no
// egress proxy) costs only cheap cached reads, not apiserver DELETEs on every reconcile —
// matching deleteNetworkPolicyIfExists. Pods are deleted with a zero grace period so they
// are actually removed in envtest (which has no kubelet to confirm graceful termination).
func (r *AgentSessionReconciler) deleteEgressObjects(ctx context.Context, objs []client.Object) error {
	for _, obj := range objs {
		existing, ok := obj.DeepCopyObject().(client.Object)
		if !ok {
			return fmt.Errorf("egress object %T is not a client.Object", obj)
		}
		if err := r.Get(ctx, client.ObjectKeyFromObject(obj), existing); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("get egress-proxy object %q: %w", obj.GetName(), err)
		}
		if err := r.Delete(ctx, existing, client.GracePeriodSeconds(0)); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete egress-proxy object %q: %w", obj.GetName(), err)
		}
	}
	return nil
}
