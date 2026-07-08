/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package lockverify implements the verified-or-refused gate for the NetworkPolicy
// routing lock (docs/design/untamperable-enforcement.md §4, issue #70).
//
// A NetworkPolicy is only a declaration: the API server accepts it on every cluster,
// but packets are only blocked if the CNI enforces it — and no API reports whether it
// does. The only trustworthy answer is empirical: run a differential canary probe (a
// pod behind a deny-all egress policy and an identical control pod without one, both
// attempting the same in-cluster connection) and observe whether the deny-all held.
// Enforced-mode sessions whose enforcement substrate is the lock must not run until
// the probe has proven the substrate real. There is no attestation override.
package lockverify

import (
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Verdict is the cached outcome of the differential probe.
type Verdict string

const (
	// VerdictVerified — the deny-all policy blocked the locked pod while the control
	// pod connected: the CNI enforces NetworkPolicy; the routing lock is real.
	VerdictVerified Verdict = "Verified"
	// VerdictRefused — both pods connected: the CNI ignored the deny-all policy.
	// Enforced sessions must be refused.
	VerdictRefused Verdict = "Refused"
	// VerdictUnknown — no conclusive probe has completed yet (startup, probe pods
	// unschedulable, or the control pod could not connect ⇒ broken network). Enforced
	// sessions fail closed on Unknown; a later conclusive probe replaces it.
	VerdictUnknown Verdict = "Unknown"
)

const (
	// probe object names are fixed: one probe runs at a time, in the controller's
	// namespace, and deterministic names make leftovers visible and adoptable.
	LockedPodName  = "scrutineer-lockprobe-locked"
	ControlPodName = "scrutineer-lockprobe-control"
	PolicyName     = "scrutineer-lockprobe-deny-all"

	labelKey    = "scrutineer.sh/lock-probe"
	labelLocked = "locked"
	labelCtrl   = "control"

	// DefaultProbeImage is the controller's own image — already pullable wherever the
	// controller runs, so the probe adds no registry/airgap surface. Keep in sync with
	// VERSION in the Makefile (the release workflow's verify-version guard checks it).
	DefaultProbeImage = "ghcr.io/grantbarry29/scrutineer:v0.1.0"
)

// DenyAllPolicy is the deny-all-egress NetworkPolicy selecting only the locked probe pod.
func DenyAllPolicy(namespace string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PolicyName,
			Namespace: namespace,
			Labels:    map[string]string{labelKey: labelLocked},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{labelKey: labelLocked},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			// No egress rules: deny everything.
		},
	}
}

// ProbePods returns the locked and control probe pods. Both run image (the controller
// image) in probe mode against the same target; the only difference is the label that
// puts the locked pod under the deny-all policy. Restricted-PSA-compliant so admission
// controllers on hardened clusters pass them.
func ProbePods(namespace, image, target string) (locked, control *corev1.Pod) {
	return probePod(namespace, image, target, LockedPodName, labelLocked),
		probePod(namespace, image, target, ControlPodName, labelCtrl)
}

func probePod(namespace, image, target, name, role string) *corev1.Pod {
	no := false
	yes := true
	uid := int64(65532)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{labelKey: role},
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			AutomountServiceAccountToken: &no,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   &yes,
				RunAsUser:      &uid,
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{{
				Name:            "probe",
				Image:           image,
				ImagePullPolicy: corev1.PullIfNotPresent,
				Args:            []string{"--lock-probe-target=" + target},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: &no,
					ReadOnlyRootFilesystem:   &yes,
					RunAsNonRoot:             &yes,
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("10m"),
						corev1.ResourceMemory: resource.MustParse("16Mi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("64Mi"),
					},
				},
			}},
		},
	}
}

// podOutcome is a probe pod's terminal result.
type podOutcome int

const (
	outcomePending   podOutcome = iota // not terminal yet / never ran
	outcomeConnected                   // probe process exited 0: TCP connect succeeded
	outcomeBlocked                     // probe process exited non-zero: connect failed
)

// outcomeOf maps a probe pod's status to its outcome. Only a terminated probe
// container is conclusive; anything else (pending, running, image errors) is not.
func outcomeOf(pod *corev1.Pod) podOutcome {
	for i := range pod.Status.ContainerStatuses {
		st := pod.Status.ContainerStatuses[i]
		if st.Name != "probe" || st.State.Terminated == nil {
			continue
		}
		if st.State.Terminated.ExitCode == 0 {
			return outcomeConnected
		}
		return outcomeBlocked
	}
	return outcomePending
}

// Decide converts the two probe outcomes into a Verdict.
//
//	control connected + locked blocked   ⇒ Verified (deny-all held)
//	control connected + locked connected ⇒ Refused  (deny-all ignored)
//	control blocked/pending              ⇒ Unknown  (broken network / probe never ran —
//	                                       the locked result is uninterpretable)
//	locked pending                       ⇒ Unknown
func Decide(control, locked *corev1.Pod) Verdict {
	if outcomeOf(control) != outcomeConnected {
		return VerdictUnknown
	}
	switch outcomeOf(locked) {
	case outcomeBlocked:
		return VerdictVerified
	case outcomeConnected:
		return VerdictRefused
	default:
		return VerdictUnknown
	}
}
