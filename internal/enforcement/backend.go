/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package enforcement

// Backend reconciles desired enforcement state for an AgentSession runtime.
// Implementations render Kubernetes objects, sidecar configuration, or external
// gateway endpoints. Data-plane components perform actual allow/deny and report
// via RuntimeReport.
type Backend interface {
	Kind() BackendKind
	Capabilities() Capabilities
	// DesiredState returns backend-specific desired state for reconciliation.
	// Returns nil when the backend has nothing to apply for the given session context.
	DesiredState(ctx SessionContext) (any, error)
}
