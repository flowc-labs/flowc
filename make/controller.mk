# K8s Controller targets
# CRD generation, controller build, K8s deployment

CONTROLLER_BINARY = flowc-controller
CONTROLLER_CMD = ./cmd/flowc-controller
CONTROLLER_IMG ?= flowc-controller:latest

## Tool Binaries
KUBECTL ?= kubectl
KIND ?= kind
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint

## Tool Versions
KUSTOMIZE_VERSION ?= v5.8.1
CONTROLLER_TOOLS_VERSION ?= v0.20.1
GOLANGCI_LINT_VERSION ?= v2.11.4

ENVTEST_VERSION ?= $(shell v='$(call gomodver,sigs.k8s.io/controller-runtime)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_VERSION manually" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?([0-9]+)\.([0-9]+).*/release-\1.\2/')

ENVTEST_K8S_VERSION ?= $(shell v='$(call gomodver,k8s.io/api)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_K8S_VERSION manually" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

##@ K8s Controller - Code Generation

.PHONY: generate
generate: controller-gen ## Generate DeepCopy methods
	"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: manifests
manifests: controller-gen ## Generate CRD manifests
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

##@ K8s Controller - Build

.PHONY: controller-build
controller-build: generate fmt vet ## Build the K8s controller binary
	$(GOBUILD) -o bin/$(CONTROLLER_BINARY) $(CONTROLLER_CMD)

.PHONY: controller-run
controller-run: generate fmt vet ## Run the K8s controller locally
	go run $(CONTROLLER_CMD)

.PHONY: controller-docker-build
controller-docker-build: ## Build controller Docker image
	$(CONTAINER_TOOL) build -t ${CONTROLLER_IMG} -f build/controller/Dockerfile .

.PHONY: controller-docker-push
controller-docker-push: ## Push controller Docker image
	$(CONTAINER_TOOL) push ${CONTROLLER_IMG}

##@ K8s Controller - Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install-crds
install-crds: manifests kustomize ## Install CRDs into K8s cluster
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" apply -f -; else echo "No CRDs to install; skipping."; fi

.PHONY: uninstall-crds
uninstall-crds: manifests kustomize ## Uninstall CRDs from K8s cluster
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -; else echo "No CRDs to delete; skipping."; fi

.PHONY: deploy-controller
deploy-controller: manifests kustomize ## Deploy controller to K8s cluster
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${CONTROLLER_IMG}
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" apply -f -

.PHONY: undeploy-controller
undeploy-controller: kustomize ## Undeploy controller from K8s cluster
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: build-installer
build-installer: manifests generate kustomize ## Generate consolidated install YAML
	mkdir -p dist
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${CONTROLLER_IMG}
	"$(KUSTOMIZE)" build config/default > dist/install.yaml

##@ K8s Controller - Lint

# Custom golangci-lint binary built from .custom-gcl.yml (includes module plugins like logcheck).
CUSTOM_GCL = $(LOCALBIN)/custom-gcl

.PHONY: custom-gcl
custom-gcl: $(CUSTOM_GCL) ## Build custom golangci-lint binary with module plugins from .custom-gcl.yml
$(CUSTOM_GCL): $(GOLANGCI_LINT) .custom-gcl.yml
	"$(GOLANGCI_LINT)" custom

.PHONY: lint
lint: custom-gcl ## Run golangci-lint (uses custom binary with module plugins)
	"$(CUSTOM_GCL)" run

.PHONY: lint-fix
lint-fix: custom-gcl ## Run golangci-lint with fixes
	"$(CUSTOM_GCL)" run --fix

.PHONY: lint-config
lint-config: custom-gcl ## Validate golangci-lint config
	"$(CUSTOM_GCL)" config verify

##@ Verification

.PHONY: verify-codegen
verify-codegen: generate manifests ## Verify generated CRDs and DeepCopy are up-to-date
	@if git --no-pager diff --quiet -- api/v1alpha1/zz_generated.deepcopy.go config/crd/; then \
		echo "Codegen is up-to-date."; \
	else \
		echo "ERROR: Generated files are out of date. Run 'make generate manifests' and commit."; \
		git --no-pager diff --stat -- api/v1alpha1/zz_generated.deepcopy.go config/crd/; \
		exit 1; \
	fi

##@ K8s Controller - E2E Tests

KIND_CLUSTER ?= flowc-test-e2e

.PHONY: setup-test-e2e
setup-test-e2e: ## Set up Kind cluster for e2e tests
	@command -v $(KIND) >/dev/null 2>&1 || { echo "Kind is not installed."; exit 1; }
	@case "$$($(KIND) get clusters)" in \
		*"$(KIND_CLUSTER)"*) echo "Kind cluster '$(KIND_CLUSTER)' already exists." ;; \
		*) echo "Creating Kind cluster '$(KIND_CLUSTER)'..."; $(KIND) create cluster --name $(KIND_CLUSTER) ;; \
	esac

.PHONY: test-e2e
test-e2e: setup-test-e2e manifests generate fmt vet ## Run e2e tests
	KIND=$(KIND) KIND_CLUSTER=$(KIND_CLUSTER) go test -tags=e2e ./test/e2e/ -v -ginkgo.v
	$(MAKE) cleanup-test-e2e

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down e2e Kind cluster
	@$(KIND) delete cluster --name $(KIND_CLUSTER)

##@ K8s Controller - Tool Dependencies

.PHONY: kustomize
kustomize: $(KUSTOMIZE)
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN)
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest
	@"$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path || exit 1

.PHONY: envtest
envtest: $(ENVTEST)
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT)
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))
