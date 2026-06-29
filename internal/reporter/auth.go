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
	"fmt"
	"net/http"
	"strings"

	authenticationv1 "k8s.io/api/authentication/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerjob "github.com/grantbarry29/scrutineer/internal/controller/job"
)

const (
	headerScrutineerPod  = "X-Scrutineer-Pod"
	podNameExtraKey      = "authentication.kubernetes.io/pod-name"
	serviceAccountPrefix = "system:serviceaccount:"
)

// IdentityVerifier authenticates a request and confirms the caller pod owns the session.
type IdentityVerifier interface {
	Verify(ctx context.Context, r *http.Request, session SessionRef) (CallerIdentity, error)
}

// KubeIdentityVerifier validates bearer tokens via TokenReview and checks pod→Job→session ownership.
type KubeIdentityVerifier struct {
	Client client.Client
	// Reader is the uncached reader for the per-request pod/Job ownership lookups.
	// Uncached by design (see the read-consistency policy on reporter.Options, #47):
	// it keeps the standalone reporter's get-only RBAC and avoids an informer cache
	// over all pods/Jobs in the namespace.
	Reader   client.Reader
	Audience string
}

// Verify implements IdentityVerifier.
func (v *KubeIdentityVerifier) Verify(ctx context.Context, r *http.Request, session SessionRef) (CallerIdentity, error) {
	token, err := bearerToken(r.Header.Get("Authorization"))
	if err != nil {
		return CallerIdentity{}, err
	}

	audience := v.Audience
	if audience == "" {
		audience = TokenAudience
	}

	review := &authenticationv1.TokenReview{
		Spec: authenticationv1.TokenReviewSpec{
			Token:     token,
			Audiences: []string{audience},
		},
	}
	if err := v.Client.Create(ctx, review); err != nil {
		return CallerIdentity{}, fmt.Errorf("token review: %w", err)
	}
	if !review.Status.Authenticated {
		msg := review.Status.Error
		if msg == "" {
			msg = "token not authenticated"
		}
		return CallerIdentity{}, fmt.Errorf("%w: %s", ErrUnauthorized, msg)
	}

	namespace := session.Namespace
	podName, ok := podNameFromTokenReview(review.Status)
	if !ok {
		podName = strings.TrimSpace(r.Header.Get(headerScrutineerPod))
		if podName == "" {
			return CallerIdentity{}, fmt.Errorf("%w: pod identity not found in token or %s header", ErrUnauthorized, headerScrutineerPod)
		}
	}

	if err := v.authorizePodForSession(ctx, namespace, podName, session.Name, review.Status.User.Username); err != nil {
		return CallerIdentity{}, err
	}

	return CallerIdentity{Namespace: namespace, PodName: podName}, nil
}

func bearerToken(header string) (string, error) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", fmt.Errorf("%w: missing bearer token", ErrUnauthorized)
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if token == "" {
		return "", fmt.Errorf("%w: empty bearer token", ErrUnauthorized)
	}
	return token, nil
}

func podNameFromTokenReview(status authenticationv1.TokenReviewStatus) (string, bool) {
	if status.User.Extra == nil {
		return "", false
	}
	names := status.User.Extra[podNameExtraKey]
	if len(names) == 0 || names[0] == "" {
		return "", false
	}
	return names[0], true
}

func (v *KubeIdentityVerifier) authorizePodForSession(ctx context.Context, namespace, podName, sessionName, tokenUsername string) error {
	var pod corev1.Pod
	if err := v.Reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: podName}, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("%w: pod %q not found", ErrForbidden, podName)
		}
		return fmt.Errorf("get pod: %w", err)
	}

	if pod.Namespace != namespace {
		return fmt.Errorf("%w: pod namespace mismatch", ErrForbidden)
	}

	expectedSA := serviceAccountPrefix + namespace + ":" + pod.Spec.ServiceAccountName
	if tokenUsername != "" && tokenUsername != expectedSA {
		return fmt.Errorf("%w: token service account does not match pod", ErrForbidden)
	}

	var jobName string
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "Job" && (ref.APIVersion == batchv1.SchemeGroupVersion.String() || ref.APIVersion == "batch/v1") {
			jobName = ref.Name
			break
		}
	}
	if jobName == "" {
		return fmt.Errorf("%w: pod is not owned by a Job", ErrForbidden)
	}

	var job batchv1.Job
	if err := v.Reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: jobName}, &job); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("%w: owning Job not found", ErrForbidden)
		}
		return fmt.Errorf("get Job: %w", err)
	}

	if job.Labels[scrutineerjob.LabelSessionRef] != sessionName {
		return fmt.Errorf("%w: pod Job does not own session %q", ErrForbidden, sessionName)
	}

	return nil
}
