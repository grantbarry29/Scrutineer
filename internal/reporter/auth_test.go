/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package reporter

import (
	"context"
	"errors"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	relayjob "github.com/secureai/relay/internal/controller/job"
)

func TestKubeIdentityVerifier_authorizePodForSession(t *testing.T) {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      relayjob.NamePrefix + "sess-a",
			Namespace: "ns1",
			Labels:    map[string]string{relayjob.LabelSessionRef: "sess-a"},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-a",
			Namespace: "ns1",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: batchv1.SchemeGroupVersion.String(),
				Kind:       "Job",
				Name:       job.Name,
			}},
		},
		Spec: corev1.PodSpec{ServiceAccountName: "default"},
	}

	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(job, pod).Build()

	v := &KubeIdentityVerifier{Client: cl, Reader: cl, Audience: TokenAudience}
	if err := v.authorizePodForSession(context.Background(), "ns1", "pod-a", "sess-a", "system:serviceaccount:ns1:default"); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if err := v.authorizePodForSession(context.Background(), "ns1", "pod-a", "other-session", "system:serviceaccount:ns1:default"); err == nil {
		t.Fatal("expected forbidden for wrong session")
	} else if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestBearerToken(t *testing.T) {
	if _, err := bearerToken(""); err == nil {
		t.Fatal("expected error")
	}
	if _, err := bearerToken("Basic x"); err == nil {
		t.Fatal("expected error")
	}
	tok, err := bearerToken("Bearer abc")
	if err != nil || tok != "abc" {
		t.Fatalf("token = %q err = %v", tok, err)
	}
}
