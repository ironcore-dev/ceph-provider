---
title: Storage Encryption

creation-date: 2022-12-29

status: implementable

authors:

- @pradumnapandit
- @aditya-dixit99
- @divya

reviewers:
- @manuel


---

# Storage Encryption

## Table of Contents

- [Summary](#summary)
- [Motivation](#motivation)
    - [Goals](#goals)
    - [Non-Goals](#goals)
    - [Details](#details)
- [Proposal](#proposal)

## Summary
The primary purpose of encryption is to protect the confidentiality of digital data stored on computer systems. Encryption removes the risk of data breach and unauthorized access. It ensures that the data remains secure regardless of the device on which it is stored and accessed. This proposal focuses on providing option to enable encryption for individual Volume as a block device. 


## Motivation
- Security is an important concern and should be a strong focus of any  Cloud Native IaaS. 
- Data breaches and downtime are costly and difficult to manage. 
- In order to meet compliance requirements of businesses to store data encrypted in the cloud. 

### Goals
- To offer  Encryption feature to the block device (RBD image) that CEPH exposes
- User 
- Way to provide user defined keys.


### Non-Goals
- Encryption for whole pool of volumes in ceph or just one volume at a time ??
- OSD level encryption is also supported

### Details
As of now two level encryption is supported by Ceph
- OSD 
- RBD which disk level

## Proposal

- Encrypted storage class will be created by additional parameters as below.

```
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: rook-ceph-block-encrypted
parameters:
  # additional parameters required for encryption
  encrypted: "true"
  encryptionKMSID: "user-secret-metadata"
# ...
```

Additinally Update the rook-ceph-operator-config configmap and patch the following configurations
```
kubectl patch cm rook-ceph-operator-config -nrook-ceph -p $'data:\n "CSI_ENABLE_ENCRYPTION": "true"'
```

```
apiVersion: v1
kind: ConfigMap
metadata:
  name: rook-ceph-csi-kms-config
  namespace: rook-ceph
data:
  config.json: |-
    {
      "user-secret-metadata": {
        "encryptionKMSType": "metadata",
        "secretName": "storage-encryption-secret"
      }
    }
```
