/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package envoy

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// Kubernetes object builders for a session's out-of-pod Envoy egress proxy. These are
// pure (no client): the controller sets owner references and creates/reconciles them
// (Slice A wiring, #60 A3). The three objects share ResourceName so they correlate.
const (
	containerName   = "envoy"
	configVolume    = "envoy-config"
	configMountPath = "/etc/envoy"
	configFileName  = "envoy.yaml"
	tmpVolume       = "tmp"
	tmpMountPath    = "/tmp"

	labelName      = "app.kubernetes.io/name"
	labelComponent = "app.kubernetes.io/component"
	labelInstance  = "app.kubernetes.io/instance"

	componentValue = "egress-proxy"
	nameValue      = "scrutineer"

	// runAsUser is an arbitrary non-root UID; Envoy runs fine under any UID.
	runAsUser = int64(101)
)

// ResourceName is the shared name of a session's Envoy Pod/Service/ConfigMap (63-char safe).
func ResourceName(sessionName string) string { return ServiceName(sessionName) }

// Labels identify a session's egress-proxy objects; the instance label is the Service
// selector so it matches exactly this session's Envoy Pod.
func Labels(sessionName string) map[string]string {
	return map[string]string{
		labelName:      nameValue,
		labelComponent: componentValue,
		labelInstance:  ResourceName(sessionName),
	}
}

// ServiceAccount is the per-session identity the egress proxy Pod runs as. It is a
// dedicated identity (not the namespace default) so egress evidence is attributable to
// exactly this session's proxy (Slice C) and so the agent can never borrow it. In Slice A
// the Pod does not mount its token (AutomountServiceAccountToken is off).
func ServiceAccount(sessionName, namespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ResourceName(sessionName),
			Namespace: namespace,
			Labels:    Labels(sessionName),
		},
	}
}

// ConfigMap holds the Envoy bootstrap for the session's proxy.
func ConfigMap(sessionName, namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ResourceName(sessionName),
			Namespace: namespace,
			Labels:    Labels(sessionName),
		},
		Data: map[string]string{configFileName: BootstrapYAML(ProxyPort)},
	}
}

// Service exposes the session's Envoy proxy port to the agent pod.
func Service(sessionName, namespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ResourceName(sessionName),
			Namespace: namespace,
			Labels:    Labels(sessionName),
		},
		Spec: corev1.ServiceSpec{
			Selector: Labels(sessionName),
			Ports: []corev1.ServicePort{{
				Name:       "proxy",
				Port:       ProxyPort,
				TargetPort: intstr.FromInt32(ProxyPort),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
}

// Pod is the session's Envoy proxy: a separate, unprivileged pod (own identity/netns)
// mounting the bootstrap ConfigMap. automountServiceAccountToken is off — the proxy needs
// no apiserver access in Slice A (the reporter token is wired in Slice C).
func Pod(sessionName, namespace, serviceAccount, image string) *corev1.Pod {
	no := false
	nonRoot := true
	readOnly := true
	uid := runAsUser
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ResourceName(sessionName),
			Namespace: namespace,
			Labels:    Labels(sessionName),
		},
		Spec: corev1.PodSpec{
			ServiceAccountName:           serviceAccount,
			AutomountServiceAccountToken: &no,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   &nonRoot,
				RunAsUser:      &uid,
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{{
				Name:            containerName,
				Image:           image,
				ImagePullPolicy: corev1.PullIfNotPresent,
				Args:            []string{"-c", configMountPath + "/" + configFileName},
				Ports: []corev1.ContainerPort{{
					Name:          "proxy",
					ContainerPort: ProxyPort,
					Protocol:      corev1.ProtocolTCP,
				}},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: &no,
					ReadOnlyRootFilesystem:   &readOnly,
					RunAsNonRoot:             &nonRoot,
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
				VolumeMounts: []corev1.VolumeMount{
					{Name: configVolume, MountPath: configMountPath, ReadOnly: true},
					{Name: tmpVolume, MountPath: tmpMountPath},
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("50m"),
						corev1.ResourceMemory: resource.MustParse("64Mi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
				},
			}},
			Volumes: []corev1.Volume{
				{
					Name: configVolume,
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: ResourceName(sessionName)},
						},
					},
				},
				{Name: tmpVolume, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
			},
		},
	}
}
