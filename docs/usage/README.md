# Usage Guides

This section provides an overview on how onmetal Volumes can be created. 

## Available Pools and Classes
As a user you can request storage by creating a `Volume`. It will be placed in the defined `VolumePool`. 
The `VolumeClasses` define the capabilities in terms of IOPS and BPS limits. 

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

## Create a Volume
A `Volume` is always placed in a generated namespace of the customer. 
```shell
kubectl create ns test-customer
```

The following `Volume` is located in the ceph `VolumePool` and uses the fast `VolumeClass`. 
```yaml
apiVersion: storage.api.onmetal.de/v1alpha1
kind: Volume
metadata:
  name: sample-volume
  namespace: test-customer
spec:
  volumeClassRef:
    name: fast
  volumePoolRef:
    name: ceph
  resources:
    storage: 1Gi
```
## Volume Status
Once the volume is provisioned the state will change to `Available`.
```shell
kubectl get volumes -A
NAMESPACE       NAME            VOLUMEPOOLREF   VOLUMECLASS   STATE       PHASE     AGE
test-customer   sample-volume   ceph            fast          Available   Unbound   4m1s
```

The status of the volume will contain the information which is needed to be able to consume the volume with a ceph client.
```yaml
apiVersion: storage.api.onmetal.de/v1alpha1
kind: Volume
metadata:
  name: sample-volume
  namespace: test-customer
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

The `secretRef` in the status defines the `secret` with the  access credentials for the specific volume.
```shell
kubectl get secrets -n test-customer 
NAME            TYPE     DATA   AGE
sample-volume   Opaque   2      93s
```

## Low level resources
Administrators can also observe the rook related resources. 
Every (customer) namespace contain a `cephblockpoolradosnamespaces` and a `cephclients` resource. Under the hood rook 
generates a rados namespace and granting access to it for the specific ceph client user.
```shell
kubectl get cephblockpoolradosnamespaces -A
NAMESPACE   NAME            AGE
rook-ceph   test-customer   94s
```

```shell
kubectl get cephclients.ceph.rook.io -A 
NAMESPACE   NAME            PHASE
rook-ceph   test-customer   Ready
```