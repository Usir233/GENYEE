# Makefile for the GENYEE CLI (cli/ Go module).
#
# Usage:
#   make build         compile to ./bin/genyee with version metadata
#   make test          go test ./...
#   make test-coverage same with coverage report
#   make lint          go vet (golangci-lint planned)
#   make tidy          go mod tidy
#   make clean         remove ./bin

BINARY      := genyee
BUILD_DIR   := bin
PKG         := github.com/Usir233/GENYEE/cli

VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE        := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(PKG)/internal/build.Version=$(VERSION) \
	-X $(PKG)/internal/build.Commit=$(COMMIT) \
	-X $(PKG)/internal/build.Date=$(DATE)

.PHONY: build test test-coverage lint tidy clean help

build: ## Compile ./bin/genyee with version metadata
	@mkdir -p $(BUILD_DIR)
	go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) .

test: ## Run Go unit and contract tests
	go test ./...

test-coverage: ## Run tests and print function coverage report
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

lint: ## Run go vet static analysis
	go vet ./...

tidy: ## Tidy cli/go.mod and cli/go.sum
	go mod tidy

clean: ## Remove build artifacts and coverage profile
	rm -rf $(BUILD_DIR) coverage.out

help: ## Show available make targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
