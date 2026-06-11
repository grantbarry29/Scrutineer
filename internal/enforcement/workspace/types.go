/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package workspace implements file/workspace policy evaluation and reporting hooks.
// FS gateway sidecar injection is deferred; see docs/design/phase-3-file-workspace-policy.md.
package workspace

import "github.com/secureai/relay/internal/enforcement"

// FileRequest is metadata for a single file operation.
type FileRequest struct {
	// Path is the absolute path accessed.
	Path string
	// Operation is read, write, delete, or list (optional).
	Operation string
}

// FileAuthorization is the outcome of EvaluateFile.
type FileAuthorization struct {
	enforcement.Evaluation
	Reason string
}
