/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// okf-lint validates the repo's OKF (Open Knowledge Format v0.1) knowledge
// bundles: docs/ and dev-agent-rules/, plus the component READMEs under
// cmd/, internal/, and config/. Rules (#127):
//
//   - Every non-reserved .md in a bundle carries YAML frontmatter with a
//     non-empty `type` and a `status` from the fixed vocabulary.
//   - `superseded_by`, when present, resolves to an existing file
//     ("/..." is bundle-root-relative, else relative to the doc).
//   - Reserved index.md files carry no frontmatter — except a bundle-root
//     index.md, which may declare only okf_version / okf_spec_rev (SPEC §11).
//   - Component READMEs carry the same frontmatter contract (concept files;
//     README.md is not reserved in OKF).
//
// Root README.md and CLAUDE.md are deliberately out of scope: they are the
// human landing page and the harness entry point, not bundle concepts.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

var bundles = []string{"docs", "dev-agent-rules"}

var componentReadmeRoots = []string{"cmd", "internal", "config"}

var statusVocab = map[string]bool{
	"draft": true, "approved": true, "implemented": true, "decided": true,
	"historical": true, "investigation": true, "live": true,
}

// bundle-root index.md may carry only these keys (OKF §11).
var indexRootKeys = map[string]bool{"okf_version": true, "okf_spec_rev": true}

type problem struct {
	file string
	msg  string
}

func main() {
	var problems []problem
	fail := func(file, format string, a ...any) {
		problems = append(problems, problem{file, fmt.Sprintf(format, a...)})
	}

	for _, bundle := range bundles {
		root := bundle
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".md") {
				return nil
			}
			if filepath.Base(path) == "index.md" {
				lintIndex(path, root, fail)
				return nil
			}
			lintConcept(path, root, fail)
			return nil
		})
		if err != nil {
			fail(root, "walk: %v", err)
		}
	}

	for _, root := range componentReadmeRoots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || filepath.Base(path) != "README.md" {
				return nil
			}
			lintConcept(path, "", fail)
			return nil
		})
		if err != nil {
			fail(root, "walk: %v", err)
		}
	}

	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "okf-lint: %s: %s\n", p.file, p.msg)
		}
		fmt.Fprintf(os.Stderr, "okf-lint: %d problem(s)\n", len(problems))
		os.Exit(1)
	}
	fmt.Println("okf-lint: OK")
}

// frontmatter returns the parsed YAML block and whether one exists at all.
func frontmatter(path string, fail func(string, string, ...any)) (map[string]any, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		fail(path, "read: %v", err)
		return nil, false
	}
	text := string(raw)
	if !strings.HasPrefix(text, "---\n") {
		return nil, false
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		fail(path, "unterminated frontmatter block")
		return nil, false
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(text[4:4+end]), &doc); err != nil {
		fail(path, "frontmatter is not valid YAML: %v", err)
		return nil, true
	}
	return doc, true
}

func lintConcept(path, bundleRoot string, fail func(string, string, ...any)) {
	doc, ok := frontmatter(path, fail)
	if !ok {
		fail(path, "missing YAML frontmatter (every bundle concept and component README needs one; see #127)")
		return
	}
	if doc == nil {
		return // parse error already reported
	}
	typ, _ := doc["type"].(string)
	if strings.TrimSpace(typ) == "" {
		fail(path, "frontmatter `type` is missing or empty (required by OKF §9)")
	}
	status, _ := doc["status"].(string)
	if !statusVocab[status] {
		fail(path, "frontmatter `status` %q not in vocabulary %v", status, keys(statusVocab))
	}
	if sup, ok := doc["superseded_by"].(string); ok && sup != "" {
		target := sup
		if strings.HasPrefix(sup, "/") {
			if bundleRoot == "" {
				fail(path, "`superseded_by` %q is bundle-root-relative but the file is not in a bundle", sup)
				return
			}
			target = filepath.Join(bundleRoot, strings.TrimPrefix(sup, "/"))
		} else {
			target = filepath.Join(filepath.Dir(path), sup)
		}
		if _, err := os.Stat(target); err != nil {
			fail(path, "`superseded_by` target %q does not exist (resolved %q)", sup, target)
		}
	}
}

func lintIndex(path, bundleRoot string, fail func(string, string, ...any)) {
	doc, has := frontmatter(path, fail)
	if !has {
		return // frontmatter-free index is the normal case (OKF §6)
	}
	if path != filepath.Join(bundleRoot, "index.md") {
		fail(path, "non-root index.md must not carry frontmatter (OKF §6)")
		return
	}
	for k := range doc {
		if !indexRootKeys[k] {
			fail(path, "bundle-root index.md frontmatter may only declare okf_version/okf_spec_rev (OKF §11); found %q", k)
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
