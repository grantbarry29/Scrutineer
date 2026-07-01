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
type explicitProxyEgressBackend struct{}

func (explicitProxyEgressBackend) manages(profile *scrutineerv1alpha1.RuntimeProfile) bool {
	return profileEnablesEnvoy(profile)
}

func (explicitProxyEgressBackend) desiredObjects(session *scrutineerv1alpha1.AgentSession) []client.Object {
	ns := session.Namespace
	name := session.Name
	// The Pod runs as its dedicated per-session ServiceAccount (envoy.ResourceName).
	return []client.Object{
		envoy.ServiceAccount(name, ns),
		envoy.ConfigMap(name, ns),
		envoy.Service(name, ns),
		envoy.Pod(name, ns, envoy.ResourceName(name), envoy.DefaultEnvoyImage),
	}
}

// profileEnablesEnvoy reports whether the RuntimeProfile opts the session into the
// out-of-pod Envoy egress proxy.
func profileEnablesEnvoy(profile *scrutineerv1alpha1.RuntimeProfile) bool {
	if profile == nil {
		return false
	}
	for _, sc := range profile.Spec.Sidecars {
		if sc.Type == scrutineerjob.SidecarTypeEnvoy && (sc.Enabled == nil || *sc.Enabled) {
			return true
		}
	}
	return false
}

func (r *AgentSessionReconciler) ensureEgressBackend() {
	if r.egress == nil {
		r.egress = explicitProxyEgressBackend{}
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
		session.Status.EgressProxyEndpoint = fmt.Sprintf("http://%s:%d", ip, envoy.ProxyPort)
	}
	return nil
}

// ensureEgressObject creates obj (owner-referenced to the session) if it does not yet
// exist. Envoy pods and their config are effectively immutable per session, so an existing,
// session-owned object is left untouched. An object of the same name not owned by the
// session is a hard conflict.
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
		return nil
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
