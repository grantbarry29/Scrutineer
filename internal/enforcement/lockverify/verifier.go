/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package lockverify

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// DefaultReprobeInterval is how often the verifier re-runs the differential probe
	// in the background. Enforcement substrates do not flap often; the interval only
	// bounds how quickly a CNI change is noticed.
	DefaultReprobeInterval = 10 * time.Minute
	// defaultPodWait bounds how long one probe run waits for both pods to finish
	// (scheduling + image present + a blocked dial timing out).
	defaultPodWait = 90 * time.Second
	pollInterval   = 2 * time.Second
)

// State is the verifier's cached result. On an inconclusive probe the previous
// conclusive verdict is retained (design: keep last-known-good) — ProbedAt always
// reports when the retained verdict was established.
type State struct {
	Verdict  Verdict
	ProbedAt time.Time
}

// Verifier runs the differential canary probe and caches the verdict for the
// reconciler gate. It implements manager.Runnable: probe once at startup, then
// periodically.
type Verifier struct {
	Client client.Client
	// Reader is an uncached reader (mgr.GetAPIReader) used for read-after-create on the
	// throwaway probe objects: the cached client's informer lags a Create, so an
	// immediate cached Get of a just-created probe pod returns NotFound and would abort
	// the probe. Falls back to Client when nil (unit tests with a fake client).
	Reader client.Reader
	// Namespace the probe objects are created in (the controller's namespace).
	Namespace string
	// Image is the probe pod image — the controller's own image (DefaultProbeImage).
	Image string
	// Interval between background re-probes (DefaultReprobeInterval when zero).
	Interval time.Duration
	// PodWait bounds one probe run (defaultPodWait when zero).
	PodWait time.Duration

	mu    sync.RWMutex
	state State
}

// reader returns the strong-consistency reader for probe objects (uncached when wired).
func (v *Verifier) reader() client.Reader {
	if v.Reader != nil {
		return v.Reader
	}
	return v.Client
}

// Current returns the cached state. Verdict is VerdictUnknown until the first
// conclusive probe completes.
func (v *Verifier) Current() State {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.state.Verdict == "" {
		return State{Verdict: VerdictUnknown}
	}
	return v.state
}

// NeedLeaderElection makes the verifier run only on the elected leader, matching the
// reconcilers its verdict gates.
func (v *Verifier) NeedLeaderElection() bool { return true }

// Start implements manager.Runnable: an initial probe, then periodic re-probes until
// the context ends.
func (v *Verifier) Start(ctx context.Context) error {
	v.probeAndStore(ctx)
	ticker := time.NewTicker(v.interval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			v.probeAndStore(ctx)
		}
	}
}

func (v *Verifier) probeAndStore(ctx context.Context) {
	log := logf.FromContext(ctx).WithName("lockverify")
	verdict, err := v.RunOnce(ctx)
	if err != nil {
		log.Error(err, "lock probe run failed; treating as inconclusive")
		verdict = VerdictUnknown
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	switch {
	case verdict != VerdictUnknown:
		v.state = State{Verdict: verdict, ProbedAt: time.Now()}
		log.Info("lock probe verdict", "verdict", verdict)
	case v.state.Verdict == VerdictVerified || v.state.Verdict == VerdictRefused:
		// Inconclusive: keep the last conclusive verdict (design §4).
		log.Info("lock probe inconclusive; keeping last verdict", "verdict", v.state.Verdict)
	default:
		v.state = State{Verdict: VerdictUnknown}
	}
}

// RunOnce executes one differential probe: create the deny-all policy and both probe
// pods, wait for terminal outcomes, decide, and clean up. Unknown (not an error) is
// returned for indeterminate runs — broken network, unschedulable pods, timeout.
func (v *Verifier) RunOnce(ctx context.Context) (Verdict, error) {
	target, err := v.probeTarget(ctx)
	if err != nil {
		return VerdictUnknown, err
	}

	// Remove leftovers from a crashed prior run, then create fresh objects.
	if err := v.cleanup(ctx); err != nil {
		return VerdictUnknown, fmt.Errorf("pre-probe cleanup: %w", err)
	}
	defer func() {
		// Best-effort teardown; leftovers are deterministic names adopted next run.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = v.cleanup(cleanupCtx)
	}()

	policy := DenyAllPolicy(v.Namespace)
	if err := v.Client.Create(ctx, policy); err != nil {
		return VerdictUnknown, fmt.Errorf("create probe policy: %w", err)
	}
	locked, control := ProbePods(v.Namespace, v.Image, target)
	if err := v.Client.Create(ctx, locked); err != nil {
		return VerdictUnknown, fmt.Errorf("create locked probe pod: %w", err)
	}
	if err := v.Client.Create(ctx, control); err != nil {
		return VerdictUnknown, fmt.Errorf("create control probe pod: %w", err)
	}

	deadline := time.Now().Add(v.podWait())
	for {
		var lockedPod, controlPod corev1.Pod
		lErr := v.reader().Get(ctx, client.ObjectKey{Namespace: v.Namespace, Name: LockedPodName}, &lockedPod)
		cErr := v.reader().Get(ctx, client.ObjectKey{Namespace: v.Namespace, Name: ControlPodName}, &controlPod)
		switch {
		case apierrors.IsNotFound(lErr) || apierrors.IsNotFound(cErr):
			// Read-after-create lag: the pods exist (Create succeeded) but this reader
			// has not observed them yet. Fall through to the deadline wait and re-poll.
		case lErr != nil:
			return VerdictUnknown, fmt.Errorf("get locked probe pod: %w", lErr)
		case cErr != nil:
			return VerdictUnknown, fmt.Errorf("get control probe pod: %w", cErr)
		default:
			if verdict := Decide(&controlPod, &lockedPod); verdict != VerdictUnknown {
				return verdict, nil
			}
			// Both outcomes terminal but still Unknown (control blocked) is conclusive
			// enough to stop early: the network is broken, waiting will not fix this run.
			if outcomeOf(&controlPod) == outcomeBlocked {
				return VerdictUnknown, nil
			}
		}
		if time.Now().After(deadline) {
			return VerdictUnknown, nil
		}
		select {
		case <-ctx.Done():
			return VerdictUnknown, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// probeTarget picks the in-cluster TCP target both pods dial: a running kube-dns
// (CoreDNS) POD IP on 53.
//
// It must be a pod-network address. The obvious choice — the kubernetes API Service
// ClusterIP — is wrong: the apiserver is host-network, and egress NetworkPolicy to
// host-network endpoints is NOT enforced on many CNIs (kindnet included), so the
// locked pod would connect even where the CNI enforces pod-to-pod egress, and the
// probe would falsely report Refused. kube-dns is pod-network and present on every
// real cluster, so it reflects genuine pod egress enforcement (this mirrors the
// networking e2e suite's own probe). No DNS resolution is needed — the locked pod
// has none by design; the controller resolves the IP here.
func (v *Verifier) probeTarget(ctx context.Context) (string, error) {
	var pods corev1.PodList
	if err := v.reader().List(ctx, &pods,
		client.InNamespace(metav1.NamespaceSystem),
		client.MatchingLabels{"k8s-app": "kube-dns"}); err != nil {
		return "", fmt.Errorf("list kube-dns pods: %w", err)
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Status.Phase == corev1.PodRunning && p.Status.PodIP != "" {
			return net.JoinHostPort(p.Status.PodIP, "53"), nil
		}
	}
	return "", fmt.Errorf("no running kube-dns pod with an IP found in %s", metav1.NamespaceSystem)
}

func (v *Verifier) cleanup(ctx context.Context) error {
	objs := []client.Object{
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: v.Namespace, Name: LockedPodName}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: v.Namespace, Name: ControlPodName}},
		DenyAllPolicy(v.Namespace),
	}
	for _, o := range objs {
		if err := v.Client.Delete(ctx, o); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	// Wait for pod names to free up (fixed names collide with a terminating pod).
	deadline := time.Now().Add(30 * time.Second)
	for _, name := range []string{LockedPodName, ControlPodName} {
		for {
			var pod corev1.Pod
			err := v.reader().Get(ctx, client.ObjectKey{Namespace: v.Namespace, Name: name}, &pod)
			if apierrors.IsNotFound(err) {
				break
			}
			if err != nil {
				return err
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("probe pod %s still terminating", name)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
	return nil
}

func (v *Verifier) interval() time.Duration {
	if v.Interval > 0 {
		return v.Interval
	}
	return DefaultReprobeInterval
}

func (v *Verifier) podWait() time.Duration {
	if v.PodWait > 0 {
		return v.PodWait
	}
	return defaultPodWait
}
