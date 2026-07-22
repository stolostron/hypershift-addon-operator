# Repo hosting the image with path
REPO ?= "quay.io/stolostron/"

# Image URL to use all building/pushing image targets
IMG ?= $(REPO)hypershift-addon-operator:latest

IMG_CANARY ?= $(REPO)hypershift-addon-operator-canary-test:latest

# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.28.3

KUBECTL?=kubectl

JUNIT_REPORT_FILE?=e2e-junit-report.xml

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
# This is a requirement for 'setup-envtest.sh' in the test target.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development


.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

# Use toolchain from go.mod so Go uses a complete install (with covdata); avoids
# "no such tool covdata" when auto-downloaded minimal toolchain is used (golang/go#75031).
GOTOOLCHAIN ?= $(shell (grep '^toolchain ' go.mod | cut -d' ' -f2) || echo "go$$(grep '^go ' go.mod | cut -d' ' -f2)")
export GOTOOLCHAIN

.PHONY: test
test: fmt vet envtest ## Run tests (with coverage).
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path)" go test $(shell go list ./... | grep -v /test/e2e) -coverprofile cover.out

.PHONY: test-no-cover
test-no-cover: fmt vet envtest ## Run tests without coverage (use if covdata tool is missing).
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path)" go test $(shell go list ./... | grep -v /test/e2e)

##@ Build
.PHONY: vendor
vendor:
	go mod tidy
	go mod vendor

.PHONY: build
build: vendor fmt vet ## Build manager binary.
	GOFLAGS="" go build -o bin/hypershift-addon cmd/main.go

.PHONY: build-konflux
build-konflux:
	GOFLAGS="" go build -o bin/hypershift-addon cmd/main.go

.PHONY: run
run: fmt vet ## Run a controller from your host.
	go run cmd/main.go

.PHONY: docker-build
docker-build:   # Build docker image with the manager.
	docker build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	docker push ${IMG}

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

ENVTEST = $(shell pwd)/bin/setup-envtest
# Keep setup-envtest aligned with controller-runtime to avoid
# requiring a newer Go toolchain than this repo uses in CI.
# Use Replace.Version when go.mod replaces controller-runtime (see require vs replace).
ENVTEST_VERSION ?= $(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' sigs.k8s.io/controller-runtime | awk -F'[v.]' '{printf "release-%d.%d", $$2, $$3}')
ENVTEST_PACKAGE ?= sigs.k8s.io/controller-runtime/tools/setup-envtest@$(ENVTEST_VERSION)
.PHONY: envtest
envtest: ## Download envtest-setup locally if necessary.
	$(call go-get-tool,$(ENVTEST),$(ENVTEST_PACKAGE))

# go-get-tool will 'go get' any package $2 and install it to $1.
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
define go-get-tool
	$(call go-get-tool-internal,$(1),$(2),$(firstword $(subst @, ,$(2))))
endef

define go-get-tool-internal
@[ -f $(1) ] || { \
set -e ;\
TMP_DIR=$$(mktemp -d) ;\
cd $$TMP_DIR ;\
go mod init tmp ;\
echo "Downloading $(2)" ;\
GOBIN=$(PROJECT_DIR)/bin go get -d $(2) ;\
GOBIN=$(PROJECT_DIR)/bin go install $(3) ;\
rm -rf $$TMP_DIR ;\
}
endef

COMPONENT_VERSION = $(shell cat COMPONENT_VERSION)
FOUNDATION_IMAGE_TAG = backplane-$(basename ${COMPONENT_VERSION})

build-e2e:
	go test -c ./test/e2e

test-e2e: build-e2e deploy-ocm deploy-addon-manager
#	./e2e.test -test.v -ginkgo.v -ginkgo.junit-report $(JUNIT_REPORT_FILE)

##@ HCP Proxy E2E (local)

# ---------------------------------------------------------------------------
# Variables – override on the command line as needed:
#   make e2e-hcp-proxy-full KIND_CLUSTER_NAME=my-cluster E2E_IMG=my-img:tag
# ---------------------------------------------------------------------------
KIND_VERSION         ?= v0.23.0
KIND_CLUSTER_NAME    ?= hcp-proxy-e2e
MANAGED_CLUSTER_NAME ?= local-cluster
# Image tag used inside the kind cluster (no registry push required)
E2E_IMG ?= kind-local/hypershift-addon-operator:$(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
KIND     ?= $(shell which kind 2>/dev/null || echo $(GOBIN)/kind)

.PHONY: ensure-kind
ensure-kind: ## Install kind $(KIND_VERSION) to $(GOBIN) if not already present.
	@if ! command -v kind >/dev/null 2>&1 && [ ! -f "$(GOBIN)/kind" ]; then \
	  OS=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
	  ARCH=$$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/'); \
	  echo "Installing kind $(KIND_VERSION) for $$OS/$$ARCH into $(GOBIN)..."; \
	  curl -sSLo "$(GOBIN)/kind" \
	    "https://kind.sigs.k8s.io/dl/$(KIND_VERSION)/kind-$$OS-$$ARCH"; \
	  chmod +x "$(GOBIN)/kind"; \
	fi
	@$(KIND) version

.PHONY: kind-create
kind-create: ensure-kind ## Create the $(KIND_CLUSTER_NAME) kind cluster.
	$(KIND) create cluster --name "$(KIND_CLUSTER_NAME)" --wait 120s
	$(KUBECTL) cluster-info --context "kind-$(KIND_CLUSTER_NAME)"

.PHONY: kind-delete
kind-delete: ## Delete the $(KIND_CLUSTER_NAME) kind cluster.
	$(KIND) delete cluster --name "$(KIND_CLUSTER_NAME)" || true

.PHONY: kind-load-e2e
kind-load-e2e: ## Load E2E_IMG into the kind cluster.
	$(KIND) load docker-image "$(E2E_IMG)" --name "$(KIND_CLUSTER_NAME)"

.PHONY: wait-hcp-proxy-service
wait-hcp-proxy-service: ## Wait until the HCP proxy Service has a cluster IP.
	@echo "Waiting for HCP proxy Service (hypershift-addon-hcp-proxy)..."; \
	for i in $$(seq 1 30); do \
	  $(KUBECTL) get service -n multicluster-engine hypershift-addon-hcp-proxy \
	    -o jsonpath='{.spec.clusterIP}' 2>/dev/null | grep -q '[0-9]' \
	    && echo "Service ready." && exit 0; \
	  echo "  waiting ($$i/30)..."; sleep 5; \
	done; \
	echo "ERROR: HCP proxy Service never became available"; exit 1

.PHONY: wait-hcp-proxy-apiservice
wait-hcp-proxy-apiservice: ## Wait until APIService v1alpha1.hcp.ocm.io is registered.
	@echo "Waiting for APIService v1alpha1.hcp.ocm.io..."; \
	for i in $$(seq 1 30); do \
	  $(KUBECTL) get apiservice v1alpha1.hcp.ocm.io 2>/dev/null && exit 0; \
	  echo "  waiting ($$i/30)..."; sleep 5; \
	done; \
	echo "ERROR: APIService v1alpha1.hcp.ocm.io never registered"; exit 1

# Host-build the linux binary (uses setup-go / local GOCACHE) then wrap in a
# tiny image — much faster than compiling inside Docker from a cold cache.
E2E_GOOS   ?= linux
E2E_GOARCH ?= $(shell go env GOARCH)

.PHONY: e2e-build-image
e2e-build-image: ## Build E2E_IMG via host go build + Dockerfile.e2e.
	# vendor/ is gitignored in this repo — use module mode (setup-go caches downloads).
	CGO_ENABLED=0 GOOS=$(E2E_GOOS) GOARCH=$(E2E_GOARCH) \
	  go build -o bin/hypershift-addon cmd/main.go
	docker build -f Dockerfile.e2e -t "$(E2E_IMG)" .

.PHONY: e2e-hcp-proxy-setup
e2e-hcp-proxy-setup: ensure-kind ## Spin up kind + OCM, build & load image, deploy addon manager.
	# One shell so background image build can be waited on after kind+OCM.
	@set -e; \
	echo "Building $(E2E_IMG) in background (host go build + Dockerfile.e2e)..."; \
	$(MAKE) e2e-build-image E2E_IMG="$(E2E_IMG)" E2E_GOOS="$(E2E_GOOS)" E2E_GOARCH="$(E2E_GOARCH)" & \
	build_pid=$$!; \
	$(MAKE) kind-create KIND_CLUSTER_NAME="$(KIND_CLUSTER_NAME)"; \
	$(MAKE) deploy-ocm; \
	$(MAKE) deploy-cluster-proxy; \
	$(MAKE) deploy-hypershift-crds; \
	echo "Waiting for image build (pid $$build_pid)..."; \
	wait $$build_pid; \
	$(MAKE) kind-load-e2e E2E_IMG="$(E2E_IMG)"; \
	$(KUBECTL) create namespace multicluster-engine --dry-run=client -o yaml | $(KUBECTL) apply -f -; \
	sed -e 's|image: quay.io/stolostron/hypershift-addon-operator:latest|image: $(E2E_IMG)|g' \
	    -e 's|value: quay.io/stolostron/hypershift-addon-operator:latest|value: $(E2E_IMG)|g' \
	    test/e2e/addon-manager-deployment.yaml | $(KUBECTL) apply -f -; \
	if ! $(KUBECTL) rollout status -n multicluster-engine deployment/hypershift-addon-manager --timeout=180s; then \
	  $(KUBECTL) describe -n multicluster-engine deployment/hypershift-addon-manager; \
	  $(KUBECTL) get pods -n multicluster-engine -l app=hypershift-addon-manager -o wide; \
	  $(KUBECTL) describe -n multicluster-engine -l app=hypershift-addon-manager pods; \
	  $(KUBECTL) logs -n multicluster-engine -l app=hypershift-addon-manager --tail=100 || true; \
	  exit 1; \
	fi; \
	$(MAKE) wait-hcp-proxy-service; \
	$(MAKE) wait-hcp-proxy-apiservice

.PHONY: e2e-hcp-proxy-full
e2e-hcp-proxy-full: e2e-hcp-proxy-setup ## Full cycle: setup → test → cleanup (always deletes kind cluster).
	@status=0; \
	$(MAKE) test-e2e-hcp-proxy MANAGED_CLUSTER_NAME="$(MANAGED_CLUSTER_NAME)" || status=$$?; \
	$(MAKE) kind-delete; \
	exit $$status

.PHONY: e2e-hcp-proxy-cleanup
e2e-hcp-proxy-cleanup: kind-delete ## Tear down the $(KIND_CLUSTER_NAME) kind cluster.

# Run only the HCP Proxy e2e suite against an already-deployed addon manager.
# Always port-forward: kind pod IPs (10.244.x.x) are not reachable from the
# GHA/host network, so direct pod-IP access times out on Linux CI.
HCP_PROXY_PORT ?= 18443

.PHONY: test-e2e-hcp-proxy
test-e2e-hcp-proxy:
	@echo "Starting kubectl port-forward on localhost:$(HCP_PROXY_PORT)..."
	@POD=$$($(KUBECTL) get pods -n multicluster-engine -l app=hypershift-addon-manager \
	        -o jsonpath='{.items[0].metadata.name}'); \
	test -n "$$POD" || { echo "ERROR: no hypershift-addon-manager pod found"; exit 1; }; \
	$(KUBECTL) port-forward -n multicluster-engine "pod/$$POD" \
	        "$(HCP_PROXY_PORT):9443" & PF_PID=$$!; \
	trap 'kill $$PF_PID 2>/dev/null || true' EXIT; \
	for i in 1 2 3 4 5 6 7 8 9 10; do \
	  if curl -sk --connect-timeout 1 "https://127.0.0.1:$(HCP_PROXY_PORT)/healthz" >/dev/null 2>&1; then \
	    break; \
	  fi; \
	  sleep 1; \
	done; \
	HCP_PROXY_HOST="localhost:$(HCP_PROXY_PORT)" \
	  go test ./test/e2e -timeout 15m -ginkgo.v -ginkgo.focus "HCP Proxy"

.PHONY: deploy-addon-manager
deploy-addon-manager:
	$(KUBECTL) create namespace multicluster-engine --dry-run=client -o yaml | $(KUBECTL) apply -f -
	sed -e 's|image: quay.io/stolostron/hypershift-addon-operator:latest|image: $(IMG)|g' \
	    -e 's|value: quay.io/stolostron/hypershift-addon-operator:latest|value: $(IMG)|g' \
	    test/e2e/addon-manager-deployment.yaml | $(KUBECTL) apply -f -
	$(KUBECTL) rollout status -n multicluster-engine deployment/hypershift-addon-manager --timeout=180s

deploy-ocm: ensure-clusteradm
	PATH="$(GOBIN):$$PATH" hack/install_ocm.sh

# OCM cluster-proxy addon (helm chart ocm/cluster-proxy) + Hypershift CRDs for
# HCP proxy POST create e2e through the spoke kube-apiserver.
.PHONY: deploy-cluster-proxy
deploy-cluster-proxy: ensure-helm ## Install OCM cluster-proxy into open-cluster-management-addon.
	PATH="$(GOBIN):$$PATH" hack/install_cluster_proxy.sh

.PHONY: deploy-hypershift-crds
deploy-hypershift-crds: ## Apply HostedCluster CRD so spoke create e2e can succeed.
	# Server-side apply avoids the client-side last-applied-configuration
	# annotation size limit on this large CRD.
	$(KUBECTL) apply --server-side --force-conflicts \
	  -f hack/crds/hypershift.openshift.io_hostedclusters.yaml

.PHONY: ensure-helm
ensure-helm: ## Install helm into $(GOBIN) if not already on PATH.
	@if ! command -v helm >/dev/null 2>&1 && [ ! -f "$(GOBIN)/helm" ]; then \
	  mkdir -p "$(GOBIN)"; \
	  OS=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
	  ARCH=$$(uname -m); \
	  case "$$ARCH" in x86_64) ARCH=amd64 ;; aarch64|arm64) ARCH=arm64 ;; esac; \
	  echo "Installing helm into $(GOBIN)..."; \
	  curl -fsSL "https://get.helm.sh/helm-v3.16.4-$$OS-$$ARCH.tar.gz" \
	    | tar -xz -C /tmp "$$OS-$$ARCH/helm"; \
	  mv "/tmp/$$OS-$$ARCH/helm" "$(GOBIN)/helm"; \
	  chmod +x "$(GOBIN)/helm"; \
	fi
	@command -v helm >/dev/null 2>&1 || test -x "$(GOBIN)/helm"

.PHONY: ensure-clusteradm
ensure-clusteradm: ## Install clusteradm into $(GOBIN) if not already on PATH.
	@if ! command -v clusteradm >/dev/null 2>&1 && [ ! -f "$(GOBIN)/clusteradm" ]; then \
	  mkdir -p "$(GOBIN)"; \
	  echo "Installing clusteradm into $(GOBIN)..."; \
	  curl -fsSL https://raw.githubusercontent.com/open-cluster-management-io/clusteradm/main/install.sh \
	    | INSTALL_DIR="$(GOBIN)" USE_SUDO=false bash; \
	fi
	@command -v clusteradm >/dev/null 2>&1 || test -x "$(GOBIN)/clusteradm"

.PHONY: quickstart
quickstart:
	./quickstart/start.sh

.PHONY: docker-build-canary
docker-build-canary:   # Build docker image with the manager.
	docker build -t ${IMG_CANARY} -f Dockerfile.canary --build-arg FOUNDATION_IMAGE_TAG=${FOUNDATION_IMAGE_TAG} .
