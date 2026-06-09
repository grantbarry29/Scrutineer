/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package workspace holds design stubs for future file/workspace governance backends.
//
// Phase 3 slice 8 is design-only. See docs/phase-3-file-workspace-policy.md.
// Evaluation, sidecar injection, and PolicyRules fields are intentionally deferred.
package workspace

// BackendKind identifies a future file enforcement backend.
type BackendKind string

const (
	BackendMountStrategy BackendKind = "mount-strategy"
	BackendFSGateway     BackendKind = "fs-gateway"
)

// FileRequest is metadata for a single file operation (future FS gateway).
type FileRequest struct {
	// Path is the absolute or workspace-relative path accessed.
	Path string
	// Operation is read, write, delete, or list (future).
	Operation string
}
