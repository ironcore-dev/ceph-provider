
# Image URL to use all building/pushing image targets
CEPH_VOLUME_PROVIDER_IMG ?= ceph-volume-provider:latest
CEPH_BUCKET_PROVIDER_IMG ?= ceph-bucket-provider:latest

# Docker image name for the mkdocs based local development setup
MKDOCS_IMG=ironcore-dev/ceph-provider-docs

# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.28.0

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
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

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	# bucket
	$(CONTROLLER_GEN) rbac:roleName=broker-role paths="./internal/bucketserver/..." output:rbac:artifacts:config=config/ceph-bucket-provider/ceph-provider-rbac

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet envtest check-license ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path)" go run github.com/onsi/ginkgo/v2/ginkgo --label-filter="!integration" -coverprofile cover.out ./...

.PHONY: integration-tests
integration-tests:
	CGO=1 go run github.com/onsi/ginkgo/v2/ginkgo --label-filter="integration" ./...

.PHONY: add-license
add-license: addlicense ## Add license headers to all go files.
	find . -name '*.go' -exec $(ADDLICENSE) -f hack/license-header.txt {} +

.PHONY: check-license
check-license: addlicense ## Check that every file has a license header present.
	find . -name '*.go' -exec $(ADDLICENSE) -check -c 'IronCore authors' {} +

.PHONY: lint
lint: golangci-lint ## Run golangci-lint on the code.
	$(GOLANGCI_LINT) run ./...

check: manifests generate check-license lint test

##@ Documentation

.PHONY: start-docs
start-docs: ## Start the local mkdocs based development environment.
	docker build -t ${MKDOCS_IMG} -f docs/Dockerfile .
	docker run -p 8000:8000 -v `pwd`/:/docs ${MKDOCS_IMG}

.PHONY: clean-docs
clean-docs: ## Remove all local mkdocs Docker images (cleanup).
	docker container prune --force --filter "label=project=ceph-provider_documentation"

##@ Build

.PHONY: build-volume
build-volume: generate fmt vet ## Build manager binary.
	CGO_ENABLED=1 GO111MODULE=on go build -ldflags="-s -w" -a -o bin/ceph-volume-provider ./cmd/volumeprovider/main.go

.PHONY: build-bucket
build-bucket: generate fmt vet ## Build manager binary.
	CGO_ENABLED=0 GO111MODULE=on go build -ldflags="-s -w" -a -o bin/ceph-bucket-provider ./cmd/bucketprovider/main.go

.PHONY: run-volume
run-volume: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/volumeprovider/main.go

.PHONY: run-bucket
run-bucket: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/bucketprovider/main.go

.PHONY: docker-build
docker-build: test ## Build docker image with the manager.
	docker build --target ceph-volume-provider -t ${CEPH_VOLUME_PROVIDER_IMG} .
	docker build --target ceph-bucket-provider -t ${CEPH_BUCKET_PROVIDER_IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	docker push ${CEPH_VOLUME_PROVIDER_IMG}
	docker push ${CEPH_BUCKET_PROVIDER_IMG}

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif


##@ Build Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
ADDLICENSE ?= $(LOCALBIN)/addlicense
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint

## Tool Versions
KUSTOMIZE_VERSION ?= v3.8.7
CONTROLLER_TOOLS_VERSION ?= v0.14.0
ADDLICENSE_VERSION ?= v1.1.1
GOLANGCI_LINT_VERSION ?= v2.1

KUSTOMIZE_INSTALL_SCRIPT ?= "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh"
.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	test -s $(LOCALBIN)/kustomize || { curl -s $(KUSTOMIZE_INSTALL_SCRIPT) | bash -s -- $(subst v,,$(KUSTOMIZE_VERSION)) $(LOCALBIN); }

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(LOCALBIN)/controller-gen || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

.PHONY: envtest
envtest: $(ENVTEST) ## Download envtest-setup locally if necessary.
$(ENVTEST): $(LOCALBIN)
	test -s $(LOCALBIN)/setup-envtest || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest

.PHONY: addlicense
addlicense: $(ADDLICENSE) ## Download addlicense locally if necessary.
$(ADDLICENSE): $(LOCALBIN)
	test -s $(LOCALBIN)/addlicense || GOBIN=$(LOCALBIN) go install github.com/google/addlicense@$(ADDLICENSE_VERSION)

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	test -s $(LOCALBIN)/golangci-lint || GOBIN=$(LOCALBIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
