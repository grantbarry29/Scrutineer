# Image URL to use all building/pushing image targets
IMG ?= ghcr.io/grantbarry29/scrutineer:latest

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
test: manifests generate fmt vet envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test ./... -coverprofile cover.out

.PHONY: verify-samples
verify-samples: manifests install ## Server-side dry-run of config/samples manifests (requires kubectl + CRDs).
	@set -e; \
	for f in config/samples/scrutineer_*.yaml; do \
	  echo ">> verifying $$f"; \
	  kubectl apply --dry-run=server -f "$$f"; \
	done

.PHONY: test-e2e-images
test-e2e-images: kind-load kind-load-dns-proxy kind-load-tool-gateway kind-load-fs-gateway kind-load-envoy kind-load-egress-reporter ## Build and load controller + sidecar images into kind (e2e live-evidence prereq).

# The e2e suite is split by Ginkgo label into two:
#   - standard: controller logic, CRDs, evidence — everything NOT labeled "networking".
#   - networking: CNI-generic Envoy egress / routing-lock / DNS enforcement specs, run
#     across CNIs (see test/e2e/networking_suite_test.go).
.PHONY: test-e2e
test-e2e: manifests install ## Run the standard e2e suite (excludes networking) against the current cluster.
	@echo ">> running standard e2e suite against $$(kubectl config current-context)"
	@echo ">> ensure no other scrutineer controller is running (no concurrent 'make run')"
	@echo ">> live evidence specs need images in kind: run 'make test-e2e-images' once"
	go test -tags=e2e -v ./test/e2e/... -timeout 20m -ginkgo.v --ginkgo.label-filter='!networking'

.PHONY: test-e2e-net
test-e2e-net: manifests ## Run the CNI-generic networking e2e suite against the CURRENT cluster (assumes it is prepped).
	@echo ">> running networking e2e suite against $$(kubectl config current-context)"
	go test -tags=e2e -v ./test/e2e/... -timeout 20m -ginkgo.v --ginkgo.label-filter='networking'

.PHONY: test-e2e-net-kindnet
test-e2e-net-kindnet: ## Run the networking e2e suite on the kindnet cluster.
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) .devcontainer/kind-attach.sh
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) $(MAKE) install
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) $(MAKE) kind-load-envoy
	$(MAKE) test-e2e-net

.PHONY: test-e2e-net-calico
test-e2e-net-calico: ## Run the networking e2e suite on the Calico cluster (run 'make kind-up-netpol' first).
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME_NETPOL) .devcontainer/kind-attach.sh
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME_NETPOL) $(MAKE) install
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME_NETPOL) $(MAKE) kind-load-envoy
	$(MAKE) test-e2e-net

.PHONY: test-e2e-net-all
test-e2e-net-all: test-e2e-net-kindnet test-e2e-net-calico ## Run the networking e2e suite across all CNIs (kindnet + Calico).
	@KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) .devcontainer/kind-attach.sh
	@echo ">> networking e2e passed on kindnet + Calico; kubeconfig restored to $(KIND_CLUSTER_NAME)"

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/main.go

.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build -t ${IMG} .

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

# Second cluster for NetworkPolicy-enforcement e2e (Slice B, #61): kindnet does not
# enforce egress policy, so these specs need a Calico cluster.
KIND_CLUSTER_NAME_NETPOL ?= scrutineer-netpol
KIND_CONFIG_NETPOL ?= .devcontainer/kind-config-netpol.yaml

.PHONY: kind-up-netpol
kind-up-netpol: ## Create the Calico (NetworkPolicy-enforcing) kind cluster for netpol e2e.
	KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME_NETPOL) KIND_CONFIG=$(KIND_CONFIG_NETPOL) KIND_CNI=calico .devcontainer/kind-up.sh

.PHONY: kind-down-netpol
kind-down-netpol: ## Delete the Calico netpol kind cluster.
	@kind delete cluster --name $(KIND_CLUSTER_NAME_NETPOL)

.PHONY: kind-load
kind-load: docker-build ## Build the controller image and load it into kind.
	kind load docker-image $(IMG) --name $(KIND_CLUSTER_NAME)

.PHONY: docker-build-dns-proxy kind-load-dns-proxy
DNS_PROXY_IMG ?= ghcr.io/grantbarry29/scrutineer-dns-proxy:latest

docker-build-dns-proxy: ## Build the dns-proxy sidecar image.
	$(CONTAINER_TOOL) build -f Dockerfile.dns-proxy -t ${DNS_PROXY_IMG} .

kind-load-dns-proxy: docker-build-dns-proxy ## Build and load dns-proxy image into kind.
	kind load docker-image $(DNS_PROXY_IMG) --name $(KIND_CLUSTER_NAME)

.PHONY: docker-build-tool-gateway kind-load-tool-gateway
TOOL_GATEWAY_IMG ?= ghcr.io/grantbarry29/scrutineer-tool-gateway:latest

docker-build-tool-gateway: ## Build the tool-gateway sidecar image.
	$(CONTAINER_TOOL) build -f Dockerfile.tool-gateway -t ${TOOL_GATEWAY_IMG} .

kind-load-tool-gateway: docker-build-tool-gateway ## Build and load tool-gateway image into kind.
	kind load docker-image $(TOOL_GATEWAY_IMG) --name $(KIND_CLUSTER_NAME)

.PHONY: docker-build-fs-gateway kind-load-fs-gateway
FS_GATEWAY_IMG ?= ghcr.io/grantbarry29/scrutineer-fs-gateway:latest

docker-build-fs-gateway: ## Build the fs-gateway sidecar image.
	$(CONTAINER_TOOL) build -f Dockerfile.fs-gateway -t ${FS_GATEWAY_IMG} .

kind-load-fs-gateway: docker-build-fs-gateway ## Build and load fs-gateway image into kind.
	kind load docker-image $(FS_GATEWAY_IMG) --name $(KIND_CLUSTER_NAME)

.PHONY: docker-build-egress-reporter kind-load-egress-reporter
EGRESS_REPORTER_IMG ?= ghcr.io/grantbarry29/scrutineer-egress-reporter:latest

docker-build-egress-reporter: ## Build the egress-reporter image (runs beside Envoy in the egress-proxy pod).
	$(CONTAINER_TOOL) build -f Dockerfile.egress-reporter -t ${EGRESS_REPORTER_IMG} .

kind-load-egress-reporter: docker-build-egress-reporter ## Build and load egress-reporter image into kind.
	kind load docker-image $(EGRESS_REPORTER_IMG) --name $(KIND_CLUSTER_NAME)

.PHONY: kind-load-envoy
# The per-session egress proxy uses the upstream Envoy image (no first-party build);
# keep this tag in sync with envoy.DefaultEnvoyImage.
ENVOY_IMG ?= envoyproxy/envoy:distroless-v1.31-latest

kind-load-envoy: ## Pull the upstream Envoy egress-proxy image and load it into kind.
	$(CONTAINER_TOOL) pull $(ENVOY_IMG)
	@# `kind load docker-image` uses `ctr import --all-platforms`, which fails on the
	@# multi-arch Envoy manifest when only the host platform was pulled. Import just the
	@# local single-platform image into the (single-node dev) cluster's containerd instead.
	$(CONTAINER_TOOL) save $(ENVOY_IMG) | $(CONTAINER_TOOL) exec -i $(KIND_CLUSTER_NAME)-control-plane ctr --namespace=k8s.io images import -

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
