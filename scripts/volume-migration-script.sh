#!/bin/bash
# ------------------------------------------------------------------
# Volume-migration-script
# ------------------------------------------------------------------

#Pre-requisites
# -----------------------------------------------------------------
# kubectl, onmetal-image(To fetch Snapshot Ref) and ceph(Monitor IPs details) client, bc (Arithmatic operation) need to be installed on env.
# https://github.com/onmetal/onmetal-image
# ----------------------------------------------------------------

#Default dry run parameter set to false
dry_run=false

while getopts 'n' opt; do
    case "$opt" in
        n) dry_run=true ;;
        *) echo 'error in command line parsing' >&2
           exit 1
    esac
done

# Choose the Namespace
namespace_name=rook-ceph

# Get the list of namespaces
namespaces=$(kubectl get namespaces -o name)

# Filter the list of namespaces to only show the namespace with the specified name
filtered_namespaces=$(echo "$namespaces" | grep "$namespace_name")

# Print the filtered list of namespaces
#echo "$filtered_namespaces"
namespace=$(echo "$filtered_namespaces" | sed 's/.*\/\(.*\)/\1/')


##### List the all Volumes
volumes=$(kubectl get volume -n $namespace  --field-selector=metadata.namespace=rook-ceph -o name)

list=()
# Print the list of volume names
#echo "Running volumes:"
for volume in $volumes; do
    volume_name=$(echo "$volume" | sed 's/^[^/]*//;s|/||g')
    list+=($volume_name)
done

echo "${list[@]}"



for val in ${list[@]}; do
	VOLUME_ID=`kubectl get volume $val -n $namespace -o json | jq '.status | .access |.volumeAttributes["image"] | .[5:]'`
	VOLUME_ID_FORMATTED=`echo "$VOLUME_ID" | tr -d '"'`
        VOLUME_NAME=`kubectl get volume  $val -n $namespace -o json | jq '.status | .access | .secretRef["name"]'`
	VOLUME_NAME=$(echo "$VOLUME_NAME" | sed 's/^"//' | sed 's/"$//')
	VOLUME_NAME1="${VOLUME_NAME}\\"
	VOLUME_UUID=`kubectl get volume  $val -n $namespace -o json | jq '.metadata | .uid '`
	VOLUME_UUID=$(echo "$VOLUME_UUID" | sed 's/^"//' | sed 's/"$//')
	VOLUME_UUID="${VOLUME_UUID}\\"
	VOLUME_TIMESTAMP=`kubectl get volume $val -n $namespace -o json | jq '.metadata.creationTimestamp'`
	IMAGE=`kubectl get volume $val -n rook-ceph -o json | jq '.metadata' | jq '.annotations' | jq -rc '."kubectl.kubernetes.io/last-applied-configuration"' | jq '.spec' | jq '.image'`
	IMAGE1=`echo "$IMAGE" | tr -d '"'`

	#Pull the image first
	onmetal-image pull $IMAGE1

        SNAPSHOT_REF=`onmetal-image inspect $IMAGE1 | jq .descriptor | jq .digest`
	USERKEY=`ceph auth get-or-create-key client.volume-rook-ceph--ceph`
	HANDLE=`kubectl get volume  $val -n $namespace -o json | jq '.status | .access |.volumeAttributes["image"]'`
	WWN=`kubectl get volume $val  -n $namespace -o json | jq .status |jq .access |jq .handle`
	NAMESPACE="${namespace}\\"


	MONITOR=$(ceph quorum_status | jq .monmap.mons | jq .[] | jq .addr)
	MONITOR=$(echo "${MONITOR[@]}" | sed 's/:6789/:6789,/g' | sed 's/$/"/')
	MONITOR=$(echo $MONITOR | sed 's/\/\0//g')
	MONITOR=$(echo "$MONITOR" | sed 's/,//g')
	MONITOR=$(echo "$MONITOR" | sed 's/ /, /g')
	MONITOR=$(echo "$MONITOR" | sed 's/"//g; s/"//g')


	SIZE=`kubectl get volume $val  -n $namespace -o json | jq '.spec.resources.storage'`
	SIZE=$( echo "$SIZE" | sed 's/Gi/1073741824/g' | bc )

        VOLUMECLASS=`kubectl get volume  $val -n $namespace  -o json | jq .spec.volumeClassRef.name`
        VOLUMECLASS=`echo $VOLUMECLASS | sed 's/"//g'`
	IOPS=`kubectl get volumeclass $VOLUMECLASS -o json | jq .capabilities.iops`
	IOPS=`echo $IOPS | sed 's/"//g'`
	TPS=`kubectl get volumeclass $VOLUMECLASS -o json | jq .capabilities.tps`
	TPS=`echo $TPS | sed 's/"//g'`
	DEFAULT_BURST_DURATION_SEC=15
	DEFAULT_BURST_FACTOR=10

	IOPS_BURST_LIMIT=`expr $DEFAULT_BURST_FACTOR \* $IOPS`
	#echo $IOPS_BURST_LIMIT
	TPS_BURST_LIMIT=`expr $DEFAULT_BURST_FACTOR \* $TPS`
        #echo $TPS_BURST_LIMIT

	JSON_STR='{
  		"metadata": {
     			"id": '$VOLUME_ID',
     			"annotations": {
        		"cephlet.api.onmetal.de/annotations": "null",
			"cephlet.api.onmetal.de/labels": "{\"volumepoollet.api.onmetal.de/volume-name\":\"'$VOLUME_NAME1'",\"volumepoollet.api.onmetal.de/volume-namespace\":\"'$NAMESPACE'",\"volumepoollet.api.onmetal.de/volume-uid\":\"'$VOLUME_UUID'"}"
     		},
     		"labels": {
        		"cephlet.api.onmetal.de/class": "fast",
        		"cephlet.api.onmetal.de/manager": "cephlet-volume"
     		},
     		"createdAt": '$VOLUME_TIMESTAMP',
     		"generation": 0,
    		"finalizers": [
        		"image"
     			]
  		},
  		"spec": {
     			"size": '$SIZE',
     		"limits": {
        		"rbd_qos_bps_burst": '$TPS_BURST_LIMIT',
        		"rbd_qos_bps_burst_seconds": '$DEFAULT_BURST_DURATION_SEC',
        		"rbd_qos_bps_limit": '$TPS',
        		"rbd_qos_iops_burst": '$IOPS_BURST_LIMIT',
        		"rbd_qos_iops_burst_seconds": '$DEFAULT_BURST_DURATION_SEC',
        		"rbd_qos_iops_limit": '$IOPS',
        		"rbd_qos_read_bps_burst": '$TPS_BURST_LIMIT',
        		"rbd_qos_read_bps_limit": '$TPS',
        		"rbd_qos_read_iops_burst": '$IOPS_BURST_LIMIT',
        		"rbd_qos_read_iops_limit": '$IOPS',
        		"rbd_qos_write_bps_burst": '$TPS_BURST_LIMIT',
        		"rbd_qos_write_bps_limit": '$TPS',
        		"rbd_qos_write_iops_burst": '$IOPS_BURST_LIMIT',
        		"rbd_qos_write_iops_limit": '$IOPS'
     		},
     		"image": '$IMAGE',
     		"snapshotRef": '$SNAPSHOT_REF',
     		"encryption": {
        		"type": "Unencrypted",
        		"encryptedPassphrase": null
     			}
  		},
  		"status": {
     		"state": "Available",
     		"access": {
        		"monitors": "'$MONITOR'",
        		"handle": '$HANDLE',
        		"user": "ceph",
        		"userKey": "'$USERKEY'",
        		"wwn": '$WWN'
     			}
   		}
 	}'

	> vol-$VOLUME_ID_FORMATTED.json
	echo $JSON_STR >> vol-$VOLUME_ID_FORMATTED.json

	if ! $dry_run; then
		rados setomapval onmetal.csi.volumes $VOLUME_ID  --pool=ceph --input-file vol-$VOLUME_ID_FORMATTED.json
		echo "Updated the OMAP data for volume $VOLUME_NAME Successfully" 
	fi
        
	#Delete the OCI image
        onmetal-image delete $IMAGE1	
done
