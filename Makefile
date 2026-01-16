# VERSION defines the project version for the bundle.
include $(CURDIR)/versions.mk

GO      := go
PKG     := ./...
CMD_DIR := ./cmd/rbln-metrics-exporter

LOCALBIN ?= $(CURDIR)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

GOFUMPT := $(LOCALBIN)/gofumpt
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint

##@ Container Images

# Container build configuration
CONTAINER_TOOL ?= docker

DOCKERFILE ?= $(CURDIR)/Dockerfile
PUSH_ON_BUILD ?= false
BUILD_MULTI_PLATFORM ?= false
DOCKER_BUILD_OPTIONS ?= --output=type=image,push=$(PUSH_ON_BUILD)
BUILDX =

ifeq ($(BUILD_MULTI_PLATFORM),true)
	DOCKER_BUILD_PLATFORM_OPTIONS ?= --platform=linux/amd64,linux/arm64
	BUILDX = buildx
else
	DOCKER_BUILD_PLATFORM_OPTIONS := --platform=linux/amd64
	# Avoid buildx-only --output flags when not using buildx
	DOCKER_BUILD_OPTIONS :=
endif

# Image registry and naming configuration
REGISTRY ?= docker.io/rebellions
IMAGE_NAME ?= $(REGISTRY)/rbln-metrics-exporter

# Image tagging configuration
IMAGE_TAG ?= $(VERSION)
IMAGE := $(IMAGE_NAME):$(IMAGE_TAG)

.PHONY: build
build:
	CGO_ENABLED=0 $(GO) build -o bin/$(BINARY) $(CMD_DIR)

.PHONY: clean
clean:
	rm -rf bin

.PHONY: test
test:
	$(GO) test $(PKG)

.PHONY: verify-deps
verify-deps:
	@echo "Verifying that all Go dependencies and vendor files are consistent..."
	go mod verify
	@echo "Go mod verify completed."
	go mod tidy
	@git diff --exit-code -- go.sum go.mod
	@echo "Go mod tidy completed."
	go mod vendor
	@git diff --exit-code -- vendor
	@echo "Go vendor completed."

.PHONY: fmt
fmt: ensure-gofumpt ## Run go fmt against code.
	@echo "Running go fmt..."
	@out="$$( $(GOFUMPT) -l . )"; \
	if [ -n "$$out" ]; then \
		echo "$$out"; \
		echo "Formatting issues found"; \
		exit 1; \
	fi
	@echo "Go fmt completed."

.PHONY: fmt-fix
fmt-fix:
	$(GOFUMPT) -l -w .

.PHONY: ensure-gofumpt
ensure-gofumpt:
	@echo "Ensuring gofumpt is installed..."
	GOBIN=$(LOCALBIN) GO111MODULE=on $(GO) install mvdan.cc/gofumpt@latest
	@echo "gofumpt installation complete."

.PHONY: vet
vet: # Run go vet against code.
	@echo "Running go vet..."
	go vet ./...
	@echo "Go vet completed."


.PHONY: lint
lint: ensure-golangci-lint # Run golangci-lint linter
	@echo "Running golangci-lint..."
	$(GOLANGCI_LINT) run
	@echo "golangci-lint completed."

.PHONY: lint-fix
lint-fix: ensure-golangci-lint # Run golangci-lint linter and perform fixes
	GOTOOLCHAIN=$(GOLANGCI_LINT_TOOLCHAIN) $(GOLANGCI_LINT) run --fix

.PHONY: build-image
build-image: # Build the RBLN metrics exporter image
	DOCKER_BUILDKIT=1 \
		$(CONTAINER_TOOL) $(BUILDX) build --pull \
		$(DOCKER_BUILD_OPTIONS) \
		$(DOCKER_BUILD_PLATFORM_OPTIONS) \
		--tag $(IMAGE) \
		--build-arg VERSION="$(VERSION)" \
		--build-arg GOLANG_VERSION="$(GOLANG_VERSION)" \
		--file $(DOCKERFILE) $(CURDIR)

.PHONY: ensure-golangci-lint
ensure-golangci-lint:
	@echo "Ensuring golangci-lint is installed..."
	GOBIN=$(LOCALBIN) GO111MODULE=on $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	@echo "golangci-lint installation complete."

.PHONY: code-check
code-check: vet fmt lint verify-deps

.PHONY: pre-commit-install
pre-commit-install:
	pre-commit install

.PHONY: pre-commit-run
pre-commit-run:
	pre-commit run --all-files
