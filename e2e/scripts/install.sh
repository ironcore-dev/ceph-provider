#!/bin/bash

curl -LO https://storage.googleapis.com/minikube/releases/latest/minikube-linux-amd64
sudo install minikube-linux-amd64 /usr/bin/minikube
echo "Installation of minikube is completed..."
minikube version
rm -rf minikube-linux-amd64

echo "##############"

sudo apt install qemu-kvm libvirt-daemon-system libvirt-clients bridge-utils -y

sudo adduser $1 libvirt
sudo adduser $1 kvm


sudo curl -LO "https://dl.k8s.io/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
sudo install -o root -g root -m 0755 kubectl /usr/bin/kubectl
echo "Installation of kubectl is completed..."
kubectl version
rm -rf kubectl

echo "**************"

wget https://go.dev/dl/go1.20.2.linux-amd64.tar.gz
sudo rm -rf /usr/local/go 
sudo tar -C /usr/local -xzf go1.20.2.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
echo "Installation of Go is completed..."
go version
rm -rf go1.20.2.linux-amd64.tar.gz

echo "@@@@@@@@@@@@@@@@@@"

go install github.com/onsi/ginkgo/v2/ginkgo@latest
sudo apt install golang-ginkgo-dev -y
echo "Starting Minikube"
minikube start --disk-size=2g --extra-disks=1 --driver qemu2