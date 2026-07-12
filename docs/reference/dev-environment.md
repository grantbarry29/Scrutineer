---
type: Reference
title: Development Environment & Local Runs
description: "The devcontainer (recommended), dev-cluster Makefile targets, pinned tool versions, the step-by-step host-side MVP walkthrough with samples, in-cluster controller deployment, and the sample-verified acceptance checklist."
status: live
read_when: "Setting up a dev environment, running samples, or changing pinned tool versions / dev Makefile targets."
---

# Development Environment & Local Runs

## Developing with the dev container (recommended for contributors)

The repo ships with a `.devcontainer/` that gives you a fully wired Scrutineer dev
environment with **zero host setup beyond Docker + VS Code**.

What you get when you open the folder in a Dev Container:

- Go 1.23 toolchain
- Docker-in-Docker
- `kubectl`, `kind`, `kustomize` pre-installed
- A local `kind` cluster named **`scrutineer-dev`** created automatically
- The Scrutineer CRD installed into that cluster on first start

### Open it

1. Install [Docker Desktop](https://www.docker.com/products/docker-desktop/) on
   your host (or any Docker-compatible runtime).
2. Open this folder in VS Code.
3. When prompted, choose **"Reopen in Container"**, or run the
   `Dev Containers: Reopen in Container` command.

On first build the `postCreateCommand` (`.devcontainer/bootstrap.sh`) will:

1. Wait for the in-container Docker daemon.
2. `go mod download`.
3. Create the `scrutineer-dev` kind cluster (idempotent — re-runs are safe).
4. `kubectl apply` the Scrutineer CRD.
5. Print the next-step commands.

### Inside the container

```bash
# (1) Run the controller against the kind cluster from your terminal:
make run

# (2) In a second terminal, apply a sample AgentSession:
kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession.yaml
kubectl get agentsessions -w
kubectl describe agentsession github-readme-update
kubectl logs job/scrutineer-session-github-readme-update

# (3) Or build, kind-load, and deploy the controller as an in-cluster Pod:
make dev-deploy

# (4) Both samples at once:
make dev-sample

# (5) Tear it all down:
make dev-down
```

### Dev-cluster Makefile targets

| Target          | What it does                                                       |
|-----------------|--------------------------------------------------------------------|
| `make kind-up`  | Create the `scrutineer-dev` kind cluster (no-op if it exists).          |
| `make kind-down`| Delete the `scrutineer-dev` kind cluster.                               |
| `make kind-load`| `docker-build` the controller image + `kind load docker-image`.    |
| `make dev-up`   | `kind-up` + install CRDs. Use with `make run` for the dev loop.    |
| `make dev-deploy`| Build + load + deploy the controller into the kind cluster.       |
| `make dev-sample`| Apply success + failing sample AgentSessions.                     |
| `make verify-samples` | Server-side dry-run all `config/samples/scrutineer_*.yaml` (needs CRDs). |
| `make dev-down` | Alias for `kind-down`.                                             |

You can also run these targets **outside** the dev container as long as Docker,
`kind`, and `kubectl` are on your `PATH`.

## Running the MVP without the dev container

### Prerequisites

- Go 1.23+
- A Kubernetes cluster you can reach via `kubectl` (kind/k3d/minikube/EKS/GKE all fine)
- `make`
- Optional: `docker`/`podman` if you want to build a controller image

The Makefile auto-installs `controller-gen`, `kustomize`, and `setup-envtest`
into `./bin/` on first use.

#### Pinned tool versions

These are pinned so contributors don't hit Go/envtest/apiserver version skew.
The values below are mirrored from the source of truth — `Makefile` and
`.devcontainer/kind-config.yaml` — so update those files (not just this table)
when a version changes:

| Tool | Version | Pinned in |
|------|---------|-----------|
| Go toolchain | `1.23` | `.devcontainer/devcontainer.json` (`VARIANT=1-1.23-bookworm`), CI workflows |
| `controller-gen` | `v0.16.1` | `Makefile` (`CONTROLLER_TOOLS_VERSION`) |
| `kustomize` | `v5.4.3` | `Makefile` (`KUSTOMIZE_VERSION`) |
| `setup-envtest` | `release-0.19` | `Makefile` |
| envtest Kubernetes assets | `1.31.0` | `Makefile` (`ENVTEST_K8S_VERSION`) |
| kind node image (dev + e2e) | `kindest/node:v1.31.4` | `.devcontainer/kind-config.yaml` |
| kind CLI (CI e2e) | `v0.31.0` | `.github/workflows/e2e.yaml` |

`make test` runs the envtest suite against the `ENVTEST_K8S_VERSION` apiserver
(`1.31.0`), and the dev/e2e `kind` cluster pins `kindest/node:v1.31.4` so the
CRD is exercised against a matching apiserver version in both unit and e2e
runs. Do **not** upgrade these unless something is broken.

### 1. Generate code and CRDs

```
make generate    # regenerate zz_generated.deepcopy.go
make manifests   # regenerate config/crd/bases and RBAC
```

A pre-generated CRD is already checked in at
`config/crd/bases/scrutineer.sh_agentsessions.yaml`, so this step is only
needed after editing `api/v1alpha1/*.go`.

### 2. Install the CRD

```
make install
```

This applies `config/crd` to the cluster pointed at by your current kubeconfig.

### 3. Run the controller against your cluster

From a separate terminal:

```
make run
```

This runs the controller-manager locally and connects to your cluster as your
current kubeconfig user.

### 4. Apply a sample AgentSession

```
kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession.yaml
```

### 5. Observe it

```
kubectl get agentsessions
kubectl describe agentsession github-readme-update
kubectl get jobs
kubectl logs job/scrutineer-session-github-readme-update
```

You should see:

- `kubectl get agentsessions` showing `Phase` transition
  `Starting` → `Running` → `Succeeded`
- `kubectl describe` showing Events: `JobCreated`, `JobRunning`, `JobSucceeded`
- `kubectl logs` showing the injected `SCRUTINEER_*` / `AGENT_*` env values

### 6. Try the failing sample

```
kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession_failing.yaml
kubectl get agentsessions
```

It should transition to `Failed` with a `JobFailed` event and
`Completed=False` condition.

### 7. Try the prompt ConfigMap sample

```
kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession_prompt_cm.yaml
kubectl get agentsession github-readme-from-cm -w
```

Applies a ConfigMap plus an AgentSession that loads `spec.task.promptConfigMapRef`
(same namespace). Expect `Succeeded` when the controller is running.

### 8. Try the cancellation sample

```
kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession_cancel.yaml
kubectl get agentsession cancel-at-create-sample -w
```

Expect `Phase=Cancelled` and no `scrutineer-session-cancel-at-create-sample` Job.

To cancel a long-running session, apply the success sample, wait for `Running`, then patch `cancelRequested` as described in [Cancelling a running session](agentsession-crd.md#cancelling-a-running-session).

### 9. Try the RuntimeProfile sample

```
kubectl apply -f config/samples/scrutineer_v1alpha1_runtimeprofile.yaml
kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession_runtimeprofile_ref.yaml
kubectl get agentsession session-with-runtimeprofile -w
```

Expect a Job whose pod template includes settings from `hardened-agent` (see `kubectl get job scrutineer-session-session-with-runtimeprofile -o yaml`). The sample uses stricter container hardening; use an image compatible with `runAsNonRoot` / `readOnlyRootFilesystem` in production.

### Validate samples against the installed CRD

After `make install` (or `make dev-up`), check that hand-maintained samples still match the API:

```
make verify-samples
```

This runs `kubectl apply --dry-run=server` on each `config/samples/scrutineer_*.yaml` (success, failing, cancel-at-create, prompt ConfigMap, AgentPolicy/RuntimeProfile refs).

### Deploying the controller into the cluster

```
make docker-build IMG=ghcr.io/grantbarry29/scrutineer:dev
make docker-push  IMG=ghcr.io/grantbarry29/scrutineer:dev
make deploy       IMG=ghcr.io/grantbarry29/scrutineer:dev
```

To remove:

```
make undeploy
make uninstall
```

## Acceptance criteria (verified by the samples)

After running the controller and applying the success sample:

- [x] `AgentSession` CRD is installed in the cluster
- [x] The sample AgentSession is accepted
- [x] The controller creates a Job named `scrutineer-session-github-readme-update`
- [x] The Job runs and exits 0
- [x] `status.phase` transitions `Pending` → `Starting` → `Running` → `Succeeded`
- [x] `status.jobName` is populated
- [x] `status.podName` is populated once a pod exists
- [x] Kubernetes Events `JobCreated`, `JobRunning`, `JobSucceeded` are visible in `kubectl describe`
- [x] `status.conditions` include `Validated`, `RuntimeCreated`, `Completed`, and `Ready`
- [x] The failing sample transitions to `Failed` and emits `JobFailed`

