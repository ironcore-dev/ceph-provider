# Local Development Setup

## Prerequisites

* go >= 1.19
* `git`, `make` and `kubectl`
* [Kustomize](https://kustomize.io/)
* [Minikube](https://minikube.sigs.k8s.io/docs/ or a real cluster

## Preperation

### Setup Ceph Cluster

### Setup `onmetal-api`

### Install cert-manager

If there is no [cert-manager](https://cert-manager.io/docs/) present in the cluster it needs to be installed.

```shell
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.8.0/cert-manager.yaml
```

## Clone the Repository

To bring up and start locally the `cephlet` project for development purposes you first need to clone the repository.

```shell
git clone git@github.com:onmetal/cephlet.git
cd cephlet
```
