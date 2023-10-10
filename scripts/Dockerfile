FROM golang:1.20-bullseye AS onmetal-image-builder

WORKDIR /tmp/
RUN git clone https://github.com/onmetal/onmetal-image.git
WORKDIR /tmp/onmetal-image
RUN pwd
RUN ls -lah
RUN mkdir -p /bin
RUN make install
RUN make build
RUN chmod +x bin/onmetal-image
RUN mv bin/onmetal-image /usr/bin/onmetal-image

FROM ubuntu:latest
COPY --from=onmetal-image-builder /usr/bin/onmetal-image /usr/bin/

RUN apt-get update && apt-get install -y \
  bc \
  jq \
  ceph-common \
  curl  \
  unzip \
  git   \
  vim   \
  make

RUN curl -LO https://storage.googleapis.com/kubernetes-release/release/v1.25.0/bin/linux/amd64/kubectl

RUN chmod +x kubectl

RUN mv kubectl /usr/bin/kubectl

COPY volume-migration-script.sh /
RUN chmod +x /volume-migration-script.sh

COPY prepare-ceph-config.sh /
RUN  chmod +x /prepare-ceph-config.sh

CMD ["/bin/bash", "/prepare-ceph-config.sh"]