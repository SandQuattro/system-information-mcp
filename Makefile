PROJECT_DIR = $(shell pwd)
PROJECT_BIN = $(PROJECT_DIR)/bin
$(shell [ -f bin ] || mkdir -p $(PROJECT_BIN))
PATH := $(PROJECT_BIN):$(PATH)

.DEFAULT_GOAL := help

# ---------------------------------- VENDOR ------------------------------------
.PHONY: vendor
vendor: ## download and vendor dependencies
	go mod tidy
	go mod vendor

.PHONY: vendor-clean
vendor-clean: ## clean vendor directory
	rm -rf vendor/

# ---------------------------------- BUILD -------------------------------------
.PHONY: build
build: ## build project
	CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o system-info-server ./cmd/mcp

.PHONY: build-vendor
build-vendor: vendor ## build project with vendor
	CGO_ENABLED=0 GOOS=linux go build -mod=vendor -a -installsuffix cgo -o system-info-server ./cmd/mcp

# ---------------------------------- DOCKER ------------------------------------
.PHONY: docker
docker: vendor ## build docker image with vendor
	docker build --no-cache -t mcp-system-info .
	docker stop mcp-system-info || true
	docker rm mcp-system-info || true
	docker run -p 8080:8080 -d --name mcp-system-info mcp-system-info

# ---------------------------------- LINTING ------------------------------------
GOLANGCI_LINT = golangci-lint

.PHONY: lint-help
lint-help: ## show linter help
	$(GOLANGCI_LINT) help linters
.PHONY: lint
lint: ## run linter
	gofumpt -w ./..
	$(GOLANGCI_LINT) run ./... --config=./.golangci.yml

.PHONY: lint-fast
lint-fast: ## run fast linter
	gofumpt -w ./..
	$(GOLANGCI_LINT) run ./... --fast --config=./.golangci.yml

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
