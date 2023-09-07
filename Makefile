
# Image URL to use all building/pushing image targets
CEPHLET_VOLUME_IMG ?= cephlet-volume:latest
CEPHLET_BUCKET_IMG ?= cephlet-bucket:latest

# Docker image name for the mkdocs based local development setup
MKDOCS_IMG=onmetal/cephlet-docs

# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.26.0

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
	$(CONTROLLER_GEN) rbac:roleName=broker-role paths="./ori/bucket/..." output:rbac:artifacts:config=config/cephlet-bucket/cephlet-rbac

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
test: manifests generate fmt vet envtest checklicense ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path)" go test ./... -- -coverprofile cover.out -ginkgo.label-filter="!integration"

.PHONY: intefration-tests
integration-tests:
	CGO=1 go test ./... -- -ginkgo.label-filter="integration"

.PHONY: addlicense
addlicense: ## Add license headers to all go files.
	find . -name '*.go' -exec go run github.com/google/addlicense -c 'OnMetal authors' {} +

.PHONY: checklicense
checklicense: ## Check that every file has a license header present.
	find . -name '*.go' -exec go run github.com/google/addlicense  -check -c 'OnMetal authors' {} +

lint: ## Run golangci-lint against code.
	golangci-lint run ./...

check: manifests generate checklicense lint test

##@ Documentation

.PHONY: start-docs
start-docs: ## Start the local mkdocs based development environment.
	docker build -t ${MKDOCS_IMG} -f docs/Dockerfile .
	docker run -p 8000:8000 -v `pwd`/:/docs ${MKDOCS_IMG}

.PHONY: clean-docs
clean-docs: ## Remove all local mkdocs Docker images (cleanup).
	docker container prune --force --filter "label=project=cephlet_documentation"

##@ Build

.PHONY: build-volume
build-volume: generate fmt vet ## Build manager binary.
	CGO_ENABLED=1 GO111MODULE=on go build -ldflags="-s -w" -a -o bin/cephlet-volume ./ori/volume/cmd/volume/main.go

.PHONY: build-bucket
build-bucket: generate fmt vet ## Build manager binary.
	CGO_ENABLED=0 GO111MODULE=on go build -ldflags="-s -w" -a -o bin/cephlet-bucket ./ori/bucket/cmd/bucket/main.go

.PHONY: run-volume
run-volume: manifests generate fmt vet ## Run a controller from your host.
	go run ./ori/bucket/cmd/volume/main.go

.PHONY: run-bucket
run-bucket: manifests generate fmt vet ## Run a controller from your host.
	go run ./ori/bucket/cmd/bucket/main.go

.PHONY: docker-build
docker-build: test ## Build docker image with the manager.
	docker build --target cephlet-volume -t ${CEPHLET_VOLUME_IMG} .
	docker build --target cephlet-bucket -t ${CEPHLET_BUCKET_IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	docker push ${CEPHLET_VOLUME_IMG}
	docker push ${CEPHLET_BUCKET_IMG}

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

## Tool Versions
KUSTOMIZE_VERSION ?= v3.8.7
CONTROLLER_TOOLS_VERSION ?= v0.11.1

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
