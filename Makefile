# Manufacture core. Run `make help` for the targets.
GO        ?= go
BIN_DIR   ?= bin
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT    ?= $(shell git rev-parse HEAD 2>/dev/null)
PKG       := github.com/t1menar/manufacture-core
LDFLAGS   := -s -w -X $(PKG)/internal/buildinfo.version=$(VERSION) -X $(PKG)/internal/buildinfo.commit=$(COMMIT)
GOLANGCI  ?= golangci-lint

.DEFAULT_GOAL := help
.PHONY: help build test cover lint fmt tidy check validate-testdata clean

help: ## show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## build bin/manufacture
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/manufacture ./cmd/manufacture

test: ## run unit tests with the race detector
	$(GO) test -race -count=1 ./...

cover: ## run tests and print coverage per function
	$(GO) test -count=1 -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -n 1

lint: ## run golangci-lint
	$(GOLANGCI) run ./...

fmt: ## format the code
	$(GOLANGCI) fmt ./...

tidy: ## fail if go.mod or go.sum are not tidy
	$(GO) mod tidy -diff

validate-testdata: build ## run the binary against testdata
	$(BIN_DIR)/manufacture validate -f testdata/valid
	! $(BIN_DIR)/manufacture validate -f testdata/invalid > /dev/null

check: tidy lint test validate-testdata ## everything CI runs

clean: ## remove build output
	rm -rf $(BIN_DIR) coverage.out
