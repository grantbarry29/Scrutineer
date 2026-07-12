# VERSION is the RELEASE version only: it names the published artifacts a `v*` tag
# push produces and pins the committed overlay newTags (config/manager,
# config/reporter-standalone). The release workflow's verify-version guard checks it
# against the pushed tag. It is deliberately NOT what local builds are tagged with.
VERSION ?= v0.1.0

# Everything built by this Makefile is a DEV build (#112): tagged dev-<git describe>
# so it can never shadow (or be shadowed by) a published release tag — release
# `vX.Y.Z` images are produced only by .github/workflows/release.yaml. The tag is
# baked into the manager binary (VERSION_LDFLAGS) so its self-referential image
# defaults (lock-probe pods, injected egress-reporter) point at the images built
# beside it; a mismatch fails loudly (ImagePullBackOff) instead of silently running
# stale release content.
DEV_TAG := dev-$(shell git describe --tags --always --dirty 2>/dev/null || echo unknown)
VERSION_PKG := github.com/grantbarry29/scrutineer/internal/version
VERSION_LDFLAGS := -ldflags "-X $(VERSION_PKG).Version=$(DEV_TAG)"

# Image URL for all build/load/deploy targets — a dev image by default. Override IMG
# explicitly only if you know the referenced image's baked-in version matches its tag.
IMG ?= ghcr.io/grantbarry29/scrutineer:$(DEV_TAG)

# CONTAINER_TOOL defines the container tool to be used for building images.
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases
	# RBAC is split by component so the in-process reporter's permissions are a
	# distinct, least-privilege role rather than aggregated into manager-role.
	# Each role is scoped to the package that declares its +kubebuilder:rbac markers.
	$(CONTROLLER_GEN) rbac:roleName=manager-role paths="./internal/controller/..." output:rbac:dir=config/rbac
	$(CONTROLLER_GEN) rbac:roleName=reporter-role paths="./internal/reporter/..." output:rbac:dir=config/rbac/reporter

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run gofmt against all Go files, including build-tagged files (e.g. //go:build e2e) that `go fmt ./...` skips.
	@gofmt -w $(shell git ls-files '*.go')

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet envtest ## Run tests (race detector on — #108; local and CI run this same line).
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test -race ./... -coverprofile cover.out

.PHONY: verify-samples
verify-samples: manifests install ## Server-side dry-run of config/samples manifests (requires kubectl + CRDs).
	@set -e; \
	for f in config/samples/scrutineer_*.yaml; do \
	  echo ">> verifying $$f"; \
	  kubectl apply --dry-run=server -f "$$f"; \
	done

.PHONY: test-e2e-images
test-e2e-images: kind-load kind-load-envoy kind-load-egress-reporter ## Build and load controller + Envoy egress-proxy images into kind (e2e prereq).

# The e2e suite is split by Ginkgo label into two:
#   - standard: controller logic, CRDs, evidence — everything NOT labeled "networking".
#   - networking: CNI-generic Envoy egress / routing-lock / DNS enforcement specs, run
#     across CNIs (see test/e2e/networking_suite_test.go).
.PHONY: test-e2e
test-e2e: manifests install ## Run the standard e2e suite (excludes networking) against the current cluster.
	@echo ">> running standard e2e suite against $$(kubectl config current-context)"
	@echo ">> ensure no other scrutineer controller is running (no concurrent 'make run')"
	@echo ">> live evidence specs need images in kind: run 'make test-e2e-images' once"
	@# VERSION_LDFLAGS bakes DEV_TAG into the test binary's in-process manager so the
	@# images it injects (lock probe, egress-reporter) are the ones test-e2e-images
	@# built and loaded (#112).
	go test $(VERSION_LDFLAGS) -tags=e2e -v ./test/e2e/... -timeout 20m -ginkgo.v --ginkgo.label-filter='!networking'

.PHONY: test-e2e-net
test-e2e-net: manifests ## Run the CNI-generic networking e2e suite against the CURRENT cluster (assumes it is prepped).
	@echo ">> running networking e2e suite against $$(kubectl config current-context)"
	@# SCRUTINEER_E2E_LOCK_VERIFY wires the verified-or-refused lock gate (#70) into the
	@# in-process manager; the gate spec asserts opposite outcomes per CNI.
	@# VERSION_LDFLAGS: see test-e2e (#112).
	SCRUTINEER_E2E_LOCK_VERIFY=1 go test $(VERSION_LDFLAGS) -tags=e2e -v ./test/e2e/... -timeout 20m -ginkgo.v --ginkgo.label-filter='networking'

.PHONY: test-e2e-net-kindnet
test-e2e-net-kindnet: ## Run the networking e2e suite on the kindnet cluster.
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) .devcontainer/kind-attach.sh
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) $(MAKE) install
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) $(MAKE) kind-load kind-load-envoy kind-load-egress-reporter
	$(MAKE) test-e2e-net

.PHONY: test-e2e-net-calico
test-e2e-net-calico: ## Run the networking e2e suite on the Calico cluster (run 'make kind-up-netpol' first).
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME_NETPOL) .devcontainer/kind-attach.sh
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME_NETPOL) $(MAKE) install
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME_NETPOL) $(MAKE) kind-load kind-load-envoy kind-load-egress-reporter
	$(MAKE) test-e2e-net

.PHONY: test-e2e-net-dual
test-e2e-net-dual: ## Run the networking e2e suite on the dual-stack cluster (run 'make kind-up-dual' first).
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME_DUAL) .devcontainer/kind-attach.sh
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME_DUAL) $(MAKE) install
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME_DUAL) $(MAKE) kind-load kind-load-envoy kind-load-egress-reporter
	$(MAKE) test-e2e-net

.PHONY: test-e2e-net-all
test-e2e-net-all: test-e2e-net-kindnet test-e2e-net-calico test-e2e-net-dual ## Run the networking e2e suite across all cluster flavors (kindnet + Calico + dual-stack).
	@KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) .devcontainer/kind-attach.sh
	@echo ">> networking e2e passed on kindnet + Calico + dual-stack; kubeconfig restored to $(KIND_CLUSTER_NAME)"

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary (dev version baked in).
	go build $(VERSION_LDFLAGS) -o bin/manager cmd/main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host (dev version baked in).
	go run $(VERSION_LDFLAGS) ./cmd/main.go

.PHONY: docker-build
docker-build: ## Build the manager dev image ($(IMG)).
	@echo ">> dev build: $(IMG)"
	$(CONTAINER_TOOL) build --build-arg VERSION=$(DEV_TAG) -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | kubectl delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | kubectl apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/default | kubectl delete --ignore-not-found=$(ignore-not-found) -f -

##@ Dev cluster (kind)

KIND_CLUSTER_NAME ?= scrutineer-dev
KIND_CONFIG ?= .devcontainer/kind-config.yaml

.PHONY: kind-up
kind-up: ## Create the local kind cluster if it does not exist.
	@# Inside the dev container we use kind-up.sh, which is resilient to
	@# kind v0.31.0's flaky "remove control-plane taint" step (it manually
	@# installs the CNI and removes the taint if kind aborted early) and
	@# also wires the dev container onto the kind docker network.
	@if [ "$${SCRUTINEER_DEVCONTAINER:-0}" = "1" ] && [ -x .devcontainer/kind-up.sh ]; then \
		.devcontainer/kind-up.sh; \
	else \
		if kind get clusters 2>/dev/null | grep -qx $(KIND_CLUSTER_NAME); then \
			echo "kind cluster '$(KIND_CLUSTER_NAME)' already exists"; \
		else \
			kind create cluster --name $(KIND_CLUSTER_NAME) --config $(KIND_CONFIG) --wait 90s; \
		fi; \
		kubectl config use-context kind-$(KIND_CLUSTER_NAME) >/dev/null; \
	fi

.PHONY: kind-down
kind-down: ## Delete the local kind cluster.
	@kind delete cluster --name $(KIND_CLUSTER_NAME)

# Second cluster for NetworkPolicy-enforcement e2e (Slice B, #61): a second,
# production-representative CNI (Calico) to cross-check enforcement details against
# kindnet (which also enforces egress policy — see .devcontainer/kind-config-netpol.yaml).
KIND_CLUSTER_NAME_NETPOL ?= scrutineer-netpol
KIND_CONFIG_NETPOL ?= .devcontainer/kind-config-netpol.yaml

.PHONY: kind-up-netpol
kind-up-netpol: ## Create the Calico (NetworkPolicy-enforcing) kind cluster for netpol e2e.
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME_NETPOL) KIND_CONFIG=$(KIND_CONFIG_NETPOL) KIND_CNI=calico .devcontainer/kind-up.sh

.PHONY: kind-down-netpol
kind-down-netpol: ## Delete the Calico netpol kind cluster.
	@kind delete cluster --name $(KIND_CLUSTER_NAME_NETPOL)

# Third cluster: dual-stack (IPv4+IPv6) for the egress-path posture e2e (#66) — proves
# IPv6 is denied by construction on a cluster that actually assigns IPv6 pod addresses.
KIND_CLUSTER_NAME_DUAL ?= scrutineer-dual
KIND_CONFIG_DUAL ?= .devcontainer/kind-config-dual.yaml

.PHONY: kind-up-dual
kind-up-dual: ## Create the dual-stack kind cluster for the IPv6-posture e2e.
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME_DUAL) KIND_CONFIG=$(KIND_CONFIG_DUAL) .devcontainer/kind-up.sh

.PHONY: kind-down-dual
kind-down-dual: ## Delete the dual-stack kind cluster.
	@kind delete cluster --name $(KIND_CLUSTER_NAME_DUAL)

.PHONY: kind-load
kind-load: docker-build ## Build the controller image and load it into kind.
	kind load docker-image $(IMG) --name $(KIND_CLUSTER_NAME)

.PHONY: docker-build-egress-reporter kind-load-egress-reporter
# Dev image like IMG (#112): same DEV_TAG so the manager's baked-in default
# (envoy.DefaultEgressReporterImage) resolves to the image built beside it.
EGRESS_REPORTER_IMG ?= ghcr.io/grantbarry29/scrutineer-egress-reporter:$(DEV_TAG)

docker-build-egress-reporter: ## Build the egress-reporter dev image (runs beside Envoy in the egress-proxy pod).
	@echo ">> dev build: $(EGRESS_REPORTER_IMG)"
	$(CONTAINER_TOOL) build --build-arg VERSION=$(DEV_TAG) -f Dockerfile.egress-reporter -t ${EGRESS_REPORTER_IMG} .

kind-load-egress-reporter: docker-build-egress-reporter ## Build and load egress-reporter image into kind.
	kind load docker-image $(EGRESS_REPORTER_IMG) --name $(KIND_CLUSTER_NAME)

.PHONY: kind-load-envoy
# The per-session egress proxy uses the upstream Envoy image (no first-party build);
# keep this tag in sync with envoy.DefaultEnvoyImage.
ENVOY_IMG ?= envoyproxy/envoy:distroless-v1.31-latest@sha256:451ad9c42b4a706092455d524e836365d265760e3e6337c1f42980b18db4c247
# The plain repo:tag form of ENVOY_IMG. `docker save` of a digest-qualified reference
# exports NO RepoTags, so the image would land untagged in the node's containerd and
# never match node.status.images (the e2e image-runnable guard) — save the tag instead.
ENVOY_IMG_TAG = $(firstword $(subst @, ,$(ENVOY_IMG)))

kind-load-envoy: ## Pull the upstream Envoy egress-proxy image and load it into kind.
	$(CONTAINER_TOOL) pull $(ENVOY_IMG)
	$(CONTAINER_TOOL) tag $(ENVOY_IMG) $(ENVOY_IMG_TAG)
	@# `kind load docker-image` uses `ctr import --all-platforms`, which fails on the
	@# multi-arch Envoy manifest when only the host platform was pulled. Import just the
	@# local single-platform image into the (single-node dev) cluster's containerd instead.
	$(CONTAINER_TOOL) save $(ENVOY_IMG_TAG) | $(CONTAINER_TOOL) exec -i $(KIND_CLUSTER_NAME)-control-plane ctr --namespace=k8s.io images import -

.PHONY: dev-up
dev-up: kind-up install ## Bring up kind + install CRDs (controller runs locally via `make run`).
	@echo ""
	@echo "Cluster $(KIND_CLUSTER_NAME) is up and Scrutineer CRDs are installed."
	@echo "Run 'make run' in another terminal to start the controller against this cluster,"
	@echo "then 'kubectl apply -f config/samples/' to try the sample AgentSessions."

.PHONY: dev-deploy
dev-deploy: kind-up kind-load deploy ## Build, load, and deploy the controller into the kind cluster.
	@kubectl -n scrutineer-system rollout status deployment/scrutineer-controller-manager --timeout=2m
	@echo "Scrutineer controller is running in-cluster."

.PHONY: dev-sample
dev-sample: ## Apply both sample AgentSessions to the kind cluster.
	kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession.yaml
	kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession_failing.yaml

.PHONY: dev-down
dev-down: kind-down ## Tear down the local kind cluster.

##@ Quickstart

# The front-door experience (#78/#79): one command from a fresh clone to a running,
# lock-verified Scrutineer on a dedicated kind cluster. Uses the released images when
# pullable, falls back to local builds — either way images are kind-loaded, so the
# cluster never pulls from a registry (probe pods use the controller image with
# PullIfNotPresent; see internal/enforcement/lockverify).
KIND_CLUSTER_NAME_QUICKSTART ?= scrutineer-quickstart
# QUICKSTART_CNI=kindnet (default, fast) or calico (production-representative; use if
# the default flavor's gate verdict comes back Refused on your kind/node versions).
QUICKSTART_CNI ?= kindnet

.PHONY: quickstart
quickstart: ## One command: kind cluster + Scrutineer controller, lock-gate verdict printed.
	@echo ">> quickstart: first run takes ~5 minutes (builds the images from this checkout); repeats are faster."
ifeq ($(QUICKSTART_CNI),calico)
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME_QUICKSTART) KIND_CONFIG=$(KIND_CONFIG_NETPOL) KIND_CNI=calico .devcontainer/kind-up.sh
else
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME_QUICKSTART) $(MAKE) kind-up
endif
	$(MAKE) quickstart-images
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME_QUICKSTART) $(MAKE) install deploy
	kubectl -n scrutineer-system rollout status deployment/scrutineer-controller-manager --timeout=3m
	$(MAKE) quickstart-verdict
	@echo ""
	@echo "Scrutineer is running on kind cluster '$(KIND_CLUSTER_NAME_QUICKSTART)'."
	@echo "Try a sample session:   kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession.yaml"
	@echo "Watch it:               kubectl get agentsessions -w"
	@echo "Inspect evidence:       kubectl get agentsession github-readme-update -o yaml"
	@echo "Tear down:              make quickstart-down"

.PHONY: quickstart-images
quickstart-images: ## Build controller + egress-reporter images from this checkout and load them (plus pinned Envoy) into the quickstart cluster.
	@# Always build from source — never pull the released tag (#109). The quickstart
	@# deploys THIS checkout's manifests/CRDs/demo, which assume this checkout's
	@# controller behavior; a released image can silently predate it (v0.1.0 predates
	@# the verified-or-refused lock gate, so the verdict wait times out). Envoy is the
	@# exception: upstream, digest-pinned, independent of the checkout.
	$(MAKE) docker-build
	$(MAKE) docker-build-egress-reporter
	kind load docker-image $(IMG) --name $(KIND_CLUSTER_NAME_QUICKSTART)
	kind load docker-image $(EGRESS_REPORTER_IMG) --name $(KIND_CLUSTER_NAME_QUICKSTART)
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME_QUICKSTART) $(MAKE) kind-load-envoy

.PHONY: quickstart-verdict
quickstart-verdict: ## Wait for and print the routing-lock verification verdict (verified-or-refused gate).
	@echo ">> waiting for the lock-verification probe (differential canary; see docs/design/untamperable-enforcement.md §4)..."
	@# The || true keeps the empty-grep iterations alive under this Makefile's
	@# bash -e -o pipefail shell (no verdict line yet is the expected first state).
	@verdict=""; \
	for i in $$(seq 1 60); do \
	  verdict=$$(kubectl -n scrutineer-system logs deployment/scrutineer-controller-manager 2>/dev/null \
	    | grep 'lock probe verdict' | tail -1 | sed -n 's/.*"verdict"[": ]*\([A-Za-z]*\).*/\1/p' || true); \
	  if [ -n "$$verdict" ]; then break; fi; \
	  sleep 2; \
	done; \
	if [ "$$verdict" = "Verified" ]; then \
	  echo ">> routing-lock enforcement VERIFIED on this cluster (enforced sessions will run)."; \
	elif [ -z "$$verdict" ]; then \
	  echo ">> no probe verdict yet (still Unknown). Check: kubectl -n scrutineer-system logs deployment/scrutineer-controller-manager"; \
	  exit 1; \
	else \
	  echo ">> lock verdict: $$verdict — this cluster's CNI did not prove NetworkPolicy enforcement."; \
	  echo ">> Enforced sessions will be REFUSED (loudly, by design: verified-or-refused)."; \
	  echo ">> Retry on Calico: make quickstart-down && make quickstart QUICKSTART_CNI=calico"; \
	  exit 1; \
	fi

.PHONY: quickstart-down
quickstart-down: ## Delete the quickstart kind cluster.
	@kind delete cluster --name $(KIND_CLUSTER_NAME_QUICKSTART)

# The demo targets belong to the quickstart cluster; running them against any other
# kube-context must be a deliberate act (#89): the guard refuses on a mismatched
# current-context, and every kubectl below pins --context so the guard and the
# actions cannot target different clusters.
DEMO_KUBE_CONTEXT ?= kind-$(KIND_CLUSTER_NAME_QUICKSTART)
DEMO_KUBECTL = kubectl --context $(DEMO_KUBE_CONTEXT)

.PHONY: demo-context-guard
demo-context-guard:
	@if [ "$(DEMO_KUBE_CONTEXT)" = "kind-$(KIND_CLUSTER_NAME_QUICKSTART)" ]; then \
	  ctx=$$(kubectl config current-context 2>/dev/null || echo '<none>'); \
	  if [ "$$ctx" != "$(DEMO_KUBE_CONTEXT)" ]; then \
	    echo ">> refusing: current kube-context is '$$ctx' but the demo targets '$(DEMO_KUBE_CONTEXT)'."; \
	    echo ">> Run 'make quickstart' first, or target another cluster deliberately:"; \
	    echo ">>   DEMO_KUBE_CONTEXT=$$ctx make demo"; \
	    exit 1; \
	  fi; \
	fi

.PHONY: demo
demo: demo-context-guard ## Guided egress-governance demo against the quickstart cluster (run 'make quickstart' first; see docs/demo.md).
	$(DEMO_KUBECTL) apply -f config/samples/demo/
	@echo ""
	@echo ">> two sessions are starting: demo-enforced and demo-audit — same busybox agent,"
	@echo ">> same allowlist (example.com), different policy mode. Waiting for both (~2 min)..."
	@# Fail fast instead of a blind 6-minute wait (#88). The loop watches, in order:
	@# the lock-gate condition (not Verified => demo-enforced is held by design and
	@# demo-audit would run WITHOUT an effective routing lock, so running the demo
	@# would demonstrate a falsehood — clean up and refuse); terminal phases
	@# (Denied/Failed => print the diagnosis); pods stuck in a waiting state the Job
	@# never fails on (the #82 CreateContainerConfigError class — session phase stays
	@# Running forever); then the 6m overall backstop.
	@deadline=$$(( $$(date +%s) + 360 )); stuck=0; \
	while :; do \
	  pe=$$($(DEMO_KUBECTL) get agentsession demo-enforced -o jsonpath='{.status.phase}' 2>/dev/null || true); \
	  pa=$$($(DEMO_KUBECTL) get agentsession demo-audit -o jsonpath='{.status.phase}' 2>/dev/null || true); \
	  if [ "$$pe" = "Succeeded" ] && [ "$$pa" = "Succeeded" ]; then break; fi; \
	  gate=$$($(DEMO_KUBECTL) get agentsession demo-enforced -o jsonpath='{.status.conditions[?(@.type=="EgressLockVerified")].status}' 2>/dev/null || true); \
	  if [ "$$gate" = "False" ]; then \
	    echo ""; \
	    echo ">> the routing-lock gate is not Verified on this cluster:"; \
	    $(DEMO_KUBECTL) get agentsession demo-enforced -o jsonpath='{.status.conditions[?(@.type=="EgressLockVerified")].reason}{": "}{.status.conditions[?(@.type=="EgressLockVerified")].message}{"\n"}' 2>/dev/null || true; \
	    echo ">> demo-enforced is held (verified-or-refused) and demo-audit would run without an"; \
	    echo ">> effective routing lock — its bypass row would contradict docs/demo.md."; \
	    echo ">> Cleaning up. Retry on Calico: make quickstart-down && make quickstart QUICKSTART_CNI=calico && make demo"; \
	    $(DEMO_KUBECTL) delete -f config/samples/demo/ --ignore-not-found >/dev/null 2>&1 || true; \
	    exit 1; \
	  fi; \
	  for s in demo-enforced demo-audit; do \
	    p=$$($(DEMO_KUBECTL) get agentsession $$s -o jsonpath='{.status.phase}' 2>/dev/null || true); \
	    if [ "$$p" = "Failed" ] || [ "$$p" = "Denied" ]; then \
	      echo ""; \
	      echo ">> $$s reached phase $$p:"; \
	      $(DEMO_KUBECTL) get agentsession $$s -o jsonpath='{range .status.conditions[*]}{.type}={.status} ({.reason}) {.message}{"\n"}{end}' 2>/dev/null || true; \
	      echo ">> recent events:"; \
	      $(DEMO_KUBECTL) get events --field-selector involvedObject.name=$$s --sort-by=.lastTimestamp 2>/dev/null | tail -5 || true; \
	      echo ">> agent log tail:"; \
	      $(DEMO_KUBECTL) logs job/scrutineer-session-$$s --tail=10 2>/dev/null || true; \
	      exit 1; \
	    fi; \
	  done; \
	  wr=$$($(DEMO_KUBECTL) get pods -l 'scrutineer.sh/session in (demo-enforced,demo-audit)' \
	    -o jsonpath='{range .items[*]}{.metadata.name}{": "}{.status.containerStatuses[*].state.waiting.reason}{"\n"}{end}' 2>/dev/null \
	    | grep -E 'CreateContainerConfigError|ImagePullBackOff|CrashLoopBackOff|InvalidImageName' || true); \
	  if [ -n "$$wr" ]; then stuck=$$((stuck+1)); else stuck=0; fi; \
	  if [ "$$stuck" -ge 3 ]; then \
	    echo ""; \
	    echo ">> demo pods are stuck (the Job will never fail on this; phase stays Running):"; \
	    echo "$$wr"; \
	    echo ">> details: kubectl --context $(DEMO_KUBE_CONTEXT) get events --sort-by=.lastTimestamp | tail"; \
	    exit 1; \
	  fi; \
	  if [ $$(date +%s) -ge $$deadline ]; then \
	    echo ">> timed out after 6m; phases: demo-enforced=$$pe demo-audit=$$pa. Pod states:"; \
	    $(DEMO_KUBECTL) get pods 2>/dev/null | grep -E 'demo|NAME' || true; \
	    exit 1; \
	  fi; \
	  sleep 5; \
	done; \
	echo ">> both sessions Succeeded."
	@echo ""
	@echo "===== the agent's own view (enforced): allowed proxied, denial rejected, bypass dead ====="
	@out=$$($(DEMO_KUBECTL) logs job/scrutineer-session-demo-enforced --tail=-1 2>/dev/null | grep 'DEMO_' || true); \
	  if [ -n "$$out" ]; then echo "$$out"; \
	  else echo ">> (no agent logs: the Job's pod was already collected — re-run 'make demo', or use Scrutineer's view below)"; fi
	@echo ""
	@echo "===== the agent's own view (audit-only): nothing blocked at L7, bypass still dead ====="
	@out=$$($(DEMO_KUBECTL) logs job/scrutineer-session-demo-audit --tail=-1 2>/dev/null | grep 'DEMO_' || true); \
	  if [ -n "$$out" ]; then echo "$$out"; \
	  else echo ">> (no agent logs: the Job's pod was already collected — re-run 'make demo', or use Scrutineer's view below)"; fi
	@echo ""
	@echo "===== Scrutineer's view: observed runtime evidence in status.policyDecisions ====="
	@# Evidence lands via the out-of-pod egress-reporter; give a lagging batch a moment.
	@for i in $$(seq 1 15); do \
	  n=$$($(DEMO_KUBECTL) get agentsession demo-enforced -o jsonpath='{range .status.policyDecisions[?(@.phase=="runtime")]}{.action}{"\n"}{end}' 2>/dev/null | grep -c . || true); \
	  if [ "$${n:-0}" -gt 0 ]; then break; fi; sleep 2; \
	done
	@echo "--- demo-enforced (ACTION  TARGET  REASON  ASSURANCE) ---"
	@$(DEMO_KUBECTL) get agentsession demo-enforced -o jsonpath='{range .status.policyDecisions[?(@.phase=="runtime")]}{.action}{"\t"}{.target}{"\t"}{.reason}{"\t"}{.assuranceLevel}{"\n"}{end}' | sort | uniq -c || true
	@echo "--- demo-audit (ACTION  TARGET  REASON  ASSURANCE) ---"
	@$(DEMO_KUBECTL) get agentsession demo-audit -o jsonpath='{range .status.policyDecisions[?(@.phase=="runtime")]}{.action}{"\t"}{.target}{"\t"}{.reason}{"\t"}{.assuranceLevel}{"\n"}{end}' | sort | uniq -c || true
	@echo ""
	@echo "Every runtime decision above is stamped 'observed' from the egress-proxy pod's own"
	@echo "identity — the agent never reported anything and could not have forged this."
	@echo "Full walkthrough + what to look at next: docs/demo.md. Clean up: make demo-down"

.PHONY: demo-down
demo-down: demo-context-guard ## Delete the demo sessions, policies, and profile (quickstart cluster only; see demo-context-guard).
	$(DEMO_KUBECTL) delete -f config/samples/demo/ --ignore-not-found

##@ Dependencies

LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest

KUSTOMIZE_VERSION ?= v5.4.3
CONTROLLER_TOOLS_VERSION ?= v0.16.1
ENVTEST_K8S_VERSION = 1.31.0

.PHONY: kustomize
kustomize: $(KUSTOMIZE)
$(KUSTOMIZE): $(LOCALBIN)
	test -s $(LOCALBIN)/kustomize || GOBIN=$(LOCALBIN) go install sigs.k8s.io/kustomize/kustomize/v5@$(KUSTOMIZE_VERSION)

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN)
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(LOCALBIN)/controller-gen || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

.PHONY: envtest
envtest: $(ENVTEST)
$(ENVTEST): $(LOCALBIN)
	test -s $(LOCALBIN)/setup-envtest || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.19

.PHONY: lint-docs
lint-docs: ## Validate OKF frontmatter across docs/, dev-agent-rules/, and component READMEs (#127).
	go run ./hack/okf-lint
