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

	"github.com/grantbarry29/scrutineer/internal/enforcement/sidecarenv"
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

	// accessLogVolume is the shared emptyDir where Envoy writes the JSON access log and
	// the egress-reporter container tails it (Slice C, #62). Size-bounded: overflow
	// evicts the pod, which fails closed — the routing lock leaves the agent without
	// egress rather than egressing without evidence.
	accessLogVolume    = "access-log"
	accessLogSizeLimit = "256Mi"

	// reporterContainerName is the egress-reporter container beside Envoy.
	reporterContainerName = "egress-reporter"
	// reporterTokenVolume mounts a projected per-session SA token (reporter audience)
	// into the egress-reporter container only — the identity the reporter authorizes
	// before stamping evidence observed.
	reporterTokenVolume        = "scrutineer-reporter-token"
	reporterTokenMountPath     = "/var/run/secrets/scrutineer/reporter-token"
	reporterTokenFileName      = "token"
	reporterTokenExpirationSec = int64(600)

	// DefaultEgressReporterImage is the first-party egress-reporter image
	// (cmd/egress-reporter, built by Dockerfile.egress-reporter).
	DefaultEgressReporterImage = "ghcr.io/grantbarry29/scrutineer-egress-reporter:latest"

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

// ConfigMap holds the Envoy bootstrap for the session's proxy. cfg carries the session's
// effective FQDN policy (#32); pass a zero-value BootstrapConfig for a pure forward proxy.
// The port is always ProxyPort — the caller need not set cfg.Port.
func ConfigMap(sessionName, namespace string, cfg BootstrapConfig) *corev1.ConfigMap {
	cfg.Port = ProxyPort
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:        ResourceName(sessionName),
			Namespace:   namespace,
			Labels:      Labels(sessionName),
			Annotations: map[string]string{ConfigHashAnnotation: cfg.Hash()},
		},
		Data: map[string]string{configFileName: BootstrapYAML(cfg)},
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

// PodConfig parameterizes the egress-proxy Pod. Reporter fields come from the caller
// (the controller passes job.DefaultReporterURL / job.ReporterTokenAudience) because the
// job package imports this one — envoy cannot import those constants back.
type PodConfig struct {
	// ServiceAccount is the dedicated per-session identity (ServiceAccount builder).
	ServiceAccount string
	// Image is the Envoy proxy image.
	Image string
	// ReporterImage is the egress-reporter image (DefaultEgressReporterImage). Empty
	// omits the evidence container and token volume — a pure proxy pod, used by tests
	// and staged rollouts only.
	ReporterImage string
	// ReporterURL is the in-cluster reporter base URL.
	ReporterURL string
	// ReporterAudience is the projected-token audience the reporter authenticates.
	ReporterAudience string
	// Bootstrap is the session's egress config (FQDN policy + mode). The same value
	// drives the Envoy RBAC (ConfigMap) and the egress-reporter's evidence
	// classification env, so enforcement and evidence agree (#32).
	Bootstrap BootstrapConfig
}

// hardenedSecurityContext is the shared container hardening for every container in the
// egress-proxy pod: drop ALL, no privilege escalation, read-only rootfs, non-root.
func hardenedSecurityContext() *corev1.SecurityContext {
	no := false
	nonRoot := true
	readOnly := true
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: &no,
		ReadOnlyRootFilesystem:   &readOnly,
		RunAsNonRoot:             &nonRoot,
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
}

// Pod is the session's egress proxy: a separate, unprivileged pod (own identity/netns)
// running Envoy plus — when cfg.ReporterImage is set — the egress-reporter container that
// tails Envoy's JSON access log from the shared volume and submits observed egress
// evidence with the pod's projected per-session SA token (Slice C, #62).
// automountServiceAccountToken stays off: the token is an explicit, audience-scoped
// projected volume mounted only into the egress-reporter container.
func Pod(sessionName, namespace string, cfg PodConfig) *corev1.Pod {
	no := false
	nonRoot := true
	uid := runAsUser

	envoyContainer := corev1.Container{
		Name:            containerName,
		Image:           cfg.Image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Args:            []string{"-c", configMountPath + "/" + configFileName},
		Ports: []corev1.ContainerPort{{
			Name:          "proxy",
			ContainerPort: ProxyPort,
			Protocol:      corev1.ProtocolTCP,
		}},
		SecurityContext: hardenedSecurityContext(),
		VolumeMounts: []corev1.VolumeMount{
			{Name: configVolume, MountPath: configMountPath, ReadOnly: true},
			{Name: tmpVolume, MountPath: tmpMountPath},
			// Writable: Envoy appends the JSON access log here (AccessLogPath).
			{Name: accessLogVolume, MountPath: AccessLogDir},
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
	}

	logSize := resource.MustParse(accessLogSizeLimit)
	volumes := []corev1.Volume{
		{
			Name: configVolume,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: ResourceName(sessionName)},
				},
			},
		},
		{Name: tmpVolume, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: accessLogVolume, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: &logSize}}},
	}

	containers := []corev1.Container{envoyContainer}
	if cfg.ReporterImage != "" {
		containers = append(containers, egressReporterContainer(sessionName, namespace, cfg))
		exp := reporterTokenExpirationSec
		volumes = append(volumes, corev1.Volume{
			Name: reporterTokenVolume,
			VolumeSource: corev1.VolumeSource{
				Projected: &corev1.ProjectedVolumeSource{
					Sources: []corev1.VolumeProjection{{
						ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
							Audience:          cfg.ReporterAudience,
							ExpirationSeconds: &exp,
							Path:              reporterTokenFileName,
						},
					}},
				},
			},
		})
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        ResourceName(sessionName),
			Namespace:   namespace,
			Labels:      Labels(sessionName),
			Annotations: map[string]string{ConfigHashAnnotation: cfg.Bootstrap.Hash()},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName:           cfg.ServiceAccount,
			AutomountServiceAccountToken: &no,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   &nonRoot,
				RunAsUser:      &uid,
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: containers,
			Volumes:    volumes,
		},
	}
}

// egressReporterContainer tails the shared access log and submits observed evidence to
// the reporter, authenticated with the pod's projected per-session SA token. It is the
// only container holding that token; Envoy itself never sees it.
func egressReporterContainer(sessionName, namespace string, cfg PodConfig) corev1.Container {
	return corev1.Container{
		Name:            reporterContainerName,
		Image:           cfg.ReporterImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Env: append([]corev1.EnvVar{
			{Name: sidecarenv.EnvSessionName, Value: sessionName},
			{Name: sidecarenv.EnvSessionNamespace, Value: namespace},
			{Name: sidecarenv.EnvReporterURL, Value: cfg.ReporterURL},
			{Name: sidecarenv.EnvReporterToken, Value: reporterTokenMountPath + "/" + reporterTokenFileName},
		}, policyEnv(cfg.Bootstrap)...),
		SecurityContext: hardenedSecurityContext(),
		VolumeMounts: []corev1.VolumeMount{
			{Name: accessLogVolume, MountPath: AccessLogDir, ReadOnly: true},
			{Name: reporterTokenVolume, MountPath: reporterTokenMountPath, ReadOnly: true},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("32Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("200m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
		},
	}
}
