# Install targets: build the flowc image and deploy it via the Helm chart.
# Intended for local development — production deploys push to a registry and
# run helm upgrade --install against the real cluster separately.

IMAGE_REPO    ?= flowc
IMAGE_TAG     ?= dev
RELEASE       ?= flowc
NAMESPACE     ?= flowc-system
KIND_CLUSTER  ?= flowc
CHART_PATH    ?= install/helm/flowc
HELM_TIMEOUT  ?= 120s

##@ Install

.PHONY: image
image: ## Build the flowc container image ($(IMAGE_REPO):$(IMAGE_TAG))
	@echo "Building image $(IMAGE_REPO):$(IMAGE_TAG)..."
	$(CONTAINER_TOOL) build -t $(IMAGE_REPO):$(IMAGE_TAG) -f build/flowc/Dockerfile .

.PHONY: image.load
image.load: ## Load the flowc image into the kind cluster ($(KIND_CLUSTER))
	@echo "Loading $(IMAGE_REPO):$(IMAGE_TAG) into kind cluster $(KIND_CLUSTER)..."
	kind load docker-image $(IMAGE_REPO):$(IMAGE_TAG) --name $(KIND_CLUSTER)

.PHONY: helm.install
helm.install: ## helm upgrade --install the flowc chart against the current kube-context
	@echo "Installing/upgrading release $(RELEASE) in $(NAMESPACE)..."
	helm upgrade --install $(RELEASE) $(CHART_PATH) \
		--namespace $(NAMESPACE) --create-namespace \
		--set image.repository=$(IMAGE_REPO) \
		--set image.tag=$(IMAGE_TAG) \
		--set image.pullPolicy=IfNotPresent \
		--wait --timeout $(HELM_TIMEOUT)

.PHONY: helm.uninstall
helm.uninstall: ## Uninstall the flowc Helm release
	-helm uninstall $(RELEASE) -n $(NAMESPACE)

.PHONY: kind.up
kind.up: ## Create the kind cluster ($(KIND_CLUSTER)) if it doesn't exist
	@if kind get clusters 2>/dev/null | grep -q "^$(KIND_CLUSTER)$$"; then \
		echo "kind cluster $(KIND_CLUSTER) already exists"; \
	else \
		echo "Creating kind cluster $(KIND_CLUSTER)..."; \
		kind create cluster --name $(KIND_CLUSTER); \
	fi

.PHONY: kind.down
kind.down: ## Delete the kind cluster
	kind delete cluster --name $(KIND_CLUSTER)

.PHONY: setup
setup: image _setup.maybe-load helm.install ## Build flowc and install on the current kube-context (auto-loads into kind if context is kind-*)

# If the current kubectl context is a kind cluster, load the freshly built image into it.
# Otherwise skip — on a real cluster the user must push the image themselves.
.PHONY: _setup.maybe-load
_setup.maybe-load:
	@ctx=$$(kubectl config current-context 2>/dev/null || true); \
	case "$$ctx" in \
	  kind-*) \
	    cluster=$${ctx#kind-}; \
	    echo "Detected kind context $$ctx — loading $(IMAGE_REPO):$(IMAGE_TAG) into cluster $$cluster..."; \
	    kind load docker-image $(IMAGE_REPO):$(IMAGE_TAG) --name "$$cluster" ;; \
	  *) \
	    echo "Context $$ctx is not kind — skipping image load. Push $(IMAGE_REPO):$(IMAGE_TAG) to a registry the cluster can reach." ;; \
	esac

.PHONY: setup.kind
setup.kind: kind.up image image.load helm.install ## Create kind cluster, build image, load it, and install flowc

.PHONY: setup.clean
setup.clean: helm.uninstall ## Uninstall the release and delete the namespace
	-kubectl delete ns $(NAMESPACE) --ignore-not-found

.PHONY: setup.kind.clean
setup.kind.clean: kind.down ## Delete the kind cluster
