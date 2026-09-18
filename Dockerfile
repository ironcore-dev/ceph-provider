# Build the manager binary
FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm AS builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.sum ./

# Cache dependencies before copying source code
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg \
    go mod download

# Copy the Go source code
COPY api/ api/
COPY internal/ internal/
COPY cmd/ cmd/
COPY hack/ hack/

ARG TARGETOS
ARG TARGETARCH
ARG BUILDPLATFORM
ARG LDFLAGS
ENV BUILDARCH=${BUILDPLATFORM##*/}

FROM builder AS ceph-bucket-provider-builder
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GO111MODULE=on go build -ldflags="${LDFLAGS}" -a -o bin/ceph-bucket-provider ./cmd/bucketprovider/main.go


FROM builder AS ceph-volume-provider-builder
# Install necessary dependencies

# go-ceph v0.41.0 requires Ceph Squid >= 19.2.4 or Tentacle >= 20.2.1
RUN curl -fsSL https://eu.ceph.com/keys/release.asc -o /usr/share/keyrings/ceph-release.asc && \
    tee /etc/apt/sources.list.d/ceph.sources <<'EOF'
Types: deb
URIs: https://eu.ceph.com/debian-tentacle/
Suites: bookworm
Components: main
Signed-By: /usr/share/keyrings/ceph-release.asc
EOF
RUN apt-get update && apt-get install -y libcephfs-dev librbd-dev librados-dev libc-bin

# Install cross-compiler for ARM64 if building for arm64 on an amd64 host
RUN if [ "$TARGETARCH" = "arm64" ] && [ "$BUILDARCH" = "amd64" ]; then \
      dpkg --add-architecture arm64 && \
      apt-get update && apt-get install -y --no-install-recommends \
      gcc-aarch64-linux-gnu librbd-dev:arm64 librados-dev:arm64 libc6-dev:arm64; \
    fi

# Install cross-compiler for AMD64 if building for amd64 on an arm64 host
RUN if [ "$TARGETARCH" = "amd64" ] && [ "$BUILDARCH" = "arm64" ]; then \
      apt update && apt-get install -y --no-install-recommends \
      gcc-x86-64-linux-gnu; \
    fi

# Set compiler and linker flags based on target architecture
ENV CC=""
ENV CGO_LDFLAGS=""

# Build
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg \
    if [ "$TARGETARCH" != "$BUILDARCH" ] && [ "$TARGETARCH" = "arm64" ]; then \
      export CC="/usr/bin/aarch64-linux-gnu-gcc"; \
      export CGO_LDFLAGS="-L/usr/lib/aarch64-linux-gnu -Wl,-lrados -Wl,-lrbd"; \
    elif [ "$TARGETARCH" != "$BUILDARCH" ] && [ "$TARGETARCH" = "amd64" ]; then \
      export CC="/usr/bin/x86_64-linux-gnu-gcc"; \
      export CGO_LDFLAGS="-L/usr/lib/x86_64-linux-gnu -Wl,-lrados -Wl,-lrbd"; \
    else \
      export CC="/usr/bin/gcc"; \
      export CGO_LDFLAGS=""; \
    fi && \
    CGO_ENABLED=1 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    CC="$CC" CGO_LDFLAGS="$CGO_LDFLAGS" GO111MODULE=on \
    go build -ldflags="${LDFLAGS} -linkmode=external" -a -o bin/ceph-volume-provider ./cmd/volumeprovider/main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS manager
WORKDIR /
COPY --from=controller /workspace/bin/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]



# Since we're leveraging apt to pull in dependencies, we use `gcr.io/distroless/base` because it includes glibc.
FROM gcr.io/distroless/base-debian13:latest@sha256:0ebad3510af52aefe45045cc01b07564570be4feecf8d9f93d3a05d1b5f2f93b AS distroless-base

# The distroless amd64 image has a target triplet of x86_64
FROM distroless-base AS distroless-amd64
ENV LIB_DIR_PREFIX=x86_64
ENV LIB_DIR_PREFIX_MINUS=x86-64
ENV LIB_DIR_SUFFIX_NUMBER=2
ENV LIB_DIR=lib64

# The distroless arm64 image has a target triplet of aarch64
FROM distroless-base AS distroless-arm64
ENV LIB_DIR_PREFIX=aarch64
ENV LIB_DIR_PREFIX_MINUS=aarch64
ENV LIB_DIR_SUFFIX_NUMBER=1
ENV LIB_DIR=lib

FROM busybox:1.38.0-uclibc AS busybox
FROM distroless-$TARGETARCH AS ceph-volume-provider-image
WORKDIR /
COPY --from=busybox /bin/sh /bin/sh
COPY --from=busybox /bin/mkdir /bin/mkdir
COPY --from=ceph-volume-provider-builder /usr/lib/librados.so.2 \
/usr/lib/librbd.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libc.so.6 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libstdc++.so.6 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libgcc_s.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libssl.so.3 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libcryptsetup.so.12 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libnbd.so.0 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libcrypto.so.3 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libresolv.so.2 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libblkid.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libudev.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libibverbs.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/librdmacm.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libz.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libcurl.so.4 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libthrift-0.17.0.so \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libm.so.6 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libuuid.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libdevmapper.so.1.02.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libargon2.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libjson-c.so.5 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libgnutls.so.30 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libxml2.so.2 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libnl-route-3.so.200 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libnl-3.so.200 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libnghttp2.so.14 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libidn2.so.0 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/librtmp.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libssh2.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libpsl.so.5 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libgssapi_krb5.so.2 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libldap-2.5.so.0 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/liblber-2.5.so.0 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libzstd.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libbrotlidec.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libselinux.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libp11-kit.so.0 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libunistring.so.2 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libtasn1.so.6 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libnettle.so.8 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libhogweed.so.6 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libgmp.so.10 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libicuuc.so.72 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/liblzma.so.5 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libkrb5.so.3 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libk5crypto.so.3 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libcom_err.so.2 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libkrb5support.so.0 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libsasl2.so.2 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libbrotlicommon.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libpcre2-8.so.0 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libffi.so.8 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libicudata.so.72 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libkeyutils.so.1 /lib/${LIB_DIR_PREFIX}-linux-gnu/
RUN mkdir -p /${LIB_DIR}
COPY --from=ceph-volume-provider-builder /${LIB_DIR}/ld-linux-${LIB_DIR_PREFIX_MINUS}.so.${LIB_DIR_SUFFIX_NUMBER} /${LIB_DIR}/
RUN mkdir -p /usr/lib/ceph/
COPY --from=ceph-volume-provider-builder /usr/lib/ceph/libceph-common.so.2 /usr/lib/ceph

COPY --from=ceph-volume-provider-builder /workspace/bin/ceph-volume-provider /ceph-volume-provider

# Build stage used for validation of the output-image
# See validate-container-linux-* targets in Makefile
FROM ceph-volume-provider-image AS validation-image

COPY --from=busybox /usr/bin/ldd /usr/bin/find /usr/bin/xargs /usr/bin/
COPY --from=builder /workspace/hack/print-missing-deps.sh /print-missing-deps.sh
SHELL ["/bin/bash", "-c"]
RUN /print-missing-deps.sh


# Final build stage, create the real Docker image with ENTRYPOINT
FROM ceph-volume-provider-image AS ceph-volume-provider
USER 65532:65532

ENTRYPOINT ["/ceph-volume-provider"]



FROM distroless-base  AS ceph-bucket-provider
COPY --from=ceph-bucket-provider-builder /workspace/bin/ceph-bucket-provider /ceph-bucket-provider
USER 65532:65532
ENTRYPOINT ["/ceph-bucket-provider"]
