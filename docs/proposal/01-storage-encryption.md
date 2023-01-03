---
title: Storage Encryption

creation-date: 2022-12-29

status: implementable

authors:

- @pradumnapandit
- @aditya-dixit99
- @DivyaD211093

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
- User should be able to create encrypted volumes (Link to API team proposal)
- To offer Encryption feature to the block device (RBD image) that CEPH exposes


### Non-Goals
- No OSD level encryption is supported
- Object storage encryption (TDB?)

### Details
As of now two types of encryption is supported by Ceph: 
- OSD Level 
- Block device Level
  - Currently when `volume` is created corresponding `PVC` is also created with reference to `storageclass`.
    However, With `Encryption` enabled `Volume` there will be a new storageclass named `encrypted-ceph` will be created which will be referenced while 
    creating encrypted PVCs with user provided passphrase.
  - To use different passphrase you need to have different storage classes and point to a different K8s secrets csi.storage.k8s.io/node-stage-secret-name and      csi.storage.k8s.io/provisioner-secret-name which carry new passphrase value for encryptionPassphrase key in these secrets
  

## Proposal

- Encrypted storage class will be created with additional parameters as below.

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
```
apiVersion: v1
kind: Secret
metadata:
  name: storage-encryption-secret
  namespace: rook-ceph
stringData:
  encryptionPassphrase: test-encryption
```
