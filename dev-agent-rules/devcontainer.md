# Devcontainer Is The Only Supported Toolchain

**All build/test/codegen for this repo runs inside the provided devcontainer — never
on the host.** The project pins **Go 1.23** (`.devcontainer/Dockerfile`
`VARIANT=1-1.23-bookworm`, matching `go.mod`) plus pinned `controller-gen`,
`setup-envtest`, `kind`, `kubectl`, etc. Running on a host with a different Go (e.g. a
Homebrew Go 1.26) silently breaks the toolchain — `controller-gen@v0.16.1` fails to
build (`x/tools` incompatibility), cached `bin/*` tools are the wrong OS/arch, and
envtest binaries are absent — producing failures that look like project bugs but are
not (this is what caused the now-closed issue #54). **Do not diagnose or "fix" such
failures on the host; reproduce in the container first.**

## Rules

- **Run every `make` target, `go test` (esp. envtest suites), and all codegen
  (`make manifests` / `make generate`) inside the devcontainer.** Treat host runs of
  these as invalid.
- **Before relying on a result, confirm you ran it in the container.** If you cannot
  reach the container, say so and do not substitute a host run.
- Quick, toolchain-insensitive sanity checks (`go build ./...`, `go vet`, non-envtest
  `go test`) *may* run on the host, but anything that drives `controller-gen`,
  `setup-envtest`, the kind cluster, or CRD/RBAC regeneration **must** use the container.
- Never commit host-built generated artifacts. Regenerate in the container and verify
  an empty `git diff` against the checked-in output.

## How to use it (from the host shell)

The devcontainer is the running container whose image is `vsc-scrutineer-*`; the repo is
bind-mounted at `/workspaces/Scrutineer` (same files and git commit as the host
checkout).

```sh
# find it
docker ps --format '{{.Names}}\t{{.Image}}'
# run a target in it
docker exec <container-name> bash -lc 'cd /workspaces/Scrutineer && make test'
```

If no `vsc-scrutineer-*` container is running, start the devcontainer (VS Code "Reopen
in Container", or the `.devcontainer` tooling) before doing build/test/codegen work.
