# kubetidy Makefile
# This repo lives under $GOPATH/src and the environment may set GOFLAGS=-mod=vendor
# globally; we force -mod=mod so module mode always works.
export GOFLAGS := -mod=mod

BIN_DIR    := bin
PKG        := github.com/kubetidy/kubetidy
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE       ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -s -w -X $(PKG)/internal/version.Version=$(VERSION) -X $(PKG)/internal/version.Commit=$(COMMIT) -X $(PKG)/internal/version.Date=$(DATE)

GOLANGCI_LINT ?= $(shell go env GOPATH)/bin/golangci-lint
GOLANGCI_VERSION ?= v2.6.0

CONTROLLER_GEN ?= $(shell go env GOPATH)/bin/controller-gen
CONTROLLER_GEN_VERSION ?= v0.16.5

# operator image (Docker Hub). The image is always Linux; PUSH_PLATFORMS is multi-arch so it
# runs on amd64 clusters and arm64 (Apple-Silicon kind / Graviton) alike. LOCAL_PLATFORM is
# the single Linux arch used for a local kind load (defaults to the host's arch).
OPERATOR_IMAGE  ?= docker.io/mayurdas1991/kubetidy-operator
OPERATOR_TAG    ?= latest
PUSH_PLATFORMS  ?= linux/amd64,linux/arm64
LOCAL_PLATFORM  ?= linux/$(shell go env GOARCH)

# kind / demo settings
KIND_CLUSTER   := kubetidy
KIND_CONFIG    := hack/kind/cluster.yaml
DEMO_MANIFEST  := hack/kind/demo-workloads.yaml
PROM_MANIFEST  := hack/kind/prometheus.yaml
PROM_NS        := monitoring
DEMO_NS        := kubetidy-demo
PROM_URL       ?= http://prometheus-server.monitoring.svc:80

# ANSI helpers for a readable `make help`.
BLUE  := \033[36m
RESET := \033[0m

.DEFAULT_GOAL := help

##@ General

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make $(BLUE)<target>$(RESET)\n"} \
		/^[a-zA-Z0-9_-]+:.*?##/ { printf "  $(BLUE)%-22s$(RESET) %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Build & Develop

.PHONY: deps
deps: ## Download and tidy module dependencies
	go mod tidy

.PHONY: generate
generate: ## Regenerate CRDs + DeepCopy from api/ markers (controller-gen)
	@command -v $(CONTROLLER_GEN) >/dev/null 2>&1 || \
		go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)
	$(CONTROLLER_GEN) object paths=./api/...
	$(CONTROLLER_GEN) crd:allowDangerousTypes=true paths=./api/... output:crd:dir=config/crd
	@cp config/crd/kubetidy.io_usageprofiles.yaml internal/installer/assets/usageprofiles.yaml
	@cp config/crd/kubetidy.io_clusterusagesummaries.yaml internal/installer/assets/clusterusagesummaries.yaml
	@cp config/crd/kubetidy.io_recommendations.yaml internal/installer/assets/recommendations.yaml
	@echo "regenerated CRDs + deepcopy; copied CRDs into installer assets"

.PHONY: build
build: ## Build the binary as both kubetidy and kubectl-tidy into ./bin
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/kubetidy ./cmd/kubetidy
	@cp $(BIN_DIR)/kubetidy $(BIN_DIR)/kubectl-tidy
	@echo "built $(BIN_DIR)/kubetidy and $(BIN_DIR)/kubectl-tidy"

.PHONY: install
install: build ## Build and copy both faces onto your PATH (/usr/local/bin, may need sudo)
	@install -m 0755 $(BIN_DIR)/kubetidy /usr/local/bin/kubetidy
	@install -m 0755 $(BIN_DIR)/kubectl-tidy /usr/local/bin/kubectl-tidy
	@echo "installed kubetidy and kubectl-tidy to /usr/local/bin"

.PHONY: run
run: build ## Build then run a scan against the current kube context
	$(BIN_DIR)/kubetidy scan

.PHONY: clean
clean: ## Remove build output and coverage files
	rm -rf $(BIN_DIR) coverage.out coverage.html

##@ Test & Quality

.PHONY: test
test: ## Run all unit tests
	go test ./...

.PHONY: test-race
test-race: ## Run all unit tests with the race detector
	go test -race ./...

.PHONY: cover
cover: ## Run tests and print total coverage
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

.PHONY: cover-html
cover-html: cover ## Generate and open an HTML coverage report
	go tool cover -html=coverage.out -o coverage.html
	@echo "open coverage.html"

.PHONY: fmt
fmt: ## Format all Go source with gofmt
	gofmt -l -w .

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: lint-install
lint-install: ## Install golangci-lint (v2) into $GOPATH/bin
	@command -v $(GOLANGCI_LINT) >/dev/null 2>&1 || \
		go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
	@echo "golangci-lint: $$($(GOLANGCI_LINT) version 2>/dev/null || echo 'install failed')"

.PHONY: lint
lint: ## Run golangci-lint (installs it if missing)
	@command -v $(GOLANGCI_LINT) >/dev/null 2>&1 || $(MAKE) lint-install
	$(GOLANGCI_LINT) run ./...

.PHONY: check
check: test vet ## Run the full pre-PR gate: tests + vet + gofmt + lint
	@test -z "$$(gofmt -l . | grep -v vendor/)" || (echo "gofmt needed:" && gofmt -l . && exit 1)
	@$(MAKE) lint

##@ Local cluster (kind)

.PHONY: kind-up
kind-up: ## Create the local kind cluster
	kind create cluster --name $(KIND_CLUSTER) --config $(KIND_CONFIG)
	@echo "kind cluster '$(KIND_CLUSTER)' ready; context: kind-$(KIND_CLUSTER)"

.PHONY: kind-metrics
kind-metrics: ## Install metrics-server into the kind cluster (Tier 0 data source)
	kubectl --context kind-$(KIND_CLUSTER) apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml
	# kind nodes use self-signed kubelet certs; allow metrics-server to talk to them.
	kubectl --context kind-$(KIND_CLUSTER) -n kube-system patch deployment metrics-server \
		--type=json -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'
	kubectl --context kind-$(KIND_CLUSTER) -n kube-system rollout status deployment/metrics-server --timeout=120s

.PHONY: demo-deploy
demo-deploy: ## Deploy over-provisioned demo workloads into the kind cluster
	kubectl --context kind-$(KIND_CLUSTER) apply -f $(DEMO_MANIFEST)
	kubectl --context kind-$(KIND_CLUSTER) -n $(DEMO_NS) rollout status deployment/checkout-api --timeout=120s

.PHONY: prometheus
prometheus: ## Deploy a minimal Prometheus into the cluster (unlocks Tier 1)
	kubectl --context kind-$(KIND_CLUSTER) apply -f $(PROM_MANIFEST)
	kubectl --context kind-$(KIND_CLUSTER) -n $(PROM_NS) rollout status deployment/prometheus-server --timeout=180s
	@echo "Prometheus ready. kubetidy auto-detects it; scans now run at Tier 1."

.PHONY: crd-install
crd-install: ## Install just the UsageProfile CRD into the kind cluster
	kubectl --context kind-$(KIND_CLUSTER) apply -f config/crd/usageprofiles.yaml

.PHONY: operator-image
operator-image: ## Build the operator image for the local Linux arch and load it into Docker
	docker buildx build --platform $(LOCAL_PLATFORM) --load \
		-t $(OPERATOR_IMAGE):$(OPERATOR_TAG) -f hack/operator/Dockerfile .
	@echo "built $(LOCAL_PLATFORM) image $(OPERATOR_IMAGE):$(OPERATOR_TAG)"

.PHONY: operator-push
operator-push: ## Build a multi-arch Linux image and push to Docker Hub (run `docker login` first)
	docker buildx build --platform $(PUSH_PLATFORMS) --push \
		-t $(OPERATOR_IMAGE):$(OPERATOR_TAG) -f hack/operator/Dockerfile .
	@echo "pushed $(PUSH_PLATFORMS) image $(OPERATOR_IMAGE):$(OPERATOR_TAG) — clusters can now run kubectl tidy init"

.PHONY: operator-deploy
operator-deploy: ## Build, load, and deploy the kubetidy operator into the kind cluster (Tier 0)
	docker buildx build --platform $(LOCAL_PLATFORM) --load \
		-t $(OPERATOR_IMAGE):$(OPERATOR_TAG) -f hack/operator/Dockerfile .
	kind load docker-image $(OPERATOR_IMAGE):$(OPERATOR_TAG) --name $(KIND_CLUSTER)
	kubectl --context kind-$(KIND_CLUSTER) apply -f config/crd/usageprofiles.yaml
	kubectl --context kind-$(KIND_CLUSTER) apply -f config/operator/operator.yaml
	kubectl --context kind-$(KIND_CLUSTER) -n kubetidy-system rollout status deployment/kubetidy-operator --timeout=120s
	@echo "Operator running. Give it a few minutes to accumulate history; scans then run at Tier 0 (operator) with no Prometheus."

.PHONY: demo-scan
demo-scan: build ## Run a scan against the demo namespace in the kind cluster
	$(BIN_DIR)/kubetidy scan --context kind-$(KIND_CLUSTER) -n $(DEMO_NS)

.PHONY: demo-scan-prom
demo-scan-prom: build ## Tier-1 scan: scan the demo namespace using Prometheus
	$(BIN_DIR)/kubetidy scan --context kind-$(KIND_CLUSTER) -n $(DEMO_NS) --prometheus-url $(PROM_URL)

.PHONY: demo-diff
demo-diff: build ## Show reversible kubectl patches for the demo namespace
	$(BIN_DIR)/kubetidy diff --context kind-$(KIND_CLUSTER) -n $(DEMO_NS)

.PHONY: e2e
e2e: ## One command: create kind cluster, install metrics-server, deploy demo, scan + diff
	@$(MAKE) kind-up
	@$(MAKE) kind-metrics
	@$(MAKE) demo-deploy
	@echo "waiting ~30s for metrics-server to collect a first sample..."
	@sleep 30
	@$(MAKE) demo-scan
	@$(MAKE) demo-diff

.PHONY: e2e-prom
e2e-prom: ## Full Tier-1 demo: kind + metrics + Prometheus + demo, then a Tier-1 scan
	@$(MAKE) kind-up
	@$(MAKE) kind-metrics
	@$(MAKE) prometheus
	@$(MAKE) demo-deploy
	@echo "waiting ~60s for Prometheus to scrape a usable window..."
	@sleep 60
	@$(MAKE) demo-scan-prom
	@$(MAKE) demo-diff

.PHONY: kind-down
kind-down: ## Delete the local kind cluster
	kind delete cluster --name $(KIND_CLUSTER)
