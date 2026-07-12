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
// Index bullets are synced content inside a hand-curated skeleton (#128):
//
//   - Section headings, ordering, prose, and link targets stay hand-authored,
//     but each bullet's title and trailing description render from the target
//     doc's frontmatter (`title`, `description`, plus an "Applies to:" suffix
//     from `applies_to`). The lint fails on any out-of-sync bullet; run
//     `go run ./hack/okf-lint -write-index` (or `make gen-index`) to resync.
//   - Bullet link targets must exist. Targets without a renderable
//     title+description (sub-indexes, external URLs) stay hand-curated and
//     get only the existence check.
//   - The repo-root index.md knowledge map gets the same bullet checks.
//
// Root README.md and CLAUDE.md are deliberately out of scope: they are the
// human landing page and the harness entry point, not bundle concepts.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var bundles = []string{"docs", "dev-agent-rules"}

var componentReadmeRoots = []string{"cmd", "internal", "config"}

// rootIndexes are indexes outside any bundle that still get the bullet
// checks (the repo-root knowledge map), but none of the reserved-file rules.
var rootIndexes = []string{"index.md"}

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
	writeIndex := flag.Bool("write-index", false,
		"rewrite index bullet titles/descriptions from target frontmatter, then exit")
	flag.Parse()
	if *writeIndex {
		runWriteIndex()
		return
	}

	var problems []problem
	fail := func(file, format string, a ...any) {
		problems = append(problems, problem{file, fmt.Sprintf(format, a...)})
	}

	for _, root := range rootIndexes {
		lintIndexBullets(root, fail)
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
			if filepath.Base(path) == "README.md" {
				fail(path, "bundle directories index with reserved index.md, not README.md (#127)")
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
	title, _ := doc["title"].(string)
	if strings.TrimSpace(title) == "" {
		fail(path, "frontmatter `title` is missing or empty (index bullets render from it; #128)")
	}
	desc, _ := doc["description"].(string)
	if strings.TrimSpace(desc) == "" {
		fail(path, "frontmatter `description` is missing or empty (index bullets render from it; #128)")
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
	if has {
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
	raw, err := os.ReadFile(path)
	if err != nil {
		fail(path, "read: %v", err)
		return
	}
	hasBullet := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "* [") || strings.HasPrefix(line, "- [") {
			hasBullet = true
			break
		}
	}
	if !hasBullet {
		fail(path, "index.md contains no link-list bullets (OKF §6)")
	}
	lintIndexBullets(path, fail)
}

var bulletRe = regexp.MustCompile(`^[*-] \[[^\]]*\]\(([^)]+)\)`)

// bulletHref returns the link target of a top-level index bullet line.
func bulletHref(line string) (string, bool) {
	m := bulletRe.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// bulletTarget resolves an href against the index's directory, or returns
// ok=false for links the bullet checks never touch (external, mailto).
func bulletTarget(indexPath, href string) (string, bool) {
	if strings.Contains(href, "://") || strings.HasPrefix(href, "mailto:") {
		return "", false
	}
	target, _, _ := strings.Cut(href, "#")
	return filepath.Join(filepath.Dir(indexPath), target), true
}

// targetFrontmatter quietly loads a bullet target's frontmatter; nil when the
// file has none or it doesn't parse (concept lint reports those separately).
func targetFrontmatter(path string) map[string]any {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	text := string(raw)
	if !strings.HasPrefix(text, "---\n") {
		return nil
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return nil
	}
	var doc map[string]any
	if yaml.Unmarshal([]byte(text[4:4+end]), &doc) != nil {
		return nil
	}
	return doc
}

// canonicalBullet renders the synced form of a bullet line from its target's
// frontmatter: marker and href are kept from the line, title and description
// come from the frontmatter, and non-empty `applies_to` renders a mechanical
// "Applies to:" suffix. ok=false when the target has no renderable
// title+description (the bullet stays hand-curated).
func canonicalBullet(line string, fm map[string]any) (string, bool) {
	href, ok := bulletHref(line)
	if !ok || fm == nil {
		return "", false
	}
	title := strings.Join(strings.Fields(str(fm["title"])), " ")
	desc := strings.Join(strings.Fields(str(fm["description"])), " ")
	if title == "" || desc == "" {
		return "", false
	}
	suffix := ""
	if globs, ok := fm["applies_to"].([]any); ok && len(globs) > 0 {
		quoted := make([]string, 0, len(globs))
		for _, g := range globs {
			quoted = append(quoted, "`"+str(g)+"`")
		}
		suffix = " Applies to: " + strings.Join(quoted, ", ") + "."
	}
	return fmt.Sprintf("%c [%s](%s) - %s%s", line[0], title, href, desc, suffix), true
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// lintIndexBullets checks every bullet in an index: the link target must
// exist, and when the target has a renderable title+description the whole
// bullet line must equal its canonical (synced) form.
func lintIndexBullets(path string, fail func(string, string, ...any)) {
	raw, err := os.ReadFile(path)
	if err != nil {
		fail(path, "read: %v", err)
		return
	}
	for _, line := range strings.Split(string(raw), "\n") {
		href, ok := bulletHref(line)
		if !ok {
			continue
		}
		target, ok := bulletTarget(path, href)
		if !ok {
			continue
		}
		st, err := os.Stat(target)
		if err != nil {
			fail(path, "index bullet target %q does not exist (resolved %q)", href, target)
			continue
		}
		if st.IsDir() || !strings.HasSuffix(target, ".md") {
			continue
		}
		want, ok := canonicalBullet(line, targetFrontmatter(target))
		if !ok {
			continue
		}
		if line != want {
			fail(path, "index bullet out of sync with %s frontmatter (run `go run ./hack/okf-lint -write-index`)\n  have: %s\n  want: %s", target, line, want)
		}
	}
}

// writeIndexBullets rewrites every syncable bullet line to its canonical
// form, reporting whether the file changed. Dangling targets and
// hand-curated bullets are left untouched (the lint reports the former).
func writeIndexBullets(path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	lines := strings.Split(string(raw), "\n")
	changed := false
	for i, line := range lines {
		href, ok := bulletHref(line)
		if !ok {
			continue
		}
		target, ok := bulletTarget(path, href)
		if !ok {
			continue
		}
		if st, err := os.Stat(target); err != nil || st.IsDir() || !strings.HasSuffix(target, ".md") {
			continue
		}
		want, ok := canonicalBullet(line, targetFrontmatter(target))
		if !ok || line == want {
			continue
		}
		lines[i] = want
		changed = true
	}
	if changed {
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			return false, err
		}
	}
	return changed, nil
}

// runWriteIndex is the -write-index mode: resync every index in place.
func runWriteIndex() {
	indexes := append([]string{}, rootIndexes...)
	for _, bundle := range bundles {
		_ = filepath.WalkDir(bundle, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && filepath.Base(path) == "index.md" {
				indexes = append(indexes, path)
			}
			return nil
		})
	}
	failed := false
	for _, idx := range indexes {
		changed, err := writeIndexBullets(idx)
		switch {
		case err != nil:
			fmt.Fprintf(os.Stderr, "okf-lint: %s: %v\n", idx, err)
			failed = true
		case changed:
			fmt.Printf("okf-lint: synced %s\n", idx)
		}
	}
	if failed {
		os.Exit(1)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
