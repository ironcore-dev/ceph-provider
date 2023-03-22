# Build the manager binary
FROM --platform=$BUILDPLATFORM debian:bookworm-slim as builder

ARG GOARCH=''


# TODO: remove once go uses bookworm


# ############# remove #############
# install cgo-related dependencies
RUN set -eux; \
	apt-get update; \
	apt-get install -y --no-install-recommends \
		g++ \
		gcc \
		libc6-dev \
		make \
		pkg-config \
        wget \
        ca-certificates \
        gpg \
        dirmngr \
        gpg-agent \
	; \
	rm -rf /var/lib/apt/lists/*

ENV PATH /usr/local/go/bin:$PATH

ENV GOLANG_VERSION 1.20.2

RUN set -eux; \
	arch="$(dpkg --print-architecture)"; arch="${arch##*-}"; \
	url=; \
	case "$arch" in \
		'amd64') \
			url='https://dl.google.com/go/go1.20.2.linux-amd64.tar.gz'; \
			sha256='4eaea32f59cde4dc635fbc42161031d13e1c780b87097f4b4234cfce671f1768'; \
			;; \
		'armel') \
			export GOARCH='arm' GOARM='5' GOOS='linux'; \
			;; \
		'armhf') \
			url='https://dl.google.com/go/go1.20.2.linux-armv6l.tar.gz'; \
			sha256='d79d56bafd6b52b8d8cbe3f8e967caaac5383a23d7a4fa9ac0e89778cd16a076'; \
			;; \
		'arm64') \
			url='https://dl.google.com/go/go1.20.2.linux-arm64.tar.gz'; \
			sha256='78d632915bb75e9a6356a47a42625fd1a785c83a64a643fedd8f61e31b1b3bef'; \
			;; \
		'i386') \
			url='https://dl.google.com/go/go1.20.2.linux-386.tar.gz'; \
			sha256='ee240ed33ae57504c41f04c12236aeaa17fbeb6ea9fcd096cd9dc7a89d10d4db'; \
			;; \
		'mips64el') \
			export GOARCH='mips64le' GOOS='linux'; \
			;; \
		'ppc64el') \
			url='https://dl.google.com/go/go1.20.2.linux-ppc64le.tar.gz'; \
			sha256='850564ddb760cb703db63bf20182dc4407abd2ff090a95fa66d6634d172fd095'; \
			;; \
		's390x') \
			url='https://dl.google.com/go/go1.20.2.linux-s390x.tar.gz'; \
			sha256='8da24c5c4205fe8115f594237e5db7bcb1d23df67bc1fa9a999954b1976896e8'; \
			;; \
		*) echo >&2 "error: unsupported architecture '$arch' (likely packaging update needed)"; exit 1 ;; \
	esac; \
	build=; \
	if [ -z "$url" ]; then \
# https://github.com/golang/go/issues/38536#issuecomment-616897960
		build=1; \
		url='https://dl.google.com/go/go1.20.2.src.tar.gz'; \
		sha256='4d0e2850d197b4ddad3bdb0196300179d095bb3aefd4dfbc3b36702c3728f8ab'; \
		echo >&2; \
		echo >&2 "warning: current architecture ($arch) does not have a compatible Go binary release; will be building from source"; \
		echo >&2; \
	fi; \
	\
	wget -O go.tgz.asc "$url.asc"; \
	wget -O go.tgz "$url" --progress=dot:giga; \
	echo "$sha256 *go.tgz" | sha256sum -c -; \
	\
# https://github.com/golang/go/issues/14739#issuecomment-324767697
	GNUPGHOME="$(mktemp -d)"; export GNUPGHOME; \
# https://www.google.com/linuxrepositories/
	gpg --batch --keyserver keyserver.ubuntu.com --recv-keys 'EB4C 1BFD 4F04 2F6D DDCC  EC91 7721 F63B D38B 4796'; \
# let's also fetch the specific subkey of that key explicitly that we expect "go.tgz.asc" to be signed by, just to make sure we definitely have it
	gpg --batch --keyserver keyserver.ubuntu.com --recv-keys '2F52 8D36 D67B 69ED F998  D857 78BD 6547 3CB3 BD13'; \
	gpg --batch --verify go.tgz.asc go.tgz; \
	gpgconf --kill all; \
	rm -rf "$GNUPGHOME" go.tgz.asc; \
	\
	tar -C /usr/local -xzf go.tgz; \
	rm go.tgz; \
	\
	if [ -n "$build" ]; then \
		savedAptMark="$(apt-mark showmanual)"; \
# add backports for newer go version for bootstrap build: https://github.com/golang/go/issues/44505
		( \
			. /etc/os-release; \
			echo "deb https://deb.debian.org/debian $VERSION_CODENAME-backports main" > /etc/apt/sources.list.d/backports.list; \
			\
			apt-get update; \
			apt-get install -y --no-install-recommends -t "$VERSION_CODENAME-backports" golang-go; \
		); \
		\
		export GOCACHE='/tmp/gocache'; \
		\
		( \
			cd /usr/local/go/src; \
# set GOROOT_BOOTSTRAP + GOHOST* such that we can build Go successfully
			export GOROOT_BOOTSTRAP="$(go env GOROOT)" GOHOSTOS="$GOOS" GOHOSTARCH="$GOARCH"; \
			./make.bash; \
		); \
		\
		apt-mark auto '.*' > /dev/null; \
		apt-mark manual $savedAptMark > /dev/null; \
		apt-get purge -y --auto-remove -o APT::AutoRemove::RecommendsImportant=false; \
		rm -rf /var/lib/apt/lists/*; \
		\
# remove a few intermediate / bootstrapping files the official binary release tarballs do not contain
		rm -rf \
			/usr/local/go/pkg/*/cmd \
			/usr/local/go/pkg/bootstrap \
			/usr/local/go/pkg/obj \
			/usr/local/go/pkg/tool/*/api \
			/usr/local/go/pkg/tool/*/go_bootstrap \
			/usr/local/go/src/cmd/dist/dist \
			"$GOCACHE" \
		; \
	fi; \
	\
	go version

ENV GOPATH /go
ENV PATH $GOPATH/bin:$PATH
RUN mkdir -p "$GOPATH/src" "$GOPATH/bin" && chmod -R 777 "$GOPATH"

# ############# remove #############

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
COPY controllers/ controllers/
COPY pkg/ pkg/
COPY ori/ ori/
COPY hack/ hack/

ARG TARGETOS
ARG TARGETARCH


FROM builder as controller
# Build
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GO111MODULE=on go build -ldflags="-s -w" -a -o bin/manager main.go


# Start from Kubernetes Debian base.
FROM builder as ori-volume-builder
# Install necessary dependencies

RUN apt update  && apt install -y libcephfs-dev librbd-dev librados-dev

# Build
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg \
    CGO_ENABLED=1 GOOS=$TARGETOS GOARCH=$TARGETARCH GO111MODULE=on go build -ldflags="-s -w" -a -o bin/ori-volume ./ori/volume/cmd/volume/main.go


# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot as manager
WORKDIR /
COPY --from=controller /workspace/bin/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]



# Since we're leveraging apt to pull in dependencies, we use `gcr.io/distroless/base` because it includes glibc.
FROM gcr.io/distroless/base-debian11 as distroless-base

# The distroless amd64 image has a target triplet of x86_64
FROM distroless-base AS distroless-amd64
ENV LIB_DIR_PREFIX x86_64
ENV LIB_DIR_PREFIX_MINUS x86-64

# The distroless arm64 image has a target triplet of aarch64
FROM distroless-base AS distroless-arm64
ENV LIB_DIR_PREFIX aarch64
ENV LIB_DIR_PREFIX_MINUS aarch64


FROM busybox:1.35.0-uclibc as busybox
FROM distroless-$TARGETARCH  as ori-volume
WORKDIR /
COPY --from=busybox /bin/sh /bin/sh
COPY --from=busybox /bin/mkdir /bin/mkdir
COPY --from=ori-volume-builder /lib/x86_64-linux-gnu/librados.so.2 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/librbd.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libc.so.6 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libfmt.so.9 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libstdc++.so.6 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libgcc_s.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libssl.so.3 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libcryptsetup.so.12 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libcrypto.so.3 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libresolv.so.2 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libboost_thread.so.1.74.0 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libboost_iostreams.so.1.74.0 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libblkid.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libudev.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libibverbs.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/librdmacm.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libm.so.6 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libuuid.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libdevmapper.so.1.02.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libargon2.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libjson-c.so.5 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libz.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libbz2.so.1.0 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/liblzma.so.5 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libzstd.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libnl-route-3.so.200 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libnl-3.so.200 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libselinux.so.1 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libpthread.so.0 \
/lib/${LIB_DIR_PREFIX}-linux-gnu/libpcre2-8.so.0 /lib/${LIB_DIR_PREFIX}-linux-gnu
COPY --from=ori-volume-builder /lib64/ld-linux-${LIB_DIR_PREFIX_MINUS}.so.2 /lib64/
RUN mkdir -p /usr/lib/${LIB_DIR_PREFIX}-linux-gnu/ceph/
COPY --from=ori-volume-builder /usr/lib/${LIB_DIR_PREFIX}-linux-gnu/ceph/libceph-common.so.2 /usr/lib/${LIB_DIR_PREFIX}-linux-gnu/ceph

COPY --from=ori-volume-builder /workspace/bin/ori-volume /ori-volume

# Build stage used for validation of the output-image
# See validate-container-linux-* targets in Makefile
FROM ori-volume as validation-image

COPY --from=busybox /usr/bin/ldd /usr/bin/find /usr/bin/xargs /usr/bin/
COPY --from=builder /workspace/hack/print-missing-deps.sh /print-missing-deps.sh
SHELL ["/bin/bash", "-c"]
RUN /print-missing-deps.sh


# Final build stage, create the real Docker image with ENTRYPOINT
FROM ori-volume
USER 65532:65532

ENTRYPOINT ["/ori-volume"]