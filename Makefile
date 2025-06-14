PROJECT_DIR = $(shell pwd)
PROJECT_BIN = $(PROJECT_DIR)/bin
$(shell [ -f bin ] || mkdir -p $(PROJECT_BIN))
PATH := $(PROJECT_BIN):$(PATH)

.DEFAULT_GOAL := help

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
