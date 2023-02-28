# Build the manager binary
FROM --platform=$BUILDPLATFORM golang:1.20.1 as builder

ARG GOARCH=''

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg \
    go mod download

# Copy the go source
COPY main.go main.go
COPY cmd/ cmd/
COPY controllers/ controllers/
COPY pkg/ pkg/
COPY ori/ ori/

ARG TARGETOS
ARG TARGETARCH


FROM builder as controller
# Build
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GO111MODULE=on go build -ldflags="-s -w" -a -o bin/manager main.go

FROM builder as populator-bin
# Build
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GO111MODULE=on go build -ldflags="-s -w" -a -o bin/populator ./cmd/populator/populator.go

FROM builder as ori-volume-bin
# Build
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GO111MODULE=on go build -ldflags="-s -w" -a -o bin/ori-volume ./ori/volume/cmd/volume/main.go


# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot as manager
WORKDIR /
COPY --from=controller /workspace/bin/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]

FROM gcr.io/distroless/static:nonroot as populator
WORKDIR /
COPY --from=populator-bin /workspace/bin/populator .
USER 65532:65532

ENTRYPOINT ["/populator"]

FROM gcr.io/distroless/static:nonroot as ori-volume
WORKDIR /
COPY --from=ori-volume-bin /workspace/bin/ori-volume .
USER 65532:65532

ENTRYPOINT ["/ori-volume"]