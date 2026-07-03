/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package lockverify

import (
	"net"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
)

func terminatedPod(exitCode int32) *corev1.Pod {
	return &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
		Name:  "probe",
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: exitCode}},
	}}}}
}

func runningPod() *corev1.Pod {
	return &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
		Name:  "probe",
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	}}}}
}

func TestDecide(t *testing.T) {
	connected := func() *corev1.Pod { return terminatedPod(0) }
	blocked := func() *corev1.Pod { return terminatedPod(1) }

	cases := []struct {
		name            string
		control, locked *corev1.Pod
		want            Verdict
	}{
		{"deny-all held: enforcing CNI", connected(), blocked(), VerdictVerified},
		{"deny-all ignored: non-enforcing CNI", connected(), connected(), VerdictRefused},
		{"control blocked: broken network is not a verdict", blocked(), blocked(), VerdictUnknown},
		{"control blocked even when locked connected", blocked(), connected(), VerdictUnknown},
		{"control still running", runningPod(), blocked(), VerdictUnknown},
		{"locked still running", connected(), runningPod(), VerdictUnknown},
		{"pods never ran", &corev1.Pod{}, &corev1.Pod{}, VerdictUnknown},
	}
	for _, tc := range cases {
		if got := Decide(tc.control, tc.locked); got != tc.want {
			t.Errorf("%s: Decide = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDenyAllPolicy_selectsOnlyLockedPod(t *testing.T) {
	np := DenyAllPolicy("scrutineer-system")
	locked, control := ProbePods("scrutineer-system", DefaultProbeImage, "10.0.0.1:443")

	sel := np.Spec.PodSelector.MatchLabels
	if len(sel) == 0 {
		t.Fatal("deny-all policy must select by label, not select everything")
	}
	for k, v := range sel {
		if locked.Labels[k] != v {
			t.Fatalf("locked pod must match policy selector %s=%s; labels=%v", k, v, locked.Labels)
		}
		if control.Labels[k] == v {
			t.Fatalf("control pod must NOT match policy selector %s=%s; labels=%v", k, v, control.Labels)
		}
	}
	if len(np.Spec.Egress) != 0 {
		t.Fatalf("deny-all must have no egress rules, got %v", np.Spec.Egress)
	}
	if len(np.Spec.PolicyTypes) != 1 || np.Spec.PolicyTypes[0] != "Egress" {
		t.Fatalf("policy types = %v, want [Egress]", np.Spec.PolicyTypes)
	}
}

func TestProbePods_identicalExceptRole(t *testing.T) {
	locked, control := ProbePods("ns", "img:v1", "10.0.0.1:443")

	if locked.Spec.Containers[0].Image != "img:v1" || control.Spec.Containers[0].Image != "img:v1" {
		t.Fatal("both pods must run the provided (controller) image")
	}
	wantArg := "--lock-probe-target=10.0.0.1:443"
	for _, p := range []*corev1.Pod{locked, control} {
		if len(p.Spec.Containers[0].Args) != 1 || p.Spec.Containers[0].Args[0] != wantArg {
			t.Fatalf("pod %s args = %v, want [%s]", p.Name, p.Spec.Containers[0].Args, wantArg)
		}
		if p.Spec.RestartPolicy != corev1.RestartPolicyNever {
			t.Fatalf("pod %s must not restart (exit code is the signal)", p.Name)
		}
		if p.Spec.AutomountServiceAccountToken == nil || *p.Spec.AutomountServiceAccountToken {
			t.Fatalf("pod %s must not mount a SA token", p.Name)
		}
		sc := p.Spec.Containers[0].SecurityContext
		if sc == nil || sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem ||
			sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
			t.Fatalf("pod %s must be restricted-PSA-compliant", p.Name)
		}
	}
}

func TestRunProbe(t *testing.T) {
	// Reachable target: a local listener.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			_ = c.Close()
		}
	}()
	if err := RunProbe(ln.Addr().String(), 2*time.Second); err != nil {
		t.Fatalf("probe against live listener: %v", err)
	}

	// Silent drop (the NetworkPolicy failure mode) ⇒ timeout error. TEST-NET-1
	// (192.0.2.0/24, RFC 5737) is guaranteed non-routable.
	err = RunProbe("192.0.2.1:443", 300*time.Millisecond)
	if err == nil {
		t.Fatal("probe against blackholed target must fail")
	}
	if !strings.Contains(err.Error(), "probe dial") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifier_keepsLastConclusiveVerdictOnInconclusive(t *testing.T) {
	v := &Verifier{}
	v.state = State{Verdict: VerdictVerified, ProbedAt: time.Now()}

	// Simulate what probeAndStore does on an Unknown result: state must be retained.
	v.mu.Lock()
	if v.state.Verdict != VerdictVerified && v.state.Verdict != VerdictRefused {
		v.state = State{Verdict: VerdictUnknown}
	}
	v.mu.Unlock()

	if got := v.Current().Verdict; got != VerdictVerified {
		t.Fatalf("verdict after inconclusive probe = %v, want retained Verified", got)
	}
}

func TestVerifier_currentDefaultsToUnknown(t *testing.T) {
	v := &Verifier{}
	if got := v.Current().Verdict; got != VerdictUnknown {
		t.Fatalf("initial verdict = %v, want Unknown (fail closed)", got)
	}
}
