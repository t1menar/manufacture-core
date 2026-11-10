# Manufacture core. Run `make help` for the targets.
GO        ?= go
BIN_DIR   ?= bin
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT    ?= $(shell git rev-parse HEAD 2>/dev/null)
PKG       := github.com/t1menar/manufacture-core
LDFLAGS   := -s -w -X $(PKG)/internal/buildinfo.version=$(VERSION) -X $(PKG)/internal/buildinfo.commit=$(COMMIT)
GOLANGCI  ?= golangci-lint

.DEFAULT_GOAL := help
.PHONY: help build itest-db test cover lint fmt tidy check validate-testdata clean

help: ## show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## build bin/manufacture and bin/mfctl
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/manufacture ./cmd/manufacture
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/mfctl ./cmd/mfctl

itest-db: ## start a throwaway PostgreSQL for integration tests (prints MF_TEST_DATABASE_URL)
	docker run -d --rm --name mf-itest-pg -e POSTGRES_USER=mf -e POSTGRES_PASSWORD=mf -e POSTGRES_DB=mf -p 127.0.0.1:55432:5432 postgres:17
	@echo 'export MF_TEST_DATABASE_URL=postgres://mf:mf@127.0.0.1:55432/mf?sslmode=disable'

test: ## run tests with the race detector (integration tests need MF_TEST_DATABASE_URL)
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
