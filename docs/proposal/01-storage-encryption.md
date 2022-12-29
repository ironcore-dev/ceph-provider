---
title: Storage Encryption

creation-date: 2022-12-29

status: implementable

authors:

- @pradumna
- @aditya
- @divya

reviewers:


---

# Storage Encryption

## Table of Contents

- [Summary](#summary)
- [Motivation](#motivation)
    - [Goals](#goals)
    - [Details](#details)
- [Proposal](#proposal)

## Summary
The primary purpose of encryption is to protect the confidentiality of digital data stored on computer systems or transmitted over the internet or any other computer network. Encryption removes the risk of data breach and unauthorized access. It ensures that the data remains secure regardless of the device on which it is stored and accessed.


## Motivation
Security is an important concern and should be a strong focus of any  Storage deployment. Data breaches and downtime are costly and difficult to manage. our business may have compliance requirements or legal obligations to store data encrypted in the cloud.
Being able to say "our data is stored encrypted" makes better positioning.

### Goals
- Encrypt the volume/disk that CEPH exposes
- Encryption for whole pool of volumes in ceph or just one volume at a time ??
- Way to provide user defined keys.


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
