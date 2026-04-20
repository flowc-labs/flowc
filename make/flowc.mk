# FlowC Control Plane targets
# Build, run, test the flowc binary (REST API + Reconciler + xDS server)

FLOWC_BINARY = flowc
FLOWC_CMD = ./cmd/flowc
FLOWC_LDFLAGS = -ldflags "-s -w"

API_PORT ?= 8080
XDS_PORT ?= 18000

##@ FlowC Control Plane

.PHONY: flowc-build
flowc-build: ## Build the FlowC control plane binary
	@echo "Building $(FLOWC_BINARY)..."
	$(GOBUILD) $(FLOWC_LDFLAGS) -o $(FLOWC_BINARY) $(FLOWC_CMD)
	@echo "Build complete: ./$(FLOWC_BINARY)"

.PHONY: flowc-build-race
flowc-build-race: ## Build with race detector
	$(GOBUILD) -race -o $(FLOWC_BINARY) $(FLOWC_CMD)

.PHONY: flowc-run
flowc-run: flowc-build ## Build and run the FlowC server
	./$(FLOWC_BINARY)

.PHONY: flowc-run-debug
flowc-run-debug: flowc-build ## Run with debug logging
	FLOWC_LOG_LEVEL=debug ./$(FLOWC_BINARY)

.PHONY: flowc-clean
flowc-clean: ## Clean flowc build artifacts
	rm -f $(FLOWC_BINARY)

##@ Testing

.PHONY: test-all
test-all: ## Run all tests
	$(GOTEST) ./...

.PHONY: test-cover-html
test-cover-html: ## Generate HTML coverage report
	$(GOTEST) -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

.PHONY: test-race
test-race: ## Run tests with race detector
	$(GOTEST) -race ./...

.PHONY: test-pkg
test-pkg: ## Test a specific package (PKG=./pkg/bundle/...)
	$(GOTEST) -v $(PKG)

.PHONY: test-run
test-run: ## Run a specific test (TEST=TestName PKG=./pkg/...)
	$(GOTEST) -v -run $(TEST) $(PKG)

##@ Code Quality

.PHONY: fmt
fmt: ## Format code
	$(GOFMT) ./...

.PHONY: vet
vet: ## Vet code
	$(GOVET) ./...

.PHONY: check
check: fmt vet ## Run all code quality checks

.PHONY: tidy
tidy: ## Tidy go modules
	$(GOMOD) tidy

##@ Code Generation

.PHONY: generate-openapi
generate-openapi: ## Generate OpenAPI spec from Go types
	$(GOCMD) run ./cmd/apigen
	@echo "OpenAPI spec generated: api/openapi.yaml"

##@ Envoy

.PHONY: envoy
envoy: ## Run Envoy connected to FlowC
	@cd scripts && ./run-envoy.sh

##@ API Operations

.PHONY: health
health: ## Health check
	curl -s http://localhost:$(API_PORT)/health | jq .
