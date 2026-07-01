/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package workspace implements file/workspace policy evaluation and the first-party
// fs-gateway sidecar (`cmd/fs-gateway`, `Dockerfile.fs-gateway`).
package workspace

import "github.com/grantbarry29/scrutineer/internal/enforcement"

// SidecarType is the RuntimeProfile sidecar type for FS gateways.
const SidecarType = "fs-gateway"

// DefaultFSGatewayImage is the first-party fs-gateway container image reference.
const DefaultFSGatewayImage = "ghcr.io/grantbarry29/scrutineer-fs-gateway:latest"

// DefaultBindAddr is the bind address for the fs-gateway HTTP server.
const DefaultBindAddr = "127.0.0.1:19191"

// DefaultInPodURL is the in-pod URL agents use when an fs-gateway sidecar is injected.
const DefaultInPodURL = "http://127.0.0.1:19191"

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
