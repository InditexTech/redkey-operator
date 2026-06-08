# SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
#
# SPDX-License-Identifier: Apache-2.0

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

# NAME defines the name of the operator, which is also used as the default name for the operator, bundle and catalog images.
NAME := redkey-operator

# VERSION defines the project version for the bundle.
# Update this value when you upgrade the version of your project.
# To re-generate a bundle for another specific version without changing the standard setup, you can:
# - use the VERSION as arg of the bundle target (e.g make bundle VERSION=0.0.2)
# - use environment variables to overwrite this value (e.g export VERSION=0.0.2)
VERSION ?= 0.2.0

# ROBIN_VERSION defines the version of the Robin image used in E2E tests.
ROBIN_VERSION ?= 0.2.0

## Tool Versions and Configuration

# GOLANG_VERSION defines the Go version used in the Dockerfile for building the manager image.
GOLANG_VERSION := 1.26.4

# KUSTOMIZE_VERSION defines the version of kustomize to use for generating the install manifests and bundle manifests.
KUSTOMIZE_VERSION ?= v5.6.0

# Set the Operator SDK version to use. By default, what is installed on the system is used.
# This is useful for CI or a project to utilize a specific version of the operator-sdk toolkit.
OPERATOR_SDK_VERSION ?= v1.42.2

# CONTROLLER_TOOLS_VERSION defines the version of controller-gen to use for generating code and manifests.
CONTROLLER_TOOLS_VERSION ?= v0.18.0

# KIND_VERSION defines the version of kind to use for local testing.
KIND_VERSION ?= v0.22.0

# Openshift platform supported version.
# Required to publish the operator in the Red Hat Marketplace.
# This value is used to set the com.redhat.openshift.versions label in the bundle and catalog images,
# which indicates the OpenShift versions that are supported by the operator.
OPENSHIFT_VERSION="v4.11"

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif


## Channels and Catalog configuration

CHANNELS ?= alpha,beta,stable

# CHANNELS define the bundle channels used in the bundle.
# Add a new line here if you would like to change its default config. (E.g CHANNELS = "candidate,fast,stable")
# To re-generate a bundle for other specific channels without changing the standard setup, you can:
# - use the CHANNELS as arg of the bundle target (e.g make bundle CHANNELS=candidate,fast,stable)
# - use environment variables to overwrite this value (e.g export CHANNELS="candidate,fast,stable")
ifneq ($(origin CHANNELS), undefined)
BUNDLE_CHANNELS := --channels=$(CHANNELS)
endif

# DEFAULT_CHANNEL defines the default channel used in the bundle.
# Add a new line here if you would like to change its default config. (E.g DEFAULT_CHANNEL = "stable")
# To re-generate a bundle for any other default channel without changing the default setup, you can:
# - use the DEFAULT_CHANNEL as arg of the bundle target (e.g make bundle DEFAULT_CHANNEL=stable)
# - use environment variables to overwrite this value (e.g export DEFAULT_CHANNEL="stable")
ifneq ($(origin DEFAULT_CHANNEL), undefined)
BUNDLE_DEFAULT_CHANNEL := --default-channel=$(DEFAULT_CHANNEL)
endif
BUNDLE_METADATA_OPTS ?= $(BUNDLE_CHANNELS) $(BUNDLE_DEFAULT_CHANNEL)


## Images configuration

# REGISTRY_NAME defines the name of the local registry container used for testing with Kind.
# This value is used in the Makefile to check if the registry container is running and to configure the
# local registry in the Kind cluster.
REGISTRY_NAME ?= $(NAME)-kind-registry-dev

# REGISTRY_PORT defines the port of the local registry used for testing with Kind. This value is used
# in the Makefile to construct the image tags for the bundle and catalog images, and to configure the
# local registry in the Kind cluster.
REGISTRY_PORT ?= 5005

# IMAGE_TAG_BASE defines the docker.io namespace and part of the image name for remote images.
# This variable is used to construct full image tags for bundle and catalog images.
#
# For example, running 'make bundle-build bundle-push catalog-build catalog-push' will build and push both
# inditex.dev/redkeyoperator-bundle:$VERSION and inditex.dev/redkeyoperator-catalog:$VERSION.
IMAGE_TAG_BASE ?= localhost:$(REGISTRY_PORT)/$(NAME)

# Image URL to use all building/pushing image targets
IMG ?= $(IMAGE_TAG_BASE):$(VERSION)

# Robin image URL.
IMG_ROBIN ?= localhost:$(REGISTRY_PORT)/redkey-robin:$(ROBIN_VERSION)

# BUNDLE_IMG defines the image:tag used for the bundle.
# You can use it as an arg. (E.g make bundle-build BUNDLE_IMG=<some-registry>/<project-name-bundle>:<tag>)
BUNDLE_IMG ?= $(IMAGE_TAG_BASE)-bundle:v$(VERSION)

# BUNDLE_GEN_FLAGS are the flags passed to the operator-sdk generate bundle command
BUNDLE_GEN_FLAGS ?= -q --overwrite --version $(VERSION) $(BUNDLE_METADATA_OPTS)

# USE_IMAGE_DIGESTS defines if images are resolved via tags or digests
# You can enable this value if you would like to use SHA Based Digests
# To enable set flag to true
USE_IMAGE_DIGESTS ?= false
ifeq ($(USE_IMAGE_DIGESTS), true)
	BUNDLE_GEN_FLAGS += --use-image-digests
endif


.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)


##@ CI

.PHONY: verify
verify: tidy fmt vet lint build test-all ## Run all verification steps (fmt, vet, lint, unit and integration tests).
	@echo "All verification checks passed successfully!"

.PHONY: version
version:: ## Print the current version of the project.
	@echo "$(VERSION)"

.PHONY: version-next
version-next:: ## Bump to next development version
	@echo "Bumping to next development version"
	sed -ri 's/(.*)(VERSION\s*:=\s*)([0-9]+)\.([0-9]+)\.([0-9]+)(.*)/echo "\1\2\3.$$((\4+1)).0-SNAPSHOT\6"/ge' Makefile

.PHONY: version-set
version-set:: ## Set the project version to the given version, using the NEW_VERSION environment variable
	@echo "Setting version to $(NEW_VERSION)"
	sed -ri 's/(.*)(VERSION\s*:=\s*)([0-9]+\.[0-9]+\.[0-9]+)(-SNAPSHOT)(.*)/echo "\1\2$(NEW_VERSION)\5"/ge' Makefile


##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: tidy
tidy: ##	Run go mod tidy
	go mod tidy

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter
	$(GOLANGCI_LINT) run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	$(GOLANGCI_LINT) run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	$(GOLANGCI_LINT) config verify

# We use count=1 to disable test caching and force the tests to run every time.
.PHONY: test
test: manifests generate fmt vet ## Run unit tests.
	go test $$(go list ./... | grep -v /e2e | grep -v /test/integration) -coverprofile cover.out -count=1

.PHONY: coverage
coverage: test ## HTML coverage from unit tests only.
	go tool cover -html=cover.out -o coverage.html

# We use count=1 to disable test caching and force the tests to run every time.
.PHONY: test-integration
test-integration: manifests generate fmt vet setup-envtest ## Run integration tests (envtest).
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test ./test/integration/ -v -count=1

.PHONY: test-all
test-all: test test-integration ## Run all tests (unit + integration).

# CertManager is installed by default; skip with:
# - CERT_MANAGER_INSTALL_SKIP=true
KIND_CLUSTER ?= $(NAME)-dev

.PHONY: setup-kind
setup-kind: kind ## Set up a Kind cluster for local tests if it does not exist
	@registry_created=false; \
	if [ "$(KIND_CLUSTER)" = "$(NAME)-dev" ]; then \
		if [ "$$($(CONTAINER_TOOL) inspect -f '{{.State.Running}}' $(REGISTRY_NAME) 2>/dev/null || echo false)" != "true" ]; then \
			echo "Starting local registry $(REGISTRY_NAME) on port $(REGISTRY_PORT)..."; \
			$(CONTAINER_TOOL) run -d --restart=always -p "$(REGISTRY_PORT):5000" --name $(REGISTRY_NAME) registry:2; \
			registry_created=true; \
		else \
			echo "Local registry $(REGISTRY_NAME) is already running."; \
		fi; \
	fi; \
	if echo "$$($(KIND) get clusters)" | grep -qx "$(KIND_CLUSTER)"; then \
		echo "Kind cluster '$(KIND_CLUSTER)' already exists. Skipping creation."; \
	else \
		echo "Creating Kind cluster '$(KIND_CLUSTER)'..."; \
		$(KIND) create cluster --name $(KIND_CLUSTER) --config test/config/kind-config.yaml; \
		if [ "$$registry_created" = "true" ]; then \
			echo "Connecting registry to cluster nodes..."; \
			for node in $$($(KIND) get nodes --name $(KIND_CLUSTER)); do \
				$(CONTAINER_TOOL) exec "$$node" mkdir -p "/etc/containerd/certs.d/localhost:$(REGISTRY_PORT)"; \
				echo "[host.\"http://$(REGISTRY_NAME):5000\"]" | $(CONTAINER_TOOL) exec -i "$$node" cp /dev/stdin "/etc/containerd/certs.d/localhost:$(REGISTRY_PORT)/hosts.toml"; \
			done; \
			echo "Registry configured successfully in kind cluster."; \
			if [ "$$($(CONTAINER_TOOL) inspect -f '{{json .NetworkSettings.Networks.kind}}' $(REGISTRY_NAME))" = 'null' ]; then \
				echo "Connecting local registry $(REGISTRY_NAME) to 'kind' network..."; \
				$(CONTAINER_TOOL) network connect "kind" $(REGISTRY_NAME); \
			fi; \
		fi; \
	fi

.PHONY: cleanup-kind
cleanup-kind: kind ## Tear down the Kind cluster used for local tests
	@if [ "$(KIND_CLUSTER)" = "$(NAME)-dev" ]; then \
		$(CONTAINER_TOOL) rm -f $(REGISTRY_NAME) || true; \
	fi
	@$(KIND) delete cluster --name $(KIND_CLUSTER)

.PHONY: kind-load
kind-load: kind docker-build ## Load the docker image into the Kind cluster
	$(KIND) load docker-image ${IMG} --name $(KIND_CLUSTER)

# By default, e2e tests are run in an isolated Kind cluster named $(NAME)-test-e2e. You can change this value by:
# - setting the KIND_CLUSTER_E2E environment variable (e.g. export KIND_CLUSTER_E2E=my-e2e-cluster)
# - passing it as an arg to the test-e2e target (e.g. make test-e2e KIND_CLUSTER_E2E=my-e2e-cluster)
KIND_CLUSTER_E2E ?= $(NAME)-test-e2e

# IMAGE_OPERATOR specifies the prebuilt operator image used for E2E tests.
IMAGE_OPERATOR ?= $(IMG)

# IMAGE_ROBIN specifies the prebuilt Robin image used for E2E tests.
IMAGE_ROBIN ?= $(IMG_ROBIN)

# TEST_PARALLEL_PROCESS defines the number of parallel Ginkgo processes for E2E tests.
# GitHub runners (ubuntu-24.04) have 4 vCPUs and 16GB RAM. Default: 1 (serial) for local dev.
# Recommended: 2 for CI runners. Set to 1 to disable parallelism.
# Parallel execution requires the ginkgo CLI (installed via `go install github.com/onsi/ginkgo/v2/ginkgo`).
TEST_PARALLEL_PROCESS ?= 1

# E2E tuning variables (passed as env vars to the test binary).
# E2E_RECONCILE_INTERVAL: Robin reconcile loop interval in seconds (applies to normal, error and wait intervals).
#   Controls how frequently Robin checks and reconciles the Redis cluster state. Lower = faster convergence.
# E2E_POLL_INTERVAL: Polling interval in seconds for test wait helpers (WaitForClusterReady, WaitForClusterPhase, etc.).
#   Controls how often the test framework checks if a condition is met.
# E2E_CREATION_TIMEOUT: Maximum seconds to wait for a cluster to reach Ready state after creation.
#   Increase if tests timeout during cluster creation on slow environments.
# E2E_HEALTH_TIMEOUT: Maximum seconds to wait for health-related operations (remediation, node recovery).
#   Increase for tests that involve pod restarts or network disruptions.
# E2E_UPGRADE_TIMEOUT: Maximum seconds to wait for a full rolling N+1 upgrade to complete.
#   Increase for heavy topologies (e.g. multiple replicas per primary) on slow runners.
E2E_RECONCILE_INTERVAL ?=
E2E_POLL_INTERVAL ?=
E2E_CREATION_TIMEOUT ?=
E2E_HEALTH_TIMEOUT ?=
E2E_UPGRADE_TIMEOUT ?=

# E2E_ENV collects all E2E_* env vars that are set, to forward them to the test process.
E2E_ENV = KIND_CLUSTER=$(KIND_CLUSTER_E2E) IMAGE_OPERATOR=$(IMAGE_OPERATOR) IMAGE_ROBIN=$(IMAGE_ROBIN)
E2E_ENV += $(if $(E2E_RECONCILE_INTERVAL),E2E_RECONCILE_INTERVAL=$(E2E_RECONCILE_INTERVAL),)
E2E_ENV += $(if $(E2E_POLL_INTERVAL),E2E_POLL_INTERVAL=$(E2E_POLL_INTERVAL),)
E2E_ENV += $(if $(E2E_CREATION_TIMEOUT),E2E_CREATION_TIMEOUT=$(E2E_CREATION_TIMEOUT),)
E2E_ENV += $(if $(E2E_HEALTH_TIMEOUT),E2E_HEALTH_TIMEOUT=$(E2E_HEALTH_TIMEOUT),)
E2E_ENV += $(if $(E2E_UPGRADE_TIMEOUT),E2E_UPGRADE_TIMEOUT=$(E2E_UPGRADE_TIMEOUT),)

.PHONY: setup-test-e2e
setup-test-e2e: ## Set up the Kind cluster used by E2E tests.
	$(MAKE) setup-kind KIND_CLUSTER=$(KIND_CLUSTER_E2E)

.PHONY: test-e2e
test-e2e: manifests generate fmt vet setup-test-e2e kind ## Run E2E tests against prebuilt images loaded into Kind.
	$(KIND) load docker-image $(IMAGE_OPERATOR) --name $(KIND_CLUSTER_E2E)
	$(KIND) load docker-image $(IMAGE_ROBIN) --name $(KIND_CLUSTER_E2E)
ifeq ($(TEST_PARALLEL_PROCESS),1)
	$(E2E_ENV) go test ./test/e2e/ -v -ginkgo.v \
		$(if $(LABEL),-ginkgo.label-filter='$(LABEL)',) \
		-timeout 90m
else
	$(E2E_ENV) go run github.com/onsi/ginkgo/v2/ginkgo \
		-v -procs=$(TEST_PARALLEL_PROCESS) \
		$(if $(LABEL),--label-filter='$(LABEL)',) \
		--timeout=90m \
		./test/e2e/
endif

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down the Kind cluster used for e2e tests
	$(MAKE) cleanup-kind KIND_CLUSTER=$(KIND_CLUSTER_E2E)

.PHONY: clean
clean: ## Clean de build artifacts, installed tools, Go cache and generated files.
	chmod -R u+w $(LOCALBIN) 2>/dev/null || true
	rm -rf bin
	rm -rf dist
	rm -rf $(LOCALBIN)
	rm -rf cover.out coverage.html
	rm -rf bundle
	rm -rf bundle_*
	rm -rf bundle-*
	rm  -rf bundle.Dockerfile
	go clean --modcache


##@ Build

.PHONY: build
build: manifests generate fmt vet test-all ## Build manager binary.
	go build -o bin/manager cmd/main.go

# By default, the manager will be run with info log level. To run the manager with debug log level, set the LOG_DEBUG variable to true:
# - make run LOG_DEBUG=true
LOG_DEBUG ?= false

.PHONY: run
run: manifests generate fmt vet test ## Run a controller from your host.
	go run ./cmd/main.go $(if $(filter true,$(LOG_DEBUG)),--zap-log-level=debug)

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: test-all ## Build docker image with the manager.
	$(CONTAINER_TOOL) build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

# PLATFORMS defines the target platforms for the manager image be built to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - be able to use docker buildx. More info: https://docs.docker.com/build/buildx/
# - have enabled BuildKit. More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image to your registry (i.e. if you do not set a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
# To adequately provide solutions that are compatible with multiple platforms, you should consider using this option.
PLATFORMS ?= linux/amd64,linux/arm64
.PHONY: docker-buildx
docker-buildx: test-all ## Build and push docker image for the manager for cross-platform support
	- $(CONTAINER_TOOL) buildx create --name redkeyoperator-builder
	$(CONTAINER_TOOL) buildx use redkeyoperator-builder
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) --tag ${IMG} .
	- $(CONTAINER_TOOL) buildx rm redkeyoperator-builder

.PHONY: build-installer
build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default > dist/install.yaml


##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) apply --server-side -f -

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | $(KUBECTL) apply --server-side -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/default | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: deploy-sample-ephemeral
deploy-sample-ephemeral: kustomize ## Deploy ephemeral sample cluster (3 primaries, no persistence).
	$(KUSTOMIZE) build config/samples/ephemeral | sed 's|image: ghcr.io/inditextech/redkey-robin:latest|image: $(IMG_ROBIN)|g' | $(KUBECTL) apply --server-side -f -

.PHONY: undeploy-sample-ephemeral
undeploy-sample-ephemeral: kustomize ## Undeploy ephemeral sample cluster.
	$(KUSTOMIZE) build config/samples/ephemeral | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: deploy-sample-storage
deploy-sample-storage: kustomize ## Deploy storage sample cluster (3 primaries, persistent storage with AOF).
	$(KUSTOMIZE) build config/samples/storage | sed 's|image: ghcr.io/inditextech/redkey-robin:latest|image: $(IMG_ROBIN)|g' | $(KUBECTL) apply --server-side -f -

.PHONY: undeploy-sample-storage
undeploy-sample-storage: kustomize ## Undeploy storage sample cluster.
	$(KUSTOMIZE) build config/samples/storage | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: deploy-sample-replicas
deploy-sample-replicas: kustomize ## Deploy replicas sample cluster (3 primaries + 1 replica each).
	$(KUSTOMIZE) build config/samples/replicas | sed 's|image: ghcr.io/inditextech/redkey-robin:latest|image: $(IMG_ROBIN)|g' | $(KUBECTL) apply --server-side -f -

.PHONY: undeploy-sample-replicas
undeploy-sample-replicas: kustomize ## Undeploy replicas sample cluster.
	$(KUSTOMIZE) build config/samples/replicas | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: deploy-sample-auth
deploy-sample-auth: kustomize ## Deploy auth sample cluster (3 primaries with Redis authentication).
	$(KUSTOMIZE) build config/samples/auth | sed 's|image: ghcr.io/inditextech/redkey-robin:latest|image: $(IMG_ROBIN)|g' | $(KUBECTL) apply --server-side -f -

.PHONY: undeploy-sample-auth
undeploy-sample-auth: kustomize ## Undeploy auth sample cluster.
	$(KUSTOMIZE) build config/samples/auth | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -


##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUBECTL ?= kubectl
KIND ?= $(LOCALBIN)/kind
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint

## Tool Versions
#ENVTEST_VERSION is the version of controller-runtime release branch to fetch the envtest setup script (i.e. release-0.20)
ENVTEST_VERSION ?= $(shell go list -m -f "{{ .Version }}" sigs.k8s.io/controller-runtime | awk -F'[v.]' '{printf "release-%d.%d", $$2, $$3}')
#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell go list -m -f "{{ .Version }}" k8s.io/api | awk -F'[v.]' '{printf "1.%d", $$3}')
GOLANGCI_LINT_VERSION ?= v2.1.0

.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@$(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: kind
kind: $(KIND) ## Download kind locally if necessary.
$(KIND): $(LOCALBIN)
	$(call go-install-tool,$(KIND),sigs.k8s.io/kind,$(KIND_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f $(1) || true ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
mv $(1) $(1)-$(3) ;\
} ;\
ln -sf $(1)-$(3) $(1)
endef

.PHONY: operator-sdk
OPERATOR_SDK ?= $(LOCALBIN)/operator-sdk
operator-sdk: ## Download operator-sdk locally if necessary.
ifeq (,$(wildcard $(OPERATOR_SDK)))
ifeq (, $(shell which operator-sdk 2>/dev/null))
	@{ \
	set -e ;\
	mkdir -p $(dir $(OPERATOR_SDK)) ;\
	OS=$(shell go env GOOS) && ARCH=$(shell go env GOARCH) && \
	curl -sSLo $(OPERATOR_SDK) https://github.com/operator-framework/operator-sdk/releases/download/$(OPERATOR_SDK_VERSION)/operator-sdk_$${OS}_$${ARCH} ;\
	chmod +x $(OPERATOR_SDK) ;\
	}
else
OPERATOR_SDK = $(shell which operator-sdk)
endif
endif

.PHONY: opm
OPM = $(LOCALBIN)/opm
opm: ## Download opm locally if necessary.
ifeq (,$(wildcard $(OPM)))
ifeq (,$(shell which opm 2>/dev/null))
	@{ \
	set -e ;\
	mkdir -p $(dir $(OPM)) ;\
	OS=$(shell go env GOOS) && ARCH=$(shell go env GOARCH) && \
	curl -sSLo $(OPM) https://github.com/operator-framework/operator-registry/releases/download/v1.55.0/$${OS}-$${ARCH}-opm ;\
	chmod +x $(OPM) ;\
	}
else
OPM = $(shell which opm)
endif
endif


##@ Bundle and Catalog

.PHONY: bundle
bundle: manifests kustomize operator-sdk ## Generate bundle manifests and metadata, then validate generated files.
	$(OPERATOR_SDK) generate kustomize manifests -q
	cd config/manager && $(KUSTOMIZE) edit set image controller=$(IMG)
	$(KUSTOMIZE) build config/manifests | $(OPERATOR_SDK) generate bundle $(BUNDLE_GEN_FLAGS)
	$(OPERATOR_SDK) bundle validate ./bundle
	@chmod -R a+rx ./bundle		# Ensure read and execute permissions on the bundle directory for all users, which is required for OLM to read the files.
	@sed -i "/^FROM.*/a LABEL com.redhat.openshift.versions="$(OPENSHIFT_VERSION)"" bundle.Dockerfile; \
	sed -i "/^FROM.*/a LABEL com.redhat.delivery.operator.bundle=true" bundle.Dockerfile; \
	sed -i "/^FROM.*/a LABEL com.redhat.delivery.backport=false" bundle.Dockerfile; \
	sed -i "/^FROM.*/a # Labels for RedHat Openshift Platform" bundle.Dockerfile
	@sed -i "/^annotations.*/a \  com.redhat.openshift.versions: "$(OPENSHIFT_VERSION)"" bundle/metadata/annotations.yaml; \
	sed -i "/^annotations.*/a \  # Annotations for RedHat Openshift Platform" bundle/metadata/annotations.yaml

.PHONY: bundle-build
bundle-build: ## Build the bundle image.
	$(CONTAINER_TOOL) build -f bundle.Dockerfile -t $(BUNDLE_IMG) .

.PHONY: bundle-push
bundle-push: ## Push the bundle image.
	$(MAKE) docker-push IMG=$(BUNDLE_IMG)

# A comma-separated list of bundle images (e.g. make catalog-build BUNDLE_IMGS=example.com/operator-bundle:v0.1.0,example.com/operator-bundle:v0.2.0).
# These images MUST exist in a registry and be pull-able.
BUNDLE_IMGS ?= $(BUNDLE_IMG)

# The image tag given to the resulting catalog image (e.g. make catalog-build CATALOG_IMG=example.com/operator-catalog:v0.2.0).
CATALOG_IMG ?= $(IMAGE_TAG_BASE)-catalog:v$(VERSION)

# Set CATALOG_BASE_IMG to an existing catalog image tag to add $BUNDLE_IMGS to that image.
ifneq ($(origin CATALOG_BASE_IMG), undefined)
FROM_INDEX_OPT := --from-index $(CATALOG_BASE_IMG)
endif

# Build a catalog image by adding bundle images to an empty catalog using the operator package manager tool, 'opm'.
# This recipe invokes 'opm' in 'semver' bundle add mode. For more information on add modes, see:
# https://github.com/operator-framework/community-operators/blob/7f1438c/docs/packaging-operator.md#updating-your-existing-operator
.PHONY: catalog-build
catalog-build: opm ## Build a catalog image.
	$(OPM) index add --container-tool $(CONTAINER_TOOL) --mode semver --tag $(CATALOG_IMG) --bundles $(BUNDLE_IMGS) $(FROM_INDEX_OPT)

# Push the catalog image.
.PHONY: catalog-push
catalog-push: ## Push a catalog image.
	$(MAKE) docker-push IMG=$(CATALOG_IMG)

.PHONY: olm-install
olm-install: operator-sdk ## Install OLM in the cluster if not already present.
	$(OPERATOR_SDK) olm install

.PHONY: olm-uninstall
olm-uninstall: operator-sdk ## Uninstall OLM from the cluster.
	$(OPERATOR_SDK) olm uninstall

.PHONY: bundle-deploy
bundle-deploy: manifests kustomize operator-sdk ## Deploy the operator from the bundle image to the cluster.
	$(OPERATOR_SDK) run bundle $(BUNDLE_IMG) --install-mode=AllNamespaces --namespace operators --timeout 5m0s --skip-tls

.PHONY: bundle-undeploy
bundle-undeploy: operator-sdk ## Undeploy the operator from the cluster.
	$(OPERATOR_SDK) cleanup $(NAME) --namespace operators


##@ Observability

OBSERVABILITY_NAMESPACE ?= monitoring

.PHONY: observability-install
observability-install: ## Install Prometheus + Grafana observability stack with Redkey dashboards.
	@hack/observability/install-observability.sh $(OBSERVABILITY_NAMESPACE)

.PHONY: observability-uninstall
observability-uninstall: ## Uninstall the observability stack and clean up all resources.
	@hack/observability/uninstall-observability.sh $(OBSERVABILITY_NAMESPACE)
