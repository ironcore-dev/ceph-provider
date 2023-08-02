FROM ubuntu:latest

RUN apt-get update && apt-get install -y \
  bc \
  ceph-common \
  curl  \
  unzip \
  git   \
  make

# RUN git clone git@github.com:onmetal/onmetal-image.git
# WORKDIR /onmetal-image
# RUN mkdir -p /bin
# RUN make install
# RUN make build 
# RUN chmod +x bin/onmetal-image 
# RUN mv bin/onmetal-image /usr/bin/onmetal-image 

COPY onmetal-image /

RUN curl -LO https://storage.googleapis.com/kubernetes-release/release/v1.23.5/bin/linux/amd64/kubectl

RUN chmod +x kubectl

RUN mv kubectl /usr/bin/kubectl

COPY volume-migration-script.sh /
RUN chmod +x /volume-migration-script.sh

COPY prepare-ceph-config.sh /
RUN  chmod +x /prepare-ceph-config.sh

CMD ["/bin/bash", "/prepare-ceph-config.sh"]