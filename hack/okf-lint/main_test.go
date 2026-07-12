/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBulletHref(t *testing.T) {
	cases := []struct {
		line string
		href string
		ok   bool
	}{
		{"* [Title](target.md) - Some description.", "target.md", true},
		{"- [Title](sub/dir/target.md) - Desc.", "sub/dir/target.md", true},
		{"* [Title](target.md)", "target.md", true},
		{"* [Title](target.md#anchor) - Desc.", "target.md#anchor", true},
		{"* [Title](https://example.com) - Desc.", "https://example.com", true},
		{"Some prose with a [link](target.md).", "", false},
		{"  * [Indented](target.md) - not a top-level bullet.", "", false},
		{"# A heading", "", false},
	}
	for _, c := range cases {
		href, ok := bulletHref(c.line)
		if ok != c.ok || href != c.href {
			t.Errorf("bulletHref(%q) = %q, %v; want %q, %v", c.line, href, ok, c.href, c.ok)
		}
	}
}

func TestCanonicalBullet(t *testing.T) {
	cases := []struct {
		name string
		fm   map[string]any
		line string
		want string
	}{
		{
			name: "plain title and description",
			fm:   map[string]any{"title": "My Doc", "description": "One-line summary."},
			line: "* [Old Title](my-doc.md) - stale text.",
			want: "* [My Doc](my-doc.md) - One-line summary.",
		},
		{
			name: "marker preserved",
			fm:   map[string]any{"title": "My Doc", "description": "Summary."},
			line: "- [My Doc](my-doc.md) - Summary.",
			want: "- [My Doc](my-doc.md) - Summary.",
		},
		{
			name: "whitespace in description collapsed",
			fm:   map[string]any{"title": "My Doc", "description": "Spread  over\n  lines."},
			line: "* [My Doc](my-doc.md) - whatever.",
			want: "* [My Doc](my-doc.md) - Spread over lines.",
		},
		{
			name: "applies_to renders a mechanical suffix",
			fm: map[string]any{
				"title":       "Rule",
				"description": "A scoped rule.",
				"applies_to":  []any{"internal/**/*.go", "cmd/**/*.go"},
			},
			line: "* [Rule](rule.md) - old.",
			want: "* [Rule](rule.md) - A scoped rule. Applies to: `internal/**/*.go`, `cmd/**/*.go`.",
		},
	}
	for _, c := range cases {
		got, ok := canonicalBullet(c.line, c.fm)
		if !ok {
			t.Errorf("%s: canonicalBullet(%q) not renderable", c.name, c.line)
			continue
		}
		if got != c.want {
			t.Errorf("%s:\n got  %q\n want %q", c.name, got, c.want)
		}
	}
}

func TestCanonicalBulletUnrenderable(t *testing.T) {
	// A target with no title/description cannot be rendered; the bullet
	// stays hand-curated and only target existence is checked.
	if _, ok := canonicalBullet("* [T](x.md) - d.", map[string]any{"type": "Design Doc"}); ok {
		t.Error("canonicalBullet should not render without title+description")
	}
	if _, ok := canonicalBullet("* [T](x.md) - d.", nil); ok {
		t.Error("canonicalBullet should not render with nil frontmatter")
	}
}

// writeTree lays out a miniature index + concept tree in a temp dir.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const conceptDoc = `---
type: Design Doc
title: Target Doc
description: "The canonical summary."
status: draft
---

# Target Doc
`

func TestLintIndexBullets(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"target.md": conceptDoc,
		"index.md": strings.Join([]string{
			"# Index",
			"",
			"* [Target Doc](target.md) - The canonical summary.",   // in sync
			"* [Target Doc](target.md) - Stale, drifted text.",     // drifted
			"* [Gone](missing.md) - Points at nothing.",            // dangling
			"* [External](https://example.com) - Never checked.",   // skipped
			"* [Sub-index](sub/index.md) - Hand-curated, no sync.", // no fm target
			"",
		}, "\n"),
		"sub/index.md": "# Sub\n\n* [Target Doc](../target.md) - The canonical summary.\n",
	})

	var problems []string
	fail := func(file, format string, a ...any) {
		problems = append(problems, file+": "+strings.SplitN(format, " %", 2)[0])
	}
	lintIndexBullets(filepath.Join(dir, "index.md"), fail)

	if len(problems) != 2 {
		t.Fatalf("want 2 problems (drift + dangling), got %d: %v", len(problems), problems)
	}
}

func TestWriteIndexBulletsSyncsAndIsIdempotent(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"target.md": conceptDoc,
		"index.md":  "# Index\n\n* [Old](target.md) - drifted.\n* [Gone](missing.md) - kept as-is.\n",
	})
	idx := filepath.Join(dir, "index.md")

	changed, err := writeIndexBullets(idx)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first write should report a change")
	}
	got, _ := os.ReadFile(idx)
	want := "# Index\n\n* [Target Doc](target.md) - The canonical summary.\n* [Gone](missing.md) - kept as-is.\n"
	if string(got) != want {
		t.Fatalf("synced index:\n got  %q\n want %q", got, want)
	}

	changed, err = writeIndexBullets(idx)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("second write must be a no-op (round-trip invariant)")
	}

	// The round-trip invariant: a synced index passes the lint.
	var problems []string
	fail := func(file, format string, a ...any) { problems = append(problems, file) }
	lintIndexBullets(idx, fail)
	// One problem remains: the dangling target (deliberately not synced).
	if len(problems) != 1 {
		t.Fatalf("synced index should only report the dangling target, got %v", problems)
	}
}
