---
title- Storage Encryption

creation-date- 2022/12/29

status- implementable

authors-
- @pradumnapandit
- @aditya-dixit99
- @DivyaD211093

reviewers-
- @manuel
- @gehoern
- @schucly
- @lukasfrank


---

# Storage Encryption

## Table of Contents

- [Summary](#summary)
- [Motivation](#motivation)
    - [Goals](#goals)
    - [Non-Goals](#goals)
    - [Details](#details)
- [Proposal](#proposal)
- [Appendices](#appendices)


## Summary
The primary purpose of encryption is to protect the confidentiality of digital data stored on a device. Encryption removes the risk of data breach and unauthorized access. The proposal for storage encryption would outline the specific encryption method, key management, and other details that would be used to secure the data being stored. This can include policies and procedures for securing the encryption keys, as well as details on how the encryption will be implemented and managed. Overall, the goal of the proposal is to ensure that the data stored is protected against unauthorized access or disclosure. This proposal focuses on providing option to enable encryption for individual Volumes.


## Motivation
- Security: Security is an important concern and should be a strong focus of any Cloud Native IaaS. To protect the data against variety of security threats, including        data breaches, hacking and physical theft, encryption plays vital role.
- Privacy: Encryption helps to maintain privacy of data from being accessed by unauthorised parties.
- Compliance and business cotinuity: In order to meet compliance requirements of businesses keeping the standards and regulations in mind, it becomes essential to          store the encryption enabled data in the cloud so that in the events of disaster and data theft, organizations can recover the data quickly.

Overall, the motivation for a storage encryption proposal is to provide a holostic and secure approach to protect sensitive data and ensure compliance with regulations and standards.


### Goals
- User should be able to create encrypted volumes (https://github.com/onmetal/onmetal-api/blob/main/docs/proposals/06-storage-encryption.md)
- Encrypt block device (RBD image) that CEPH exposes. 


### Non-Goals
- OSD level encryption
- Object storage encryption

### Details
As of now two types of encryption is supported by Ceph: 
- OSD Level 
- Block device Level
(Currently we are moving ahead in this proposal with Block device level.)
  
## Proposal
- Data encryption key(DEK) will be provided by user in volume object. Cephlet will internally create a Key encryption key(KEK), encrypt the data encryption key and add     into the metadata. All the changes will take place in cephCSI.

- Currently when `volume` is created corresponding `PVC` is also created with reference to `storageclass`.
    However, With `Encryption` enabled `Volume` there will be a new storageclass named `encrypted-ceph` will be created which will be referenced while 
    creating encrypted PVCs with cephlet generated passphrase. 
 
- Encrypted storage class will be created with additional parameters as below.

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

Additionally Update the rook-ceph-operator-config configmap and patch the following configurations
```
kubectl patch cm rook-ceph-operator-config -nrook-ceph -p $'data:\n "CSI_ENABLE_ENCRYPTION": "true"'
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

The `rook-ceph-block-encrypted` is the encrypted `storage class`.

- The ownership of managing `user-secret-metadata` will be taken by the cephlet.

## Appendices
- Following link will describe the use of passphrase and importance of second level encryption. https://docs.ceph.com/en/quincy/rbd/rbd-encryption/

