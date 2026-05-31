# kubetidy Makefile
# This repo lives under $GOPATH/src and the environment may set GOFLAGS=-mod=vendor
# globally; we force -mod=mod so module mode always works.
export GOFLAGS := -mod=mod

BIN_DIR    := bin
PKG        := github.com/kubetidy/kubetidy
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE       ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.Date=$(DATE)

.PHONY: all deps build test cover lint fmt vet check clean install

all: check build

deps:
	go mod tidy

# Build a single binary and expose it under both faces (kubetidy + kubectl-tidy).
build:
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/kubetidy ./cmd/kubetidy
	@cp $(BIN_DIR)/kubetidy $(BIN_DIR)/kubectl-tidy
	@echo "built $(BIN_DIR)/kubetidy and $(BIN_DIR)/kubectl-tidy"

test:
	go test ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

fmt:
	gofmt -l -w .

vet:
	go vet ./...

# golangci-lint is optional locally; CI enforces it.
lint: fmt vet
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run || echo "golangci-lint not installed; ran gofmt+vet only"

check: test vet
	@test -z "$$(gofmt -l . | grep -v vendor/)" || (echo "gofmt needed:" && gofmt -l . && exit 1)

# Install both faces into GOBIN (or $GOPATH/bin).
install:
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/kubetidy ./cmd/kubetidy
	@cp $(BIN_DIR)/kubetidy $(BIN_DIR)/kubectl-tidy
	@echo "copy $(BIN_DIR)/kubetidy and $(BIN_DIR)/kubectl-tidy onto your PATH"

clean:
	rm -rf $(BIN_DIR) coverage.out
