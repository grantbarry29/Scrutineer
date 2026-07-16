#!/usr/bin/env bash
# validate-examples.sh — server-side dry-run validation of every examples/<scenario>/
# folder against the installed CRDs (#157). Users apply each folder with a single
# `kubectl apply -f examples/<name>/`; this gate makes sure the shipped manifests cannot
# silently rot — a folder whose YAML fails schema validation fails the build.
#
# Requires a cluster whose Scrutineer CRDs are installed (`make install`). It is a
# schema/structural floor, not a functional test: --dry-run=server validates each object
# against its CRD OpenAPI schema without persisting anything, so cross-object references
# (a session naming a profile/policy) are resolved by the controller at runtime, not here.
# README.md files are ignored — `kubectl apply -f <dir>` only reads .yaml/.yml/.json.
set -euo pipefail

root="${1:-examples}"
if [ ! -d "$root" ]; then
  echo "validate-examples: no ${root}/ directory" >&2
  exit 1
fi

fail=0
shopt -s nullglob
found=0
for dir in "$root"/*/; do
  found=1
  echo "== validating ${dir} =="
  if ! kubectl apply --dry-run=server -f "$dir"; then
    echo "FAIL: ${dir} did not pass server-side validation" >&2
    fail=1
  fi
done

if [ "$found" -eq 0 ]; then
  echo "validate-examples: no scenario folders under ${root}/" >&2
  exit 1
fi
if [ "$fail" -ne 0 ]; then
  echo "validate-examples: one or more example folders failed validation" >&2
  exit 1
fi
echo "validate-examples: all example folders passed server-side validation"
