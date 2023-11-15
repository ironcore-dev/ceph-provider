#!/bin/bash
# ------------------------------------------------------------------
# Volume-migration-script
# ------------------------------------------------------------------

#set -x

#Pre-requisites
# -----------------------------------------------------------------
# kubectl, onmetal-image(To fetch Snapshot Ref) and ceph(Monitor IPs details) client, bc (Arithmatic operation) need to be installed on env.
# https://github.com/onmetal/onmetal-image
# ----------------------------------------------------------------

#Default dry run parameter set to false
dry_run=false

multiply_k8s_values() {
    local value1="$1"
    local value2="$2"

    # Convert k8s style to plain numbers
    local number1=$(echo $value1 | sed -e 's/Ei/*1152921504606846976/' -e 's/Pi/*1125899906842624/' -e 's/Ti/*1099511627776/' -e 's/Gi/*1073741824/' -e 's/Mi/*1048576/' -e 's/Ki/*1024/' -e 's/E/*1000000000000000000/' -e 's/P/*1000000000000000/' -e 's/T/*1000000000000/' -e 's/G/*1000000000/' -e 's/M/*1000000/' -e 's/k/*1000/' | bc -l)
    local number2=$(echo $value2 | sed -e 's/Ei/*1152921504606846976/' -e 's/Pi/*1125899906842624/' -e 's/Ti/*1099511627776/' -e 's/Gi/*1073741824/' -e 's/Mi/*1048576/' -e 's/Ki/*1024/' -e 's/E/*1000000000000000000/' -e 's/P/*1000000000000000/' -e 's/T/*1000000000000/' -e 's/G/*1000000000/' -e 's/M/*1000000/' -e 's/k/*1000/' | bc -l)

    # Multiply
    local result=$(echo "$number1 * $number2" | bc)

    echo $result
}

while getopts d:n:l: opt; do
    case "$opt" in
        d) dry_run=true ;;
        n) namespace=${OPTARG} ;;
        l) list=${OPTARG} ;;
        *) echo 'error in command line parsing' >&2
           exit 1
    esac
done

# Choose the Namespace
###namespace_name=$namespace

# Get the list of namespaces
###namespaces=$(kubectl get namespaces -o name)

# Filter the list of namespaces to only show the namespace with the specified name
###filtered_namespaces=$(echo "$namespaces" | grep "$namespace_name")

# Print the filtered list of namespaces
#echo "$filtered_namespaces"
###namespace=$(echo "$filtered_namespaces" | sed 's/.*\/\(.*\)/\1/')

##### List the all Volumes
volumes=$(kubectl get volume -n $namespace  --field-selector=metadata.namespace=$namespace -o name)

# if list is empty populate with all volumes
if [ -z "$list" ]; then
  for volume in $volumes; do
      volume_name=$(echo "$volume" | sed 's/^[^/]*//;s|/||g')
      list+=($volume_name)
  done
fi

echo "${list[@]}"

ceph quorum_status | jq .monmap.mons | jq .[] | jq .addr > mons.txt
cat mons.txt

##Loop through the list of volumes to migrate
for val in ${list[@]}; do
  kubectl get volume $val -n $namespace -o json > vol-$val.json
  # if exit code is not 0, then write the error to log file and continue
  if [ $? -ne 0 ]; then
    echo "Error in getting the volume $val details" >> volume-migration_err.log
    continue
  fi

  VOLUME_ID=`cat vol-$val.json | jq '.status | .access |.volumeAttributes["image"] | .[5:]'`
  VOLUME_ID_FORMATTED=`echo "$VOLUME_ID" | tr -d '"'`
  VOLUME_NAME=`cat vol-$val.json | jq '.status | .access | .secretRef["name"]'`
  VOLUME_NAME=$(echo "$VOLUME_NAME" | sed 's/^"//' | sed 's/"$//')
  VOLUME_NAME1="${VOLUME_NAME}\\"
  VOLUME_UUID=`cat vol-$val.json | jq '.metadata | .uid '`
  VOLUME_UUID=$(echo "$VOLUME_UUID" | sed 's/^"//' | sed 's/"$//')
  VOLUME_UUID="${VOLUME_UUID}\\"
  VOLUME_TIMESTAMP=`cat vol-$val.json | jq '.metadata.creationTimestamp'`
  IMAGE=`cat vol-$val.json | jq '.spec' | jq '.image'`
  IMAGE1=`echo "$IMAGE" | tr -d '"'`

  ## if IMAGE1 is not empty and not contains null, pull the image and get the snapshot ref
  SNAPSHOT_REF="null"
  if [ ! -z "$IMAGE1" ] && [ "$IMAGE1" != "null" ]; then
    echo "Pulling the image $IMAGE1"
    onmetal-image pull $IMAGE1
    SNAPSHOT_REF=`onmetal-image inspect $IMAGE1 | jq .descriptor | jq .digest`
    echo "Snapshot Ref is $SNAPSHOT_REF"
  fi

  USERKEY=`ceph auth get-or-create-key client.volume-rook-ceph--ceph`
  HANDLE=`cat vol-$val.json | jq '.status | .access |.volumeAttributes["image"]'`
  WWN=`cat vol-$val.json | jq .status |jq .access |jq .handle`
  NAMESPACE="${namespace}\\"

  MONITOR=$(cat mons.txt)
  MONITOR=$(echo "${MONITOR[@]}" | sed 's/:6789/:6789,/g' | sed 's/$/"/')
  MONITOR=$(echo $MONITOR | sed 's/\/\0//g')
  MONITOR=$(echo "$MONITOR" | sed 's/,//g')
  MONITOR=$(echo "$MONITOR" | sed 's/ /, /g')
  MONITOR=$(echo "$MONITOR" | sed 's/"//g; s/"//g')

  SIZE=`cat vol-$val.json | jq '.spec.resources.storage'`
  SIZE=$( echo "$SIZE" | sed 's/Gi/1073741824/g' | bc )

  VOLUMECLASS=`cat vol-$val.json | jq .spec.volumeClassRef.name`
  VOLUMECLASS=`echo $VOLUMECLASS | sed 's/"//g'`
  # if file does not exists create it
  if [ ! -f volclass-$VOLUMECLASS.json ]; then
    kubectl get volumeclass $VOLUMECLASS -o json > volclass-$VOLUMECLASS.json
  fi
  IOPS=`cat volclass-$VOLUMECLASS.json | jq .capabilities.iops`
  IOPS=`echo $IOPS | sed 's/"//g'`
  TPS=`cat volclass-$VOLUMECLASS.json | jq .capabilities.tps`
  TPS=`echo $TPS | sed 's/"//g'`
  DEFAULT_BURST_DURATION_SEC=15
  DEFAULT_BURST_FACTOR=10

  IOPS_BURST_LIMIT=$(multiply_k8s_values $DEFAULT_BURST_FACTOR $IOPS)
  #echo $IOPS_BURST_LIMIT
  TPS_BURST_LIMIT=$(multiply_k8s_values $DEFAULT_BURST_FACTOR $TPS)
  #echo $TPS_BURST_LIMIT

  JSON_STR='{
      "metadata": {
          "id": '$VOLUME_ID',
          "annotations": {
            "cephlet.api.onmetal.de/annotations": "null",
      "cephlet.api.onmetal.de/labels": "{\"volumepoollet.api.onmetal.de/volume-name\":\"'$VOLUME_NAME1'",\"volumepoollet.api.onmetal.de/volume-namespace\":\"'$NAMESPACE'",\"volumepoollet.api.onmetal.de/volume-uid\":\"'$VOLUME_UUID'"}"
        },
        "labels": {
            "cephlet.api.onmetal.de/class": "'$VOLUMECLASS'",
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
            "rbd_qos_bps_limit": '$(multiply_k8s_values 1 $TPS)',
            "rbd_qos_iops_burst": '$IOPS_BURST_LIMIT',
            "rbd_qos_iops_burst_seconds": '$DEFAULT_BURST_DURATION_SEC',
            "rbd_qos_iops_limit": '$(multiply_k8s_values 1 $IOPS)',
            "rbd_qos_read_bps_burst": '$TPS_BURST_LIMIT',
            "rbd_qos_read_bps_limit": '$(multiply_k8s_values 1 $TPS)',
            "rbd_qos_read_iops_burst": '$IOPS_BURST_LIMIT',
            "rbd_qos_read_iops_limit": '$(multiply_k8s_values 1 $IOPS)',
            "rbd_qos_write_bps_burst": '$TPS_BURST_LIMIT',
            "rbd_qos_write_bps_limit": '$(multiply_k8s_values 1 $TPS)',
            "rbd_qos_write_iops_burst": '$IOPS_BURST_LIMIT',
            "rbd_qos_write_iops_limit": '$(multiply_k8s_values 1 $IOPS)'
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
            "user": "volume-rook-ceph--ceph",
            "userKey": "'$USERKEY'",
            "wwn": '$WWN'
          }
      }
  }'

  > vol-$VOLUME_ID_FORMATTED.json
  echo $JSON_STR >> vol-$VOLUME_ID_FORMATTED.json
  echo "Written volume file: vol-$VOLUME_ID_FORMATTED.json"

  if ! $dry_run; then
    rados setomapval onmetal.csi.volumes $VOLUME_ID  --pool=ceph --input-file vol-$VOLUME_ID_FORMATTED.json
    echo "Updated the OMAP data for volume $VOLUME_NAME Successfully"
  fi

  #if [ ! -z "$IMAGE1" ]; then
  #  #Delete the OCI image
  #  onmetal-image delete $IMAGE1
  #fi
done
