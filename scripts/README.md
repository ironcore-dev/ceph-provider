# Volume-migration-script

## Pre-requisites
Build the docker image that includes the script and the required dependencies.
```
cd scripts
docker build --platform=linux/amd64 --progress plain --secret id=github_token,src=/Users/flpeter/.github_pat -t mtr.devops.telekom.de/osc/onmetal/cephlet:migration .
docker push mtr.devops.telekom.de/osc/onmetal/cephlet:migration
```

## Deploy the container with the script
```
# apply kubeconfig secret (not provided via git)
kubectl apply -f ./scripts/migration-config.yaml

# apply the deployment
kubectl apply -f ./scripts/migration.yaml
```

## Run the script with dry run flag and `rook-ceph` namespace
```
/prepare-ceph-config.sh -d dry_run -n volumepoollet-system

# check volume-migration.log for errors 
```

## Adjust deployments on flux landscape 
- disable all volumepoollets
- disable old cephlet 

## Run the script without dry run flag
```
/prepare-ceph-config.sh -n volumepoollet-system
```

## Adjust deployments on flux landscape
- enable the new cephlet-volume
- enable all volumepoollets

## post migration tasks
- check if the ceph client `volume-rook-ceph--ceph` still exists and remove only the ownerReferences from the object
- remove StorageClass `k delete StorageClass volume-rook-ceph--ceph` 
- remove VolumeSnapshotClass volume-rook-ceph--ceph `k delete VolumeSnapshotClass volume-rook-ceph--ceph`
- change ROOK_CSI_ENABLE_RBD=false and CSI_ENABLE_RBD_SNAPSHOTTER=false in rook-ceph-operator-config in the landscape.
- double check that rbd csi driver is gone after flux has synced the changes otherwise next step will result in destroying customer data
- cleanup old VolumeSnapshot
```
k get VolumeSnapshot --no-headers -n volumepoollet-system | awk '{print "kubectl patch VolumeSnapshot -n volumepoollet-system "$1" -p '\'{\\\"metadata\\\":{\\\"finalizers\\\":null}}\'' --type=merge"}' | bash
k get VolumeSnapshot --no-headers -n volumepoollet-system
k get VolumeSnapshot --no-headers -n volumepoollet-system | awk '{print "kubectl delete VolumeSnapshot -n volumepoollet-system "$1}' | bash
```
- cleanup old PVCs
```
kubectl get pvc --no-headers -n volumepoollet-system -o=custom-columns=NS:.metadata.namespace,NAME:.metadata.name,STORAGE:.spec.storageClassName | awk '/volume-rook-ceph--ceph/ {print "kubectl delete pvc -n volumepoollet-system "$2}' | bash
kubectl get pv --no-headers -o=custom-columns=NS:.metadata.namespace,NAME:.metadata.name,STORAGE:.spec.storageClassName,STATUS:.status.phase | grep Released | awk '/volume-rook-ceph--ceph/ {print "kubectl delete pv "$2}' | bash
```
