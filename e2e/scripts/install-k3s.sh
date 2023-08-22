#!/bin/bash

echo "Installing K3S"

echo "##############"

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
# Docker installation

curl -fsSL https://download.docker.com/linux/ubuntu/gpg 
sudo gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg
echo "deb [arch=amd64 signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" | sudo tee /etc/apt/sources.list.d/docker.list &gt; /dev/null
sudo apt-get install apt-transport-https ca-certificates curl gnupg lsb-release -y
sudo apt-get update
sudo apt-get install docker-ce docker-ce-cli containerd.io -y
sudo usermod -aG docker $USER

echo "*****************************"

go install github.com/onsi/ginkgo/v2/ginkgo@latest
sudo apt install golang-ginkgo-dev -y
echo "Starting Minikube"
#minikube start --disk-size=2g --extra-disks=1 --driver qemu2 --force
minikube start driver=docker --force