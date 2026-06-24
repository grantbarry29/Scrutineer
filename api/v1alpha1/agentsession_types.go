/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentSessionPhase represents the lifecycle phase of an AgentSession.
// +kubebuilder:validation:Enum=Pending;Validating;AwaitingApproval;Starting;Running;Succeeded;Failed;Denied;TimedOut;Cancelled
type AgentSessionPhase string

const (
	// PhasePending indicates the AgentSession has been created but not yet processed.
	PhasePending AgentSessionPhase = "Pending"
	// PhaseValidating indicates the AgentSession spec is currently being validated.
	PhaseValidating AgentSessionPhase = "Validating"
	// PhaseAwaitingApproval indicates the session is blocked on a human approval
	// gate (a matching ApprovalPolicy applies) and no runtime has been created.
	PhaseAwaitingApproval AgentSessionPhase = "AwaitingApproval"
	// PhaseStarting indicates the underlying runtime (e.g. a Job) is being created.
	PhaseStarting AgentSessionPhase = "Starting"
	// PhaseRunning indicates the agent runtime is actively executing.
	PhaseRunning AgentSessionPhase = "Running"
	// PhaseSucceeded indicates the agent runtime completed successfully.
	PhaseSucceeded AgentSessionPhase = "Succeeded"
	// PhaseFailed indicates the agent runtime exited with a failure.
	PhaseFailed AgentSessionPhase = "Failed"
	// PhaseDenied indicates the AgentSession was rejected by policy/validation.
	PhaseDenied AgentSessionPhase = "Denied"
	// PhaseTimedOut indicates the agent runtime exceeded its allowed deadline.
	PhaseTimedOut AgentSessionPhase = "TimedOut"
	// PhaseCancelled indicates the agent runtime was cancelled by a user.
	PhaseCancelled AgentSessionPhase = "Cancelled"
)

// SessionTaskSpec describes the agent task to perform.
type SessionTaskSpec struct {
	// Description is a short human-readable description of the task.
	// +optional
	Description string `json:"description,omitempty"`

	// Prompt is the natural-language instruction sent to the agent.
	// Either Description or Prompt must be non-empty (PromptConfigMapRef can substitute Prompt).
	// +optional
	Prompt string `json:"prompt,omitempty"`

	// PromptConfigMapRef optionally sources the prompt from a ConfigMap key.
	// +optional
	PromptConfigMapRef *PromptConfigMapRef `json:"promptConfigMapRef,omitempty"`
}

// PromptConfigMapRef references a ConfigMap value containing the agent prompt.
type PromptConfigMapRef struct {
	// Name of the ConfigMap in the AgentSession namespace (same-namespace refs only in MVP).
	Name string `json:"name"`
	// Key inside the ConfigMap that contains the prompt text.
	Key string `json:"key"`
}

// ModelSpec describes which model/provider the agent should use.
type ModelSpec struct {
	// Provider is the model provider, e.g. "openai", "anthropic", "bedrock".
	// +kubebuilder:validation:MinLength=1
	Provider string `json:"provider"`
	// Name is the model identifier, e.g. "gpt-4.1".
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Temperature controls sampling temperature for the model. It is the
	// numeric value [0.0, 2.0] encoded as a string (e.g. "0.2", "1.5").
	//
	// We use a string rather than a Go float64 to avoid cross-language
	// round-tripping issues with JSON/YAML floats — the same approach used
	// by k8s.io/apimachinery's resource.Quantity. The Pattern marker below
	// enforces [0, 2] at admission time; the controller additionally parses
	// and range-checks the value as defense-in-depth.
	// +kubebuilder:validation:Pattern=`^(0(\.[0-9]+)?|1(\.[0-9]+)?|2(\.0+)?)$`
	// +optional
	Temperature *string `json:"temperature,omitempty"`
	// MaxTokens limits output tokens for the model call.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxTokens *int32 `json:"maxTokens,omitempty"`

	// BaseURL optionally overrides the provider API endpoint. Use it for
	// OpenAI-compatible aggregators or gateways (e.g. OpenRouter
	// "https://openrouter.ai/api/v1", LiteLLM, vLLM, Together, Azure). It is
	// propagated to the agent as AGENT_MODEL_BASE_URL. Relay never calls the
	// model itself, so the value is opaque and provider-agnostic; it only tells
	// the agent runtime where to point its client. Must be an http(s) URL.
	// +kubebuilder:validation:Pattern=`^https?://.+`
	// +optional
	BaseURL string `json:"baseURL,omitempty"`
}

// RuntimeSpec describes how the agent should be executed.
type RuntimeSpec struct {
	// Orchestrator selects which runtime backend should execute the session.
	// MVP only supports "kubernetes-job".
	// Future values: "tekton", "argo", "temporal", "external".
	// +kubebuilder:validation:Enum=kubernetes-job
	// +kubebuilder:default=kubernetes-job
	// +optional
	Orchestrator string `json:"orchestrator,omitempty"`

	// Image is the container image to run the agent.
	Image string `json:"image"`

	// Command overrides the container ENTRYPOINT.
	// +optional
	Command []string `json:"command,omitempty"`

	// Args overrides the container CMD.
	// +optional
	Args []string `json:"args,omitempty"`

	// ImagePullPolicy is the container image pull policy.
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// ServiceAccountName is the ServiceAccount the agent pod runs as.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// TimeoutSeconds bounds the maximum wall-clock duration of the session.
	// +kubebuilder:validation:Minimum=1
	// +optional
	TimeoutSeconds *int64 `json:"timeoutSeconds,omitempty"`

	// Env are extra environment variables to inject into the agent container,
	// in addition to the Relay-managed RELAY_*/AGENT_* variables.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Resources sets the container resource requests and limits.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// NodeSelector constrains the pod to nodes with matching labels.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations allow the pod to schedule onto nodes with matching taints.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
}

// InlinePolicySpec captures session-specific governance overrides.
// Referenced AgentPolicy objects are merged first; inline fields override on conflict.
type InlinePolicySpec struct {
	PolicyRules `json:",inline"`
}

// WorkspaceSpec describes the agent workspace volume.
type WorkspaceSpec struct {
	// Ephemeral, when true, provisions an emptyDir workspace that is discarded after the session.
	// +optional
	Ephemeral bool `json:"ephemeral,omitempty"`

	// Size is the requested workspace size (e.g. "5Gi"). Used by future PVC-backed workspaces.
	// +optional
	Size string `json:"size,omitempty"`

	// MountPath is the in-container mount point. Defaults to /workspace.
	// +optional
	MountPath string `json:"mountPath,omitempty"`
}

// OutputSpec controls what artifacts/logs Relay collects from a session.
type OutputSpec struct {
	// CollectLogs, when true, indicates that pod logs should be retained.
	// +optional
	CollectLogs bool `json:"collectLogs,omitempty"`

	// CollectArtifacts, when true, indicates that files under ArtifactPath should be retained.
	// +optional
	CollectArtifacts bool `json:"collectArtifacts,omitempty"`

	// ArtifactPath is the directory inside the workspace from which artifacts are collected.
	// +optional
	ArtifactPath string `json:"artifactPath,omitempty"`
}

// AgentSessionSpec defines the desired state of an AgentSession.
type AgentSessionSpec struct {
	// Task describes what the agent should do.
	Task SessionTaskSpec `json:"task"`

	// Model describes which LLM provider/model the agent should use.
	Model ModelSpec `json:"model"`

	// Runtime describes how the agent should be executed.
	Runtime RuntimeSpec `json:"runtime"`

	// PolicyRefs lists reusable policies to merge before spec.policy overrides.
	// Refs are resolved in order within the same namespace as the AgentSession.
	// Recommended order: AgentPolicy entries, then ToolPolicy, then spec.policy inline overrides.
	// +optional
	PolicyRefs []PolicyRef `json:"policyRefs,omitempty"`

	// RuntimeProfileRef optionally references a RuntimeProfile in the same namespace.
	// The profile is applied to the session Job pod template once wired in the reconciler.
	// +optional
	RuntimeProfileRef *RuntimeProfileRef `json:"runtimeProfileRef,omitempty"`

	// Policy describes inline governance overrides for this session.
	// +optional
	Policy InlinePolicySpec `json:"policy,omitempty"`

	// Workspace describes the workspace volume mounted into the agent container.
	// +optional
	Workspace WorkspaceSpec `json:"workspace,omitempty"`

	// Outputs controls log/artifact collection for the session.
	// +optional
	Outputs OutputSpec `json:"outputs,omitempty"`

	// CancelRequested asks Relay to stop this session. When true, the controller
	// (in a later reconciliation step) deletes the owned runtime Job and moves
	// the session to Phase=Cancelled. Setting this field is the MVP cancellation
	// request mechanism; it does not by itself stop the Job until the controller
	// handles it.
	// +optional
	// +kubebuilder:default=false
	CancelRequested bool `json:"cancelRequested,omitempty"`
}

// SessionResult captures the terminal outcome of an AgentSession.
type SessionResult struct {
	// Outcome is a short symbolic outcome, e.g. "completed", "failed", "denied".
	// +optional
	Outcome string `json:"outcome,omitempty"`
	// Summary is a human-readable summary of the result.
	// +optional
	Summary string `json:"summary,omitempty"`
	// ExitCode is the exit code of the primary agent container, when available.
	// +optional
	ExitCode int32 `json:"exitCode,omitempty"`
	// Message is a free-form message about the result.
	// +optional
	Message string `json:"message,omitempty"`
}

// SessionUsage captures resource/usage metrics for an AgentSession.
// Populated from runtime reports: network/tool decisions increment counters; optional usage deltas carry token totals.
type SessionUsage struct {
	// InputTokens is the total number of input tokens consumed.
	// +optional
	InputTokens int64 `json:"inputTokens,omitempty"`
	// OutputTokens is the total number of output tokens produced.
	// +optional
	OutputTokens int64 `json:"outputTokens,omitempty"`
	// ToolCalls is the total number of tool invocations made.
	// +optional
	ToolCalls int64 `json:"toolCalls,omitempty"`
	// NetworkRequests is the total number of network requests made.
	// +optional
	NetworkRequests int64 `json:"networkRequests,omitempty"`
	// FileOperations is the total number of file access operations observed at runtime.
	// +optional
	FileOperations int64 `json:"fileOperations,omitempty"`
}

// SessionEventType categorizes structured timeline events for observability surfaces.
//
// +kubebuilder:validation:Enum=policy;network;tool;lifecycle;system
type SessionEventType string

const (
	SessionEventTypePolicy    SessionEventType = "policy"
	SessionEventTypeNetwork   SessionEventType = "network"
	SessionEventTypeTool      SessionEventType = "tool"
	SessionEventTypeLifecycle SessionEventType = "lifecycle"
	SessionEventTypeSystem    SessionEventType = "system"
)

// SessionEvent is a durable, timestamped runtime observation for UI timelines and audit.
// Phase 3b reporters append via POST /v1/report; the reconciler preserves existing entries.
type SessionEvent struct {
	// Time when the event was observed at runtime.
	Time metav1.Time `json:"time"`

	// Type categorizes the event for filtering (policy, network, tool, lifecycle, system).
	Type SessionEventType `json:"type"`

	// Source is the reporting component (e.g. egress-proxy, tool-gateway).
	// +optional
	Source string `json:"source,omitempty"`

	// Action is a short verb: allow, deny, call, block, start, complete.
	// +optional
	Action string `json:"action,omitempty"`

	// Target is the entity involved (domain, tool name, path).
	// +optional
	Target string `json:"target,omitempty"`

	// Message is human-readable detail for operators.
	// +optional
	Message string `json:"message,omitempty"`

	// EventID is an optional client idempotency key unique within the session.
	// +optional
	EventID string `json:"eventId,omitempty"`
}

// PolicyViolation records a single policy violation observed during a session.
// Phase 3 enforcement reporters populate this via runtime reports (deny and dry-run outcomes).
type PolicyViolation struct {
	// Time when the violation was observed.
	Time metav1.Time `json:"time"`
	// Type categorizes the violation, e.g. "network", "tool", "approval".
	Type string `json:"type"`
	// Message is a human-readable description of the violation.
	Message string `json:"message"`
	// Target is the entity the policy was applied against (domain, tool name, etc.).
	// +optional
	Target string `json:"target,omitempty"`

	// AssuranceLevel indicates how trustworthy this violation record is.
	// Violations reported by cooperative sidecars are stamped "self-reported"
	// by the controller; an empty value should be treated as "self-reported".
	// Independently-observed sources (e.g. kernel/eBPF) would be "observed".
	// +optional
	AssuranceLevel EvidenceAssurance `json:"assuranceLevel,omitempty"`
}

// RuntimeApprovalSummary is a redaction-safe view of one outstanding
// mid-execution per-tool approval hold (ApprovalRequest spec.trigger=runtime)
// gating this session. It answers the operational "what needs a human now?"
// question for UI/observability surfaces WITHOUT leaking tool-call arguments —
// only a digest is exposed. It is controller-owned, recomputed each reconcile,
// and entries drop off once the hold is decided (granted/denied/expired).
type RuntimeApprovalSummary struct {
	// Name is the ApprovalRequest object gating the held tool call.
	Name string `json:"name"`

	// RequestID correlates this hold with the data-plane call that raised it
	// (the reporter's idempotency key). Empty if the gateway did not supply one.
	// +optional
	RequestID string `json:"requestId,omitempty"`

	// Action is the gated action type (e.g. "deploy").
	// +optional
	Action string `json:"action,omitempty"`

	// Target is the tool/entity being approved (scoped target, else action).
	// +optional
	Target string `json:"target,omitempty"`

	// ArgDigest is a redacted fingerprint (e.g. sha256) of the held call's
	// arguments. It NEVER carries raw argument values — only a digest — so this
	// surface stays redaction-safe.
	// +optional
	ArgDigest string `json:"argDigest,omitempty"`

	// State is the controller-observed lifecycle state (Pending while held).
	// +optional
	State ApprovalState `json:"state,omitempty"`

	// PolicyRef is the ApprovalPolicy gating the hold, if any (empty for
	// policy-less holds gated solely by RBAC on the ApprovalRequest).
	// +optional
	PolicyRef string `json:"policyRef,omitempty"`

	// RequestedAt is when the hold's ApprovalRequest was created.
	// +optional
	RequestedAt *metav1.Time `json:"requestedAt,omitempty"`

	// Reason is a short human-readable explanation of the current state.
	// +optional
	Reason string `json:"reason,omitempty"`
}

// ArtifactRef references an artifact produced by an AgentSession.
// Populated when spec.outputs requests log/artifact collection on terminal phases.
type ArtifactRef struct {
	// Name is a human-readable name of the artifact.
	Name string `json:"name"`
	// URI locates the artifact (e.g. "s3://...", "file:///workspace/out.txt").
	URI string `json:"uri"`
	// MediaType is the artifact's MIME type.
	// +optional
	MediaType string `json:"mediaType,omitempty"`
}

// AgentSessionStatus defines the observed state of an AgentSession.
type AgentSessionStatus struct {
	// Phase is the current high-level phase of the session.
	// +optional
	Phase AgentSessionPhase `json:"phase,omitempty"`

	// ObservedGeneration is the last generation reconciled by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// StartTime is when the session moved out of Pending.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// CompletionTime is when the session reached a terminal phase.
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// Conditions represent the latest available observations of the session's state.
	//
	// Condition types (value stored in metav1.Condition.Type):
	// - Validated: spec/task/policy validation result
	// - PolicyResolved: referenced policies merged into an effective policy (control-plane)
	// - PolicyPropagated: effective policy propagated to the runtime Job env/template
	// - RuntimeProfileResolved: runtime profile referenced by spec.runtimeProfileRef is applied
	// - RuntimeCreated: underlying runtime Job created/exists
	// - Completed: terminal state mapping from Job succeeded/failed/timeout
	// - Ready: aggregate readiness — True when phase is Running or Succeeded; False otherwise
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// JobName is the name of the underlying batch/v1 Job, when one has been created.
	// +optional
	JobName string `json:"jobName,omitempty"`

	// PodName is the name of the agent Pod for the current Job, when known.
	// The controller lists Pods labeled for this session, keeps only those owned by the
	// current Job (ownerReference UID), and records the newest by CreationTimestamp
	// (name breaks ties). Pods from a replaced Job or without the session label are ignored.
	// +optional
	PodName string `json:"podName,omitempty"`

	// MatchedPolicies lists policy CRDs that contributed to status.effectivePolicy.
	// +optional
	MatchedPolicies []MatchedPolicyRef `json:"matchedPolicies,omitempty"`

	// EffectivePolicy is the merged policy propagated to the runtime (env vars today).
	// +optional
	EffectivePolicy *EffectivePolicyStatus `json:"effectivePolicy,omitempty"`

	// PolicyDecisions records merge-time and runtime policy evaluations (bounded list).
	// Merge-time entries are replaced each reconcile; runtime entries are Phase 3+.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=64
	PolicyDecisions []PolicyDecision `json:"policyDecisions,omitempty"`

	// MatchedRuntimeProfile records the RuntimeProfile applied to the session Job template.
	// +optional
	MatchedRuntimeProfile *MatchedRuntimeProfileRef `json:"matchedRuntimeProfile,omitempty"`

	// Result captures the terminal outcome of the session.
	// +optional
	Result *SessionResult `json:"result,omitempty"`

	// Usage captures resource/usage metrics for the session.
	// Not populated in the MVP; reserved for Phase 4 observability backends.
	// +optional
	Usage *SessionUsage `json:"usage,omitempty"`

	// Violations records policy violations observed during this session.
	// Populated by Phase 3 enforcement reporters (deny and dry-run outcomes).
	// +optional
	Violations []PolicyViolation `json:"violations,omitempty"`

	// Events is an ordered, bounded stream of structured runtime observations (timeline/audit).
	// Appended by data-plane reporters; preserved across reconciler status patches.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=256
	Events []SessionEvent `json:"events,omitempty"`

	// Artifacts references artifacts collected from this session when spec.outputs requests retention.
	// +optional
	Artifacts []ArtifactRef `json:"artifacts,omitempty"`

	// PendingApprovals lists outstanding mid-execution per-tool approval holds
	// (ApprovalRequest spec.trigger=runtime) awaiting a human decision for this
	// session. It surfaces the operational "what needs approval now?" question for
	// UI/observability and is redaction-safe (argDigest only, never raw args).
	// Controller-owned: recomputed each reconcile and cleared as holds are decided
	// or the session reaches a terminal phase. Bounded to keep status small.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=64
	PendingApprovals []RuntimeApprovalSummary `json:"pendingApprovals,omitempty"`
}

// AgentSession is the Schema for the agentsessions API.
//
// An AgentSession represents one governed autonomous AI agent execution.
// It is intentionally not a generic workflow task; the spec captures task intent,
// model selection, runtime placement, and governance policy.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=as;agentsess
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Job",type=string,JSONPath=`.status.jobName`
// +kubebuilder:printcolumn:name="Pod",type=string,JSONPath=`.status.podName`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type AgentSession struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentSessionSpec   `json:"spec,omitempty"`
	Status AgentSessionStatus `json:"status,omitempty"`
}

// AgentSessionList contains a list of AgentSession.
//
// +kubebuilder:object:root=true
type AgentSessionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentSession `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentSession{}, &AgentSessionList{})
}
