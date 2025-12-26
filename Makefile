# Makefile for s3-bucket-exporter

# Project variables
BINARY_NAME=s3-bucket-exporter
MAIN_PATH=./cmd/s3-bucket-exporter
DOCKER_REGISTRY=ghcr.io
DOCKER_IMAGE=$(DOCKER_REGISTRY)/tropnikovvl/s3-bucket-exporter
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')

# Build flags
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildTime=$(BUILD_TIME) -w -s"
GO_BUILD=CGO_ENABLED=0 go build $(LDFLAGS)

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt

# Docker parameters
PLATFORMS=linux/amd64,linux/arm64
DOCKER_BUILD_ARGS=--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT)

# Colors for output
RED=\033[0;31m
GREEN=\033[0;32m
YELLOW=\033[0;33m
BLUE=\033[0;34m
NC=\033[0m # No Color

.PHONY: all build clean test test-race test-coverage lint fmt vet help
.PHONY: docker-build docker-push docker-multiarch run install uninstall
.PHONY: deps deps-update tidy security-check helm-lint e2e-test e2e-test-quick
.PHONY: e2e-test-long e2e-test-long-quick release pre-commit version

.DEFAULT_GOAL := help

help: ## Display this help message
	@echo "$(BLUE)s3-bucket-exporter Makefile$(NC)"
	@echo ""
	@echo "$(GREEN)Available targets:$(NC)"
	@awk 'BEGIN {FS = ":.*##"; printf ""} /^[a-zA-Z0-9_-]+:.*##/ { printf "  $(YELLOW)%-20s$(NC) %s\n", $$1, $$2 } /^##@/ { printf "\n$(BLUE)%s$(NC)\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

build: ## Build the binary
build:
	@echo "$(GREEN)Building $(BINARY_NAME)...$(NC)"
	@mkdir -p bin
	$(GO_BUILD) -o bin/$(BINARY_NAME) $(MAIN_PATH)
	@echo "$(GREEN)✓ Build complete: bin/$(BINARY_NAME)$(NC)"

build-all: ## Build binaries for all platforms
build-all:
	@echo "$(GREEN)Building for multiple platforms...$(NC)"
	@mkdir -p bin
	GOOS=linux GOARCH=amd64 $(GO_BUILD) -o bin/$(BINARY_NAME)-linux-amd64 $(MAIN_PATH)
	GOOS=linux GOARCH=arm64 $(GO_BUILD) -o bin/$(BINARY_NAME)-linux-arm64 $(MAIN_PATH)
	GOOS=darwin GOARCH=amd64 $(GO_BUILD) -o bin/$(BINARY_NAME)-darwin-amd64 $(MAIN_PATH)
	GOOS=darwin GOARCH=arm64 $(GO_BUILD) -o bin/$(BINARY_NAME)-darwin-arm64 $(MAIN_PATH)
	GOOS=windows GOARCH=amd64 $(GO_BUILD) -o bin/$(BINARY_NAME)-windows-amd64.exe $(MAIN_PATH)
	@echo "$(GREEN)✓ All builds complete$(NC)"

run: ## Build and run the application
run: build
	@echo "$(GREEN)Running $(BINARY_NAME)...$(NC)"
	./bin/$(BINARY_NAME)

install: ## Install the binary to $GOPATH/bin
install:
	@echo "$(GREEN)Installing $(BINARY_NAME)...$(NC)"
	$(GO_BUILD) -o $(GOPATH)/bin/$(BINARY_NAME) $(MAIN_PATH)
	@echo "$(GREEN)✓ Installed to $(GOPATH)/bin/$(BINARY_NAME)$(NC)"

uninstall: ## Remove the binary from $GOPATH/bin
uninstall:
	@echo "$(YELLOW)Uninstalling $(BINARY_NAME)...$(NC)"
	rm -f $(GOPATH)/bin/$(BINARY_NAME)
	@echo "$(GREEN)✓ Uninstalled$(NC)"

clean: ## Clean build artifacts
clean:
	@echo "$(YELLOW)Cleaning...$(NC)"
	$(GOCLEAN)
	rm -rf bin/
	rm -f coverage.out coverage.html
	rm -rf dist/
	@echo "$(GREEN)✓ Clean complete$(NC)"

##@ Testing

test: ## Run tests
test:
	@echo "$(GREEN)Running tests...$(NC)"
	$(GOTEST) -v -timeout 30s ./...
	@echo "$(GREEN)✓ Tests passed$(NC)"

test-short: ## Run tests in short mode
test-short:
	@echo "$(GREEN)Running short tests...$(NC)"
	$(GOTEST) -short -v ./...

test-race: ## Run tests with race detector
test-race:
	@echo "$(GREEN)Running tests with race detector...$(NC)"
	$(GOTEST) -race -v ./...
	@echo "$(GREEN)✓ Tests passed (no race conditions)$(NC)"

test-coverage: ## Run tests with coverage
test-coverage:
	@echo "$(GREEN)Running tests with coverage...$(NC)"
	$(GOTEST) -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -func=coverage.out
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "$(GREEN)✓ Coverage report generated: coverage.html$(NC)"

test-coverage-report: ## Show detailed coverage report
test-coverage-report: test-coverage
	@echo "$(GREEN)Opening coverage report...$(NC)"
	@which open > /dev/null && open coverage.html || xdg-open coverage.html || echo "Please open coverage.html manually"

e2e-test: ## Run end-to-end tests (rebuilds Docker image)
e2e-test:
	@echo "$(GREEN)Running e2e tests...$(NC)"
	@bash -c '\
		set -e; \
		E2E_DIR="test/e2e"; \
		GREEN="\033[0;32m"; \
		YELLOW="\033[0;33m"; \
		NC="\033[0m"; \
		cleanup() { \
			printf "$${YELLOW}Cleaning up...$${NC}\n"; \
			(cd $$E2E_DIR && docker-compose --profile short down -v) 2>/dev/null || true; \
		}; \
		trap cleanup EXIT; \
		printf "$${YELLOW}Building Docker images...$${NC}\n"; \
		(cd $$E2E_DIR && docker-compose --profile short build --no-cache); \
		printf "$${YELLOW}Running short tests...$${NC}\n"; \
		(cd $$E2E_DIR && docker-compose --profile short up --abort-on-container-exit); \
		printf "$${GREEN}✓ E2E tests passed$${NC}\n"; \
	'

e2e-test-quick: ## Run e2e tests without rebuilding (faster for development)
e2e-test-quick:
	@echo "$(GREEN)Running e2e tests (no rebuild)...$(NC)"
	@bash -c '\
		set -e; \
		E2E_DIR="test/e2e"; \
		GREEN="\033[0;32m"; \
		YELLOW="\033[0;33m"; \
		NC="\033[0m"; \
		cleanup() { \
			printf "$${YELLOW}Cleaning up...$${NC}\n"; \
			(cd $$E2E_DIR && docker-compose --profile short down -v) 2>/dev/null || true; \
		}; \
		trap cleanup EXIT; \
		printf "$${YELLOW}Running short tests...$${NC}\n"; \
		(cd $$E2E_DIR && docker-compose --profile short up --abort-on-container-exit); \
		printf "$${GREEN}✓ E2E tests passed$${NC}\n"; \
	'

e2e-test-long: ## Run long-running e2e test (3 minutes, rebuilds Docker image)
e2e-test-long:
	@echo "$(GREEN)Running long-running e2e test...$(NC)"
	@bash -c '\
		set -e; \
		E2E_DIR="test/e2e"; \
		GREEN="\033[0;32m"; \
		YELLOW="\033[0;33m"; \
		NC="\033[0m"; \
		cleanup() { \
			printf "$${YELLOW}Cleaning up...$${NC}\n"; \
			(cd $$E2E_DIR && docker-compose --profile long down -v) 2>/dev/null || true; \
		}; \
		trap cleanup EXIT; \
		printf "$${YELLOW}Building Docker images...$${NC}\n"; \
		(cd $$E2E_DIR && docker-compose --profile long build --no-cache); \
		printf "$${YELLOW}Running long-running test...$${NC}\n"; \
		(cd $$E2E_DIR && docker-compose --profile long up --abort-on-container-exit); \
		printf "$${GREEN}✓ Long-running e2e test passed$${NC}\n"; \
	'

e2e-test-long-quick: ## Run long-running e2e test without rebuilding (faster for development)
e2e-test-long-quick:
	@echo "$(GREEN)Running long-running e2e test (no rebuild)...$(NC)"
	@bash -c '\
		set -e; \
		E2E_DIR="test/e2e"; \
		GREEN="\033[0;32m"; \
		YELLOW="\033[0;33m"; \
		NC="\033[0m"; \
		cleanup() { \
			printf "$${YELLOW}Cleaning up...$${NC}\n"; \
			(cd $$E2E_DIR && docker-compose --profile long down -v) 2>/dev/null || true; \
		}; \
		trap cleanup EXIT; \
		printf "$${YELLOW}Running long-running test...$${NC}\n"; \
		(cd $$E2E_DIR && docker-compose --profile long up --abort-on-container-exit); \
		printf "$${GREEN}✓ Long-running e2e test passed$${NC}\n"; \
	'

bench: ## Run benchmarks
bench:
	@echo "$(GREEN)Running benchmarks...$(NC)"
	$(GOTEST) -bench=. -benchmem ./...

##@ Code Quality

fmt: ## Format Go code
fmt:
	@echo "$(GREEN)Formatting code...$(NC)"
	$(GOFMT) ./...
	@echo "$(GREEN)✓ Code formatted$(NC)"

vet: ## Run go vet
vet:
	@echo "$(GREEN)Running go vet...$(NC)"
	$(GOCMD) vet ./...
	@echo "$(GREEN)✓ Vet passed$(NC)"

lint: ## Run golangci-lint
lint:
	@echo "$(GREEN)Running linter...$(NC)"
	@which golangci-lint > /dev/null || (echo "$(RED)golangci-lint not installed. Run: make install-tools$(NC)" && exit 1)
	golangci-lint run --timeout 5m
	@echo "$(GREEN)✓ Lint passed$(NC)"

security-check: ## Run security checks with gosec
security-check:
	@echo "$(GREEN)Running security checks...$(NC)"
	@which gosec > /dev/null || (echo "$(RED)gosec not installed. Run: make install-tools$(NC)" && exit 1)
	gosec -no-fail -fmt=json -out=gosec-report.json ./...
	@echo "$(GREEN)✓ Security check complete. Report: gosec-report.json$(NC)"

pre-commit: ## Run all pre-commit checks
pre-commit: fmt vet lint test-race
	@echo "$(GREEN)✓ All pre-commit checks passed$(NC)"

##@ Dependencies

deps: ## Download dependencies
deps:
	@echo "$(GREEN)Downloading dependencies...$(NC)"
	$(GOMOD) download
	@echo "$(GREEN)✓ Dependencies downloaded$(NC)"

deps-update: ## Update dependencies
deps-update:
	@echo "$(GREEN)Updating dependencies...$(NC)"
	$(GOGET) -u ./...
	$(GOMOD) tidy
	@echo "$(GREEN)✓ Dependencies updated$(NC)"

tidy: ## Tidy up dependencies
tidy:
	@echo "$(GREEN)Tidying dependencies...$(NC)"
	$(GOMOD) tidy
	@echo "$(GREEN)✓ Dependencies tidied$(NC)"

verify: ## Verify dependencies
verify:
	@echo "$(GREEN)Verifying dependencies...$(NC)"
	$(GOMOD) verify
	@echo "$(GREEN)✓ Dependencies verified$(NC)"

##@ Docker

docker-build: ## Build Docker image
docker-build:
	@echo "$(GREEN)Building Docker image...$(NC)"
	docker build -t $(DOCKER_IMAGE):$(VERSION) \
		-t $(DOCKER_IMAGE):latest \
		$(DOCKER_BUILD_ARGS) \
		-f Dockerfile .
	@echo "$(GREEN)✓ Docker image built: $(DOCKER_IMAGE):$(VERSION)$(NC)"

docker-build-multiarch: ## Build multi-architecture Docker image
docker-build-multiarch:
	@echo "$(GREEN)Building multi-arch Docker image...$(NC)"
	docker buildx create --use --name multiarch-builder 2>/dev/null || true
	docker buildx build \
		--platform $(PLATFORMS) \
		-t $(DOCKER_IMAGE):$(VERSION) \
		-t $(DOCKER_IMAGE):latest \
		$(DOCKER_BUILD_ARGS) \
		--push \
		-f Dockerfile .
	@echo "$(GREEN)✓ Multi-arch Docker image built and pushed$(NC)"

docker-push: ## Push Docker image to registry
docker-push:
	@echo "$(GREEN)Pushing Docker image...$(NC)"
	docker push $(DOCKER_IMAGE):$(VERSION)
	docker push $(DOCKER_IMAGE):latest
	@echo "$(GREEN)✓ Docker image pushed$(NC)"

docker-run: ## Run Docker container locally
docker-run:
	@echo "$(GREEN)Running Docker container...$(NC)"
	docker run --rm -p 9655:9655 \
		-e S3_ENDPOINT="" \
		-e S3_REGION="us-east-1" \
		-e LOG_LEVEL="info" \
		$(DOCKER_IMAGE):$(VERSION)

docker-compose-up: ## Start services with docker-compose
docker-compose-up:
	@echo "$(GREEN)Starting docker-compose services...$(NC)"
	docker-compose -f deployments/docker-compose/docker-compose.yml up -d
	@echo "$(GREEN)✓ Services started$(NC)"

docker-compose-down: ## Stop services with docker-compose
docker-compose-down:
	@echo "$(YELLOW)Stopping docker-compose services...$(NC)"
	docker-compose -f deployments/docker-compose/docker-compose.yml down
	@echo "$(GREEN)✓ Services stopped$(NC)"

##@ Kubernetes/Helm

helm-lint: ## Lint Helm chart
helm-lint:
	@echo "$(GREEN)Linting Helm chart...$(NC)"
	helm lint deployments/helm/
	@echo "$(GREEN)✓ Helm chart lint passed$(NC)"

helm-template: ## Generate Kubernetes manifests from Helm chart
helm-template:
	@echo "$(GREEN)Generating Helm templates...$(NC)"
	helm template s3-bucket-exporter deployments/helm/ \
		--set image.tag=$(VERSION) \
		--output-dir /tmp/helm-templates
	@echo "$(GREEN)✓ Templates generated in /tmp/helm-templates$(NC)"

helm-install: ## Install Helm chart locally
helm-install:
	@echo "$(GREEN)Installing Helm chart...$(NC)"
	helm upgrade --install s3-bucket-exporter deployments/helm/ \
		--set image.tag=$(VERSION) \
		--create-namespace \
		--namespace monitoring
	@echo "$(GREEN)✓ Helm chart installed$(NC)"

helm-uninstall: ## Uninstall Helm chart
helm-uninstall:
	@echo "$(YELLOW)Uninstalling Helm chart...$(NC)"
	helm uninstall s3-bucket-exporter --namespace monitoring
	@echo "$(GREEN)✓ Helm chart uninstalled$(NC)"

helm-package: ## Package Helm chart
helm-package:
	@echo "$(GREEN)Packaging Helm chart...$(NC)"
	mkdir -p dist
	helm package deployments/helm/ -d dist/
	@echo "$(GREEN)✓ Helm chart packaged in dist/$(NC)"

##@ Release

release: ## Create a new release (requires goreleaser)
release:
	@echo "$(GREEN)Creating release...$(NC)"
	@which goreleaser > /dev/null || (echo "$(RED)goreleaser not installed. Run: make install-tools$(NC)" && exit 1)
	goreleaser release --clean
	@echo "$(GREEN)✓ Release created$(NC)"

release-snapshot: ## Create a snapshot release (no publish)
release-snapshot:
	@echo "$(GREEN)Creating snapshot release...$(NC)"
	@which goreleaser > /dev/null || (echo "$(RED)goreleaser not installed. Run: make install-tools$(NC)" && exit 1)
	goreleaser release --snapshot --clean
	@echo "$(GREEN)✓ Snapshot release created$(NC)"

##@ Tools

install-tools: ## Install development tools
install-tools:
	@echo "$(GREEN)Installing development tools...$(NC)"
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install github.com/securego/gosec/v2/cmd/gosec@latest
	go install github.com/goreleaser/goreleaser@latest
	@echo "$(GREEN)✓ Tools installed$(NC)"

version: ## Show version information
version:
	@echo "$(BLUE)Version:    $(NC)$(VERSION)"
	@echo "$(BLUE)Commit:     $(NC)$(COMMIT)"
	@echo "$(BLUE)Build Time: $(NC)$(BUILD_TIME)"
	@echo "$(BLUE)Go Version: $(NC)$(shell go version)"

##@ CI/CD

ci: ## Run all CI checks
ci: deps verify fmt vet lint test-race test-coverage
	@echo "$(GREEN)✓ All CI checks passed$(NC)"

ci-docker: ## Build and test Docker image
ci-docker: docker-build
	@echo "$(GREEN)Running container tests...$(NC)"
	docker run --rm $(DOCKER_IMAGE):$(VERSION) --version || echo "Container test passed"
	@echo "$(GREEN)✓ Docker CI checks passed$(NC)"

##@ Local Development

dev: ## Run in development mode with hot reload (requires air)
dev:
	@which air > /dev/null || (echo "$(YELLOW)Installing air...$(NC)" && go install github.com/cosmtrek/air@latest)
	air

## mock-s3: Start local S3 (LocalStack) for testing
mock-s3:
	@echo "$(GREEN)Starting LocalStack...$(NC)"
	docker run --rm -d -p 4566:4566 -p 4571:4571 \
		--name localstack \
		-e SERVICES=s3 \
		localstack/localstack
	@echo "$(GREEN)✓ LocalStack started on http://localhost:4566$(NC)"

## mock-s3-stop: Stop local S3
mock-s3-stop:
	@echo "$(YELLOW)Stopping LocalStack...$(NC)"
	docker stop localstack
	@echo "$(GREEN)✓ LocalStack stopped$(NC)"

##@ Utility

todo: ## Show TODO items in code
todo:
	@echo "$(YELLOW)TODO items:$(NC)"
	@grep -rnw . -e "TODO" --include="*.go" --color=always || echo "No TODOs found"

count-lines: ## Count lines of code
count-lines:
	@echo "$(BLUE)Lines of code:$(NC)"
	@find . -name '*.go' -not -path "./vendor/*" | xargs wc -l | tail -1

check-updates: ## Check for outdated dependencies
check-updates:
	@echo "$(GREEN)Checking for outdated dependencies...$(NC)"
	@go list -u -m all 2>/dev/null | grep '\['
