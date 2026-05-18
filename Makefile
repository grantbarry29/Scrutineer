# Image URL to use all building/pushing image targets
IMG ?= ghcr.io/secureai/relay:latest

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
	# allowDangerousTypes=true so that float64 fields (e.g. Model.Temperature)
	# get a proper "type: number" entry in the OpenAPI schema. Without it
	# controller-gen drops the type, producing a CRD the apiserver rejects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role "crd:allowDangerousTypes=true" webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test ./... -coverprofile cover.out

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

KIND_CLUSTER_NAME ?= relay-dev
KIND_CONFIG ?= .devcontainer/kind-config.yaml

.PHONY: kind-up
kind-up: ## Create the local kind cluster if it does not exist.
	@# Inside the dev container we use kind-up.sh, which is resilient to
	@# kind v0.31.0's flaky "remove control-plane taint" step (it manually
	@# installs the CNI and removes the taint if kind aborted early) and
	@# also wires the dev container onto the kind docker network.
	@if [ "$${RELAY_DEVCONTAINER:-0}" = "1" ] && [ -x .devcontainer/kind-up.sh ]; then \
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

.PHONY: kind-load
kind-load: docker-build ## Build the controller image and load it into kind.
	kind load docker-image $(IMG) --name $(KIND_CLUSTER_NAME)

.PHONY: dev-up
dev-up: kind-up install ## Bring up kind + install CRDs (controller runs locally via `make run`).
	@echo ""
	@echo "Cluster $(KIND_CLUSTER_NAME) is up and Relay CRDs are installed."
	@echo "Run 'make run' in another terminal to start the controller against this cluster,"
	@echo "then 'kubectl apply -f config/samples/' to try the sample AgentSessions."

.PHONY: dev-deploy
dev-deploy: kind-up kind-load deploy ## Build, load, and deploy the controller into the kind cluster.
	@kubectl -n relay-system rollout status deployment/relay-controller-manager --timeout=2m
	@echo "Relay controller is running in-cluster."

.PHONY: dev-sample
dev-sample: ## Apply both sample AgentSessions to the kind cluster.
	kubectl apply -f config/samples/relay_v1alpha1_agentsession.yaml
	kubectl apply -f config/samples/relay_v1alpha1_agentsession_failing.yaml

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
	test -s $(LOCALBIN)/setup-envtest || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
