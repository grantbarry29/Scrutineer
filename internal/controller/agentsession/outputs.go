/*
Copyright 2026 The Relay Authors.

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
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	relayjob "github.com/secureai/relay/internal/controller/job"
)

const (
	artifactNameAgentLogs       = "agent-logs"
	artifactNameWorkspaceBundle = "workspace-artifacts"
	artifactLogConfigMapKey     = "agent.log"
	artifactArchiveSecretKey    = "artifacts.tar.gz"

	maxCollectedLogBytes      = 512 * 1024
	maxCollectedArtifactBytes = 512 * 1024
)

// collectSessionOutputs retains pod logs and workspace files when spec.outputs requests it.
// Collection runs once per artifact name; results are stored in owned ConfigMaps/Secrets
// and referenced from status.artifacts.
func (r *AgentSessionReconciler) collectSessionOutputs(ctx context.Context, session *relayv1alpha1.AgentSession) error {
	if session == nil || !isTerminal(session.Status.Phase) {
		return nil
	}
	outs := session.Spec.Outputs
	if !outs.CollectLogs && !outs.CollectArtifacts {
		return nil
	}
	podName := strings.TrimSpace(session.Status.PodName)
	if podName == "" {
		return nil
	}

	clientset, err := r.kubernetesClient()
	if err != nil {
		return err
	}

	var collected []relayv1alpha1.ArtifactRef
	if outs.CollectLogs && !hasArtifactNamed(session.Status.Artifacts, artifactNameAgentLogs) {
		ref, err := r.collectAgentLogs(ctx, clientset, session, podName)
		if err != nil {
			return fmt.Errorf("collect logs: %w", err)
		}
		if ref != nil {
			collected = append(collected, *ref)
		}
	}
	if outs.CollectArtifacts && !hasArtifactNamed(session.Status.Artifacts, artifactNameWorkspaceBundle) {
		ref, err := r.collectWorkspaceArtifacts(ctx, clientset, session, podName)
		if err != nil {
			return fmt.Errorf("collect artifacts: %w", err)
		}
		if ref != nil {
			collected = append(collected, *ref)
		}
	}

	if len(collected) == 0 {
		return nil
	}
	session.Status.Artifacts = appendUniqueArtifacts(session.Status.Artifacts, collected)
	AppendSessionEvents(session, []relayv1alpha1.SessionEvent{{
		Time:    metav1.Now(),
		Type:    relayv1alpha1.SessionEventTypeLifecycle,
		Source:  "relay-controller",
		Action:  "outputs-collected",
		Message: fmt.Sprintf("collected %d output artifact(s)", len(collected)),
	}})
	r.recordNormal(session, EventReasonOutputsCollected, fmt.Sprintf("collected %d output artifact(s)", len(collected)))
	return nil
}

func (r *AgentSessionReconciler) collectAgentLogs(ctx context.Context, clientset kubernetes.Interface, session *relayv1alpha1.AgentSession, podName string) (*relayv1alpha1.ArtifactRef, error) {
	limit := int64(maxCollectedLogBytes)
	req := clientset.CoreV1().Pods(session.Namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container:  relayjob.AgentContainerName,
		LimitBytes: &limit,
	})
	stream, err := req.Stream(ctx)
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	body, err := io.ReadAll(io.LimitReader(stream, maxCollectedLogBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, nil
	}
	if len(body) > maxCollectedLogBytes {
		body = body[:maxCollectedLogBytes]
	}

	name := outputResourceName("relay-logs-", session.Name)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: session.Namespace,
			Labels:    outputLabels(session),
		},
		Data: map[string]string{artifactLogConfigMapKey: string(body)},
	}
	if err := controllerutil.SetControllerReference(session, cm, r.Scheme); err != nil {
		return nil, err
	}
	if err := r.createOrUpdateConfigMap(ctx, cm); err != nil {
		return nil, err
	}
	ref := artifactRef(artifactNameAgentLogs, fmt.Sprintf("configmap://%s/%s", session.Namespace, name), "text/plain")
	return &ref, nil
}

func (r *AgentSessionReconciler) collectWorkspaceArtifacts(ctx context.Context, clientset kubernetes.Interface, session *relayv1alpha1.AgentSession, podName string) (*relayv1alpha1.ArtifactRef, error) {
	path, err := resolveArtifactPath(session)
	if err != nil {
		return nil, err
	}

	var stdout, stderr bytes.Buffer
	err = r.execInAgentContainer(ctx, clientset, session.Namespace, podName, []string{
		"sh", "-c", fmt.Sprintf("if [ -d %q ]; then tar -C %q -cz .; fi", path, path),
	}, &stdout, &stderr)
	if err != nil {
		return nil, fmt.Errorf("exec tar: %w (stderr=%s)", err, strings.TrimSpace(stderr.String()))
	}
	if stdout.Len() == 0 {
		return nil, nil
	}
	data := stdout.Bytes()
	if len(data) > maxCollectedArtifactBytes {
		data = data[:maxCollectedArtifactBytes]
	}

	name := outputResourceName("relay-artifacts-", session.Name)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: session.Namespace,
			Labels:    outputLabels(session),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{artifactArchiveSecretKey: data},
	}
	if err := controllerutil.SetControllerReference(session, secret, r.Scheme); err != nil {
		return nil, err
	}
	if err := r.createOrUpdateSecret(ctx, secret); err != nil {
		return nil, err
	}
	ref := artifactRef(artifactNameWorkspaceBundle, fmt.Sprintf("secret://%s/%s", session.Namespace, name), "application/gzip")
	return &ref, nil
}

func (r *AgentSessionReconciler) execInAgentContainer(ctx context.Context, clientset kubernetes.Interface, namespace, podName string, cmd []string, stdout, stderr io.Writer) error {
	if r.RESTConfig == nil {
		return fmt.Errorf("RESTConfig not configured")
	}
	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(podName).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: relayjob.AgentContainerName,
			Command:   cmd,
			Stdout:    true,
			Stderr:    true,
		}, clientgoscheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(r.RESTConfig, "POST", req.URL())
	if err != nil {
		return err
	}
	return executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: stdout,
		Stderr: stderr,
	})
}

func (r *AgentSessionReconciler) createOrUpdateConfigMap(ctx context.Context, desired *corev1.ConfigMap) error {
	key := client.ObjectKeyFromObject(desired)
	if err := r.Create(ctx, desired); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		var live corev1.ConfigMap
		if err := r.Get(ctx, key, &live); err != nil {
			return err
		}
		live.Labels = desired.Labels
		live.Data = desired.Data
		return r.Update(ctx, &live)
	}
	return nil
}

func (r *AgentSessionReconciler) createOrUpdateSecret(ctx context.Context, desired *corev1.Secret) error {
	key := client.ObjectKeyFromObject(desired)
	if err := r.Create(ctx, desired); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		var live corev1.Secret
		if err := r.Get(ctx, key, &live); err != nil {
			return err
		}
		live.Labels = desired.Labels
		live.Data = desired.Data
		return r.Update(ctx, &live)
	}
	return nil
}

func (r *AgentSessionReconciler) kubernetesClient() (kubernetes.Interface, error) {
	if r.clientset != nil {
		return r.clientset, nil
	}
	if r.RESTConfig == nil {
		return nil, fmt.Errorf("RESTConfig not configured")
	}
	cs, err := kubernetes.NewForConfig(r.RESTConfig)
	if err != nil {
		return nil, err
	}
	r.clientset = cs
	return cs, nil
}

func resolveArtifactPath(session *relayv1alpha1.AgentSession) (string, error) {
	path := strings.TrimSpace(session.Spec.Outputs.ArtifactPath)
	if path == "" {
		mount := relayjob.DefaultWorkspaceMountPath
		if strings.TrimSpace(session.Spec.Workspace.MountPath) != "" {
			mount = filepath.Clean(session.Spec.Workspace.MountPath)
		}
		path = filepath.Join(mount, "artifacts")
	}
	path = filepath.Clean(path)
	if err := validateArtifactPath(path); err != nil {
		return "", err
	}
	return path, nil
}

func validateArtifactPath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("artifact path must be absolute")
	}
	if strings.Contains(path, "..") {
		return fmt.Errorf("artifact path must not contain ..")
	}
	if strings.ContainsAny(path, "`$\"\\\n\r") {
		return fmt.Errorf("artifact path contains invalid characters")
	}
	return nil
}

func outputResourceName(prefix, sessionName string) string {
	name := prefix + sessionName
	if len(name) <= 63 {
		return name
	}
	name = name[:63]
	return strings.TrimRight(name, "-.")
}

func outputLabels(session *relayv1alpha1.AgentSession) map[string]string {
	return map[string]string{
		relayjob.LabelAppName:      relayjob.AppNameRelay,
		relayjob.LabelAppComponent: relayjob.ComponentSession,
		relayjob.LabelSessionRef:   session.Name,
	}
}

func artifactRef(name, uri, mediaType string) relayv1alpha1.ArtifactRef {
	return relayv1alpha1.ArtifactRef{Name: name, URI: uri, MediaType: mediaType}
}

func hasArtifactNamed(artifacts []relayv1alpha1.ArtifactRef, name string) bool {
	for _, a := range artifacts {
		if a.Name == name {
			return true
		}
	}
	return false
}

func appendUniqueArtifacts(dst []relayv1alpha1.ArtifactRef, incoming []relayv1alpha1.ArtifactRef) []relayv1alpha1.ArtifactRef {
	if len(incoming) == 0 {
		return dst
	}
	seen := make(map[string]struct{}, len(dst))
	for _, a := range dst {
		seen[a.Name] = struct{}{}
	}
	for _, a := range incoming {
		if _, ok := seen[a.Name]; ok {
			continue
		}
		dst = append(dst, a)
		seen[a.Name] = struct{}{}
	}
	return dst
}

func mergeArtifactsInPlace(dst *[]relayv1alpha1.ArtifactRef, preserve []relayv1alpha1.ArtifactRef) {
	if dst == nil || len(preserve) == 0 {
		return
	}
	*dst = appendUniqueArtifacts(*dst, preserve)
}
