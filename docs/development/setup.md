# Local Development Setup

## Prerequisites

* go >= 1.19
* `git`, `make` and `kubectl`
* [Kustomize](https://kustomize.io/)
* [Minikube](https://minikube.sigs.k8s.io/docs/) or a real cluster

## Preperation

### Setup Ceph Cluster

[//]: # (https://rook.io/docs/rook/v1.9/Contributing/development-environment/?h=mini#minikube)


### Install cert-manager

If there is no [cert-manager](https://cert-manager.io/docs/) present in the cluster it needs to be installed.

```shell
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.8.0/cert-manager.yaml
```

### Setup `onmetal-api`

### Setup `Rook`

1. Install Rook operator and `CRD`s
```shell
kubectl apply \
 -f https://raw.githubusercontent.com/rook/rook/v1.10.8/deploy/examples/crds.yaml \
 -f https://raw.githubusercontent.com/rook/rook/v1.10.8/deploy/examples/common.yaml \
 -f https://raw.githubusercontent.com/rook/rook/v1.10.8/deploy/examples/operator.yaml
```

2. Verify the rook-ceph-operator is in the Running state before proceeding 
```shell
kubectl -n rook-ceph get pod
```

3. Create a Rook Ceph Cluster see: [Rook Docs](https://rook.io/docs/rook/v1.10/Getting-Started/example-configurations/#cluster-crd)

4. Verify cluster installation. List all rook pods again: 
```shell
kubectl -n rook-ceph get pod
```
In the end you should see all pods `Running` or `Completed` and have at least one `rook-ceph-osd-*` Pod:
```
NAME                                            READY   STATUS      RESTARTS   AGE
csi-cephfsplugin-b7ktv                          3/3     Running     6          63d
csi-cephfsplugin-provisioner-59499cbcdd-wvnfq   6/6     Running     136        63d
csi-rbdplugin-bs4tn                             3/3     Running     6          63d
csi-rbdplugin-provisioner-857d65496c-mxjp4      6/6     Running     144        63d
rook-ceph-mgr-a-769964c967-9kmxq                1/1     Running     17         26d
rook-ceph-mon-a-66b5cfc47f-8d4ts                1/1     Running     94         63d
rook-ceph-operator-75c6d6bbfc-b9q9n             1/1     Running     3          63d
rook-ceph-osd-0-7464fbbd49-szdrp                1/1     Running     100        63d
rook-ceph-osd-prepare-minikube-7t4mk            0/1     Completed   0          6d8h
```

## Clone the Repository

To bring up and start locally the `cephlet` project for development purposes you first need to clone the repository.

```shell
git clone git@github.com:onmetal/cephlet.git
cd cephlet
```
