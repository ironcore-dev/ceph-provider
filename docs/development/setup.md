# Local Development Setup

## Prerequisites

* go >= 1.19
* `git`, `make` and `kubectl`
* [Kustomize](https://kustomize.io/)
* [Minikube](https://minikube.sigs.k8s.io/docs/) or a real cluster

## Preperation

### Setup Ceph Cluster

Reference:  [rook docs](https://rook.io/docs/rook/v1.9/Contributing/development-environment/?h=mini#minikube)


### Install cert-manager

If there is no [cert-manager](https://cert-manager.io/docs/) present in the cluster it needs to be installed.

```shell
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.8.0/cert-manager.yaml
```

### Setup `ironcore`

Reference: [ironcore docs](https://github.com/ironcore-dev/ironcore/blob/main/docs/development/setup.md)

### Setup `Rook`

1. Install Rook operator and `CRD`s
```shell
kubectl apply -k ./rook
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
csi-cephfsplugin-b7ktv                          3/3     Running     0          63d
csi-cephfsplugin-provisioner-59499cbcdd-wvnfq   6/6     Running     0          63d
csi-rbdplugin-bs4tn                             3/3     Running     6          63d
csi-rbdplugin-provisioner-857d65496c-mxjp4      6/6     Running     0          63d
rook-ceph-mgr-a-769964c967-9kmxq                1/1     Running     0          26d
rook-ceph-mon-a-66b5cfc47f-8d4ts                1/1     Running     0          63d
rook-ceph-operator-75c6d6bbfc-b9q9n             1/1     Running     0          63d
rook-ceph-osd-0-7464fbbd49-szdrp                1/1     Running     0          63d
rook-ceph-osd-prepare-minikube-7t4mk            0/1     Completed   0          6d8h
```


5. Deploy a `CephCluster`
```shell
kubectl apply -f ./rook/cluster.yaml
```
Ensure that the cluster is in `Ready` phase

```shell
kubectl get cephcluster -A
```

6. Deploy a `CephBlockPool`, `CephObjectStore` & `StorageClass`
```shell
kubectl apply -f ./rook/pool.yaml
```

## Clone the Repository

To bring up and start locally the `ceph-provider` project for development purposes you first need to clone the repository.

```shell
git clone git@github.com:ironcore-dev/ceph-provider.git
cd ceph-provider
```

## Build the `ceph-provider`


1. Build the `ceph-volume-provider`
```shell
make build-volume
```

2. Build the `ceph-bucket-provider`
```shell
make build-bucket
```

## Run the `ceph-volume-provider`

The required `ceph-provider` flags needs to be defined in order to connect to ceph. 

The following command starts a `ceph-volume-provider` and connects to a local `ceph` cluster.
```shell
go run ./cmd/volumeprovider/main.go \
    --address=./iri-volume.sock
    --supported-volume-classes=./classes.json
    --zap-log-level=2
    --ceph-key-file=./key
    --ceph-monitors=192.168.64.23:6789
    --ceph-user=admin
    --ceph-pool=ceph-provider-pool
    --ceph-client=client.ceph-provider-pool
```


Sample `supported-volume-classes.json` file: 
```json
[
  {
    "name": "volumeclass-sample",
    "capabilities": {
      "tps": 262144000,
      "iops": 15000
    }
  }
]
```

The `ceph key` can be retrieved from the keyring by decoding (base64) the keyring and using only the `key`. 
```shell
kubectl get secrets -n rook-ceph rook-ceph-admin-keyring -o yaml
```


## Run the `ceph-bucket-provider`

The required `ceph-provider` flags needs to be defined in order to work with rook.

The following command starts a `ceph-bucket-provider`. 
The flag `bucket-pool-storage-class-name` defines the `StorageClass` and hereby implicit the `CephBlockPool` (see rook [docs](https://rook.io/docs/rook/v1.11/Storage-Configuration/Object-Storage-RGW/object-storage/)). 
```shell
go run ./cmd/bucketprovider/main.go \
    --address=./iri-bucket.sock
    --bucket-pool-storage-class-name=rook-ceph-bucket
```


## Interact with the  `ceph-provider`

### Prerequisites
* irictl-volume
    * locally running or
    * https://github.com/ironcore-dev/ironcore/pkgs/container/ironcore-irictl-volume
* irictl-bucket
    * locally running or
    * https://github.com/ironcore-dev/ironcore/pkgs/container/ironcore-irictl-bucket

### Listing supported `VolumeClass` 
```shell
irictl-volume --address=unix:./iri-volume.sock get volumeclass
Name           TPS         IOPS
volumeclass-sample   262144000   15000
```

### Listing supported `VolumeClass`
```shell
irictl-volume --address=unix:./iri-volume.sock get volumeclass
Name           TPS         IOPS
volumeclass-sample   262144000   15000
```

### Creating a  `Volume`
```shell
irictl-volume --address=unix:./iri-volume.sock create volume -f ./volume.json

Created volume 796264618065bb31024ec509d4ed8a87ed098ee8e89b370c06b0522ba4bf1e2
```

Sample volume.json
```json
{
  "metadata": {
    "labels": {
      "test.api.ironcore.dev/volume-name": "test"
    }
  },
  "spec": {
    "class":  "volumeclass-sample",
    "resources":  {
      "storage_bytes": 10070703360
    }
  }
}
```

### Listing `Volume`s
```shell
irictl-volume --address=unix:./iri-volume.sock get  volume
ID                                                                Class          Image   State              Age
796264618065bb31024ec509d4ed8a87ed098ee8e89b370c06b0522ba4bf1e2   volumeclass-sample           VOLUME_AVAILABLE   2s
```

### Deleting a `Volume`s
```shell
irictl-volume --address=unix:./iri-volume.sock delete  volume 796264618065bb31024ec509d4ed8a87ed098ee8e89b370c06b0522ba4bf1e2
Volume 796264618065bb31024ec509d4ed8a87ed098ee8e89b370c06b0522ba4bf1e2 deleted
```
