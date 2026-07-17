---
type: Agent Rule
title: Release Process
description: "Tags are the release identity; every minor release also gets a release-X.Y branch pushed at the tagged commit — the serviceable line for backports and patch releases."
status: live
read_when: "Cutting or preparing a release (tag push), editing the Release workflows, or bumping the release image pin."
applies_to: [".github/workflows/release*.yaml", "config/manager/kustomization.yaml"]
always_load: false
---

# Release Process

## Highest Priority

1. **The tag is the release.** `vX.Y.Z` pins the exact commit; the Release workflow
   triggers on it and `verify-version` enforces that the
   `config/manager/kustomization.yaml` pin matches it. Nothing else — not a branch,
   not a GitHub Release object — defines what code a release is. (The workflow's
   `release-notes` job does publish a GitHub Release object with generated notes
   once both images are pullable, but that object is presentation for the Releases
   tab — never treat it as the identity, and never create it by hand; after a tag
   push, verify it appeared.)
2. **Every minor release also gets a release branch.** When publishing `vX.Y.0`,
   create `release-X.Y` at the tagged commit and push it together with the tag:

   ```
   git tag vX.Y.0 <commit>
   git branch release-X.Y vX.Y.0
   git push origin vX.Y.0 release-X.Y
   ```

   The branch is the *serviceable line* for that release: backports and patch
   releases (`vX.Y.1`, …) land there — cherry-picked from `main`, tagged on the
   branch — so an old line can be fixed without shipping `main`'s unreleased work.
3. **Patch releases reuse the line.** `vX.Y.Z` (Z > 0) is tagged on `release-X.Y`;
   never create a new branch per patch.

## Anti-Patterns To Reject

- A release branch with no matching tag, or a tag whose `release-X.Y` branch was
  never pushed — the pairing is the point (existing pairs: `release-0.1`/`v0.1.0`,
  `release-0.2`/`v0.2.0`).
- Treating a release branch as the release identity (e.g. building "the release"
  from the branch head instead of the tag), or committing routine development to a
  release branch — everything lands on `main` first and is cherry-picked back.
- Per-patch branches (`release-0.2.1`) or GitFlow-style long-lived `develop`
  branches — `main` plus serviceable `release-X.Y` lines is the whole model.

The agent-side push rule still applies: creating the tag/branch pair is
preparation an agent may do; **pushing them is the release act and stays with
Grant** unless he explicitly asks for the push in the same request.
