/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// TestCRDShortNamesAreUnique guards against a sharp edge that is otherwise a
// confusing failure: Kubernetes silently refuses to register a CRD whose short
// name collides with an already-registered CRD, so the resource never appears
// as an API resource and envtest fails with an opaque "context deadline
// exceeded" during BeforeSuite. This test reads the generated CRD manifests
// (the exact artifacts envtest installs) and fails fast with a clear message if
// any short name is reused across CRDs.
func TestCRDShortNamesAreUnique(t *testing.T) {
	dir := filepath.Join("..", "..", "config", "crd", "bases")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read CRD bases dir: %v", err)
	}

	owner := make(map[string]string) // shortName -> CRD name that first claimed it
	var crdCount int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}

		var crd struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Names struct {
					ShortNames []string `json:"shortNames"`
				} `json:"names"`
			} `json:"spec"`
		}
		if err := yaml.Unmarshal(data, &crd); err != nil {
			t.Fatalf("unmarshal %s: %v", e.Name(), err)
		}
		if crd.Metadata.Name == "" {
			continue // not a CRD manifest
		}
		crdCount++

		for _, sn := range crd.Spec.Names.ShortNames {
			if prev, ok := owner[sn]; ok {
				t.Errorf("duplicate CRD short name %q used by both %q and %q; "+
					"short names must be unique or the apiserver will not register the second CRD",
					sn, prev, crd.Metadata.Name)
				continue
			}
			owner[sn] = crd.Metadata.Name
		}
	}

	if crdCount == 0 {
		t.Fatalf("no CRD manifests found in %s (run `make manifests`?)", dir)
	}
	// Sanity: our CRDs all declare short names, so an empty set means the
	// manifest field was not parsed and the uniqueness check above was vacuous.
	if len(owner) == 0 {
		t.Fatalf("parsed %d CRDs but found no shortNames; manifest parsing likely broke", crdCount)
	}
}
