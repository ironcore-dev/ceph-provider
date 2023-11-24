# Usage Guides

This section provides an overview on how `Volume`s from the [ironcore](https://github.com/ironcore-dev/ironcore) project can be provisioned using the `ceph-provider` provider. The samples are equivalent for `Bucket`s. 

## Available Pools and Classes

As a user you can request storage by creating a `Volume`. It will be allocated in the referenced `VolumePool`. 
The `VolumeClasses` define the capabilities in terms of IOPS, BPS limits and other resource requirements. 

Get the available `VolumePools` with the corresponding `VolumeClasses`

```shell
kubectl get volumeclasses 
NAME   AGE
fast   4d18h
slow   4d18h

kubectl get volumepool
NAME   VOLUMECLASSES   AGE
ceph   fast,slow       4d17h
```

## Creating a `Volume`

A `Volume` is referencing a `VolumePool` and a matching `VolumeClass` which the `VolumePool` supports.

```yaml
# sample-volume.yaml
apiVersion: storage.ironcore.dev/v1alpha1
kind: Volume
metadata:
  name: sample-volume
  namespace: default
spec:
  volumeClassRef:
    name: fast
  volumePoolRef:
    name: ceph
  resources:
    storage: 1Gi
```

```shell
kubectl apply -f sample-volume.yaml 
volume.storage.ironcore.dev/sample-volume created
```

## `Volume` Status

Once the `Volume` is provisioned the state will change to `Available`.

```shell
kubectl get volumes
NAMESPACE       NAME            VOLUMEPOOLREF   VOLUMECLASS   STATE       PHASE     AGE
default   sample-volume   ceph            fast          Available   Unbound   4m1s
```

The status of the `Volume` will contain the information which is needed to be able to consume the volume with a ceph client.

```yaml
apiVersion: storage.ironcore.dev/v1alpha1
kind: Volume
metadata:
  name: sample-volume
  namespace: default
spec:
  ...
status:
  access:
    driver: ceph
    secretRef:
      name: sample-volume
    volumeAttributes:
      WWN: f1243b9a192c4825
      image: ceph/csi-vol-ae2bb4d0-2cf1-11ed-a7db-b6307c819ad0
      monitors: '[2a10:afc0:e013:4030::]:6789'
  lastPhaseTransitionTime: "2022-09-05T08:05:48Z"
  phase: Unbound
  state: Available
```

The `secretRef` in the status defines the `secret` with the  access credentials for the specific `Volume`.

```shell
kubectl get secrets
NAME            TYPE     DATA   AGE
sample-volume   Opaque   2      93s
```