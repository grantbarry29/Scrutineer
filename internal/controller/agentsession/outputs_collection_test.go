/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	fakekube "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// envtest cannot serve pods/log or pods/exec (no kubelet), so these path-level tests drive
// collectSessionOutputs through the collectLogsFn/execFn seams with canned bytes/errors,
// and assert the collection MVP's core behaviors: once-per-artifact idempotency, the
// 512KiB truncation, empty-output skip, the created ConfigMap/Secret, and the best-effort
// degrade-to-warning at the call site. (#121)

func newOutputsTestReconciler(t *testing.T) (*AgentSessionReconciler, *record.FakeRecorder, client.Client) {
	t.Helper()
	scheme := podTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	rec := record.NewFakeRecorder(16)
	r := &AgentSessionReconciler{
		Client:    cl,
		Scheme:    scheme,
		Recorder:  rec,
		clientset: fakekube.NewSimpleClientset(),
	}
	return r, rec, cl
}

// terminalCollectingSession is a terminal session that requests both outputs.
func terminalCollectingSession() *scrutineerv1alpha1.AgentSession {
	return &scrutineerv1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Name: "s1", Namespace: "ns1", UID: types.UID("uid-1")},
		Spec: scrutineerv1alpha1.AgentSessionSpec{
			Outputs: scrutineerv1alpha1.OutputSpec{CollectLogs: true, CollectArtifacts: true},
		},
		Status: scrutineerv1alpha1.AgentSessionStatus{
			Phase:   scrutineerv1alpha1.PhaseSucceeded,
			PodName: "s1-pod",
		},
	}
}

func logsKey() client.ObjectKey {
	return client.ObjectKey{Namespace: "ns1", Name: outputResourceName("scrutineer-logs-", "s1")}
}

func artifactsKey() client.ObjectKey {
	return client.ObjectKey{Namespace: "ns1", Name: outputResourceName("scrutineer-artifacts-", "s1")}
}

// drainReasons returns the reasons of all buffered events (non-blocking).
func drainReasons(rec *record.FakeRecorder) []string {
	var out []string
	for {
		select {
		case e := <-rec.Events:
			out = append(out, e)
		default:
			return out
		}
	}
}

func containsReason(events []string, reason string) bool {
	for _, e := range events {
		if bytes.Contains([]byte(e), []byte(reason)) {
			return true
		}
	}
	return false
}

func TestCollectSessionOutputs_collectsAndIsIdempotent(t *testing.T) {
	r, rec, cl := newOutputsTestReconciler(t)
	var logCalls, execCalls int
	r.collectLogsFn = func(_ context.Context, _ kubernetes.Interface, _, _, _ string, _ int64) ([]byte, error) {
		logCalls++
		return []byte("hello logs"), nil
	}
	r.execFn = func(_ context.Context, _ kubernetes.Interface, _, _ string, _ []string, stdout, _ io.Writer) error {
		execCalls++
		_, err := stdout.Write([]byte("tarbytes"))
		return err
	}
	session := terminalCollectingSession()

	if err := r.collectSessionOutputs(context.Background(), session); err != nil {
		t.Fatalf("collect: %v", err)
	}

	if len(session.Status.Artifacts) != 2 ||
		!hasArtifactNamed(session.Status.Artifacts, artifactNameAgentLogs) ||
		!hasArtifactNamed(session.Status.Artifacts, artifactNameWorkspaceBundle) {
		t.Fatalf("artifacts = %+v", session.Status.Artifacts)
	}

	var cm corev1.ConfigMap
	if err := cl.Get(context.Background(), logsKey(), &cm); err != nil {
		t.Fatalf("get log ConfigMap: %v", err)
	}
	if cm.Data[artifactLogConfigMapKey] != "hello logs" {
		t.Fatalf("log data = %q", cm.Data[artifactLogConfigMapKey])
	}
	var sec corev1.Secret
	if err := cl.Get(context.Background(), artifactsKey(), &sec); err != nil {
		t.Fatalf("get artifact Secret: %v", err)
	}
	if string(sec.Data[artifactArchiveSecretKey]) != "tarbytes" {
		t.Fatalf("artifact data = %q", sec.Data[artifactArchiveSecretKey])
	}
	if !containsReason(drainReasons(rec), EventReasonOutputsCollected) {
		t.Fatal("expected an OutputsCollected event")
	}

	// A second reconcile of the same (now-populated) session must re-collect nothing.
	if err := r.collectSessionOutputs(context.Background(), session); err != nil {
		t.Fatalf("second collect: %v", err)
	}
	if logCalls != 1 || execCalls != 1 {
		t.Fatalf("collectors re-invoked on second reconcile: logCalls=%d execCalls=%d", logCalls, execCalls)
	}
	if len(session.Status.Artifacts) != 2 {
		t.Fatalf("artifacts changed on second reconcile: %+v", session.Status.Artifacts)
	}
}

func TestCollectAgentLogs_truncatesAtCapAndSkipsEmpty(t *testing.T) {
	t.Run("truncates at cap", func(t *testing.T) {
		r, _, cl := newOutputsTestReconciler(t)
		r.collectLogsFn = func(_ context.Context, _ kubernetes.Interface, _, _, _ string, _ int64) ([]byte, error) {
			return bytes.Repeat([]byte("x"), maxCollectedLogBytes+4096), nil
		}
		session := terminalCollectingSession()
		session.Spec.Outputs.CollectArtifacts = false

		if err := r.collectSessionOutputs(context.Background(), session); err != nil {
			t.Fatalf("collect: %v", err)
		}
		var cm corev1.ConfigMap
		if err := cl.Get(context.Background(), logsKey(), &cm); err != nil {
			t.Fatalf("get ConfigMap: %v", err)
		}
		if got := len(cm.Data[artifactLogConfigMapKey]); got != maxCollectedLogBytes {
			t.Fatalf("stored log length = %d, want %d (cap)", got, maxCollectedLogBytes)
		}
	})

	t.Run("skips empty output", func(t *testing.T) {
		r, _, cl := newOutputsTestReconciler(t)
		r.collectLogsFn = func(_ context.Context, _ kubernetes.Interface, _, _, _ string, _ int64) ([]byte, error) {
			return nil, nil
		}
		session := terminalCollectingSession()
		session.Spec.Outputs.CollectArtifacts = false

		if err := r.collectSessionOutputs(context.Background(), session); err != nil {
			t.Fatalf("collect: %v", err)
		}
		if len(session.Status.Artifacts) != 0 {
			t.Fatalf("expected no artifacts for empty logs, got %+v", session.Status.Artifacts)
		}
		var cm corev1.ConfigMap
		if err := cl.Get(context.Background(), logsKey(), &cm); !apierrors.IsNotFound(err) {
			t.Fatalf("expected no ConfigMap, got err=%v", err)
		}
	})
}

func TestCollectWorkspaceArtifacts_truncatesAtCapAndSkipsEmpty(t *testing.T) {
	t.Run("truncates at cap", func(t *testing.T) {
		r, _, cl := newOutputsTestReconciler(t)
		r.execFn = func(_ context.Context, _ kubernetes.Interface, _, _ string, _ []string, stdout, _ io.Writer) error {
			_, err := stdout.Write(bytes.Repeat([]byte("z"), maxCollectedArtifactBytes+4096))
			return err
		}
		session := terminalCollectingSession()
		session.Spec.Outputs.CollectLogs = false

		if err := r.collectSessionOutputs(context.Background(), session); err != nil {
			t.Fatalf("collect: %v", err)
		}
		var sec corev1.Secret
		if err := cl.Get(context.Background(), artifactsKey(), &sec); err != nil {
			t.Fatalf("get Secret: %v", err)
		}
		if got := len(sec.Data[artifactArchiveSecretKey]); got != maxCollectedArtifactBytes {
			t.Fatalf("stored artifact length = %d, want %d (cap)", got, maxCollectedArtifactBytes)
		}
	})

	t.Run("skips empty output", func(t *testing.T) {
		r, _, cl := newOutputsTestReconciler(t)
		r.execFn = func(_ context.Context, _ kubernetes.Interface, _, _ string, _ []string, _, _ io.Writer) error {
			return nil // no bytes written to stdout
		}
		session := terminalCollectingSession()
		session.Spec.Outputs.CollectLogs = false

		if err := r.collectSessionOutputs(context.Background(), session); err != nil {
			t.Fatalf("collect: %v", err)
		}
		if len(session.Status.Artifacts) != 0 {
			t.Fatalf("expected no artifacts for empty bundle, got %+v", session.Status.Artifacts)
		}
		var sec corev1.Secret
		if err := cl.Get(context.Background(), artifactsKey(), &sec); !apierrors.IsNotFound(err) {
			t.Fatalf("expected no Secret, got err=%v", err)
		}
	})
}

func TestCollectOutputsBestEffort_degradesFailureToWarning(t *testing.T) {
	r, rec, cl := newOutputsTestReconciler(t)
	r.collectLogsFn = func(_ context.Context, _ kubernetes.Interface, _, _, _ string, _ int64) ([]byte, error) {
		return []byte("logs"), nil
	}
	r.execFn = func(_ context.Context, _ kubernetes.Interface, _, _ string, _ []string, _, _ io.Writer) error {
		return fmt.Errorf("exec tar boom")
	}
	session := terminalCollectingSession()

	// Must not panic and must not block; the failure is degraded to a warning event.
	r.collectOutputsBestEffort(context.Background(), session)

	if !containsReason(drainReasons(rec), EventReasonOutputsCollectionFailed) {
		t.Fatal("expected an OutputsCollectionFailed warning event")
	}
	// The log ConfigMap collected before the exec failure still exists (partial success),
	// but no refs were promoted to status (the batch failed atomically).
	var cm corev1.ConfigMap
	if err := cl.Get(context.Background(), logsKey(), &cm); err != nil {
		t.Fatalf("expected the log ConfigMap from the successful first collector: %v", err)
	}
	if len(session.Status.Artifacts) != 0 {
		t.Fatalf("no refs should be promoted on a failed batch, got %+v", session.Status.Artifacts)
	}
}

func TestCollectSessionOutputs_guardsSkipCollection(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*scrutineerv1alpha1.AgentSession)
	}{
		{"non-terminal phase", func(s *scrutineerv1alpha1.AgentSession) { s.Status.Phase = scrutineerv1alpha1.PhaseRunning }},
		{"no collection requested", func(s *scrutineerv1alpha1.AgentSession) { s.Spec.Outputs = scrutineerv1alpha1.OutputSpec{} }},
		{"no pod name", func(s *scrutineerv1alpha1.AgentSession) { s.Status.PodName = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _, _ := newOutputsTestReconciler(t)
			called := false
			r.collectLogsFn = func(_ context.Context, _ kubernetes.Interface, _, _, _ string, _ int64) ([]byte, error) {
				called = true
				return []byte("x"), nil
			}
			r.execFn = func(_ context.Context, _ kubernetes.Interface, _, _ string, _ []string, stdout, _ io.Writer) error {
				called = true
				_, err := stdout.Write([]byte("x"))
				return err
			}
			session := terminalCollectingSession()
			tc.mutate(session)

			if err := r.collectSessionOutputs(context.Background(), session); err != nil {
				t.Fatalf("collect: %v", err)
			}
			if called {
				t.Fatal("collectors must not run when collection is guarded off")
			}
			if len(session.Status.Artifacts) != 0 {
				t.Fatalf("no artifacts expected, got %+v", session.Status.Artifacts)
			}
		})
	}
}
