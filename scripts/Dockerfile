FROM ubuntu:latest

RUN apt-get update && apt-get install -y \
  bc \
  ceph-common \
  curl  \
  unzip

RUN curl -LO https://storage.googleapis.com/kubernetes-release/release/v1.23.5/bin/linux/amd64/kubectl

RUN chmod +x kubectl

RUN mv kubectl /usr/bin/kubectl

COPY script.sh /

CMD ["/bin/bash", "/volume-migration-script.sh"]