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
- [Document Terminology](#documentterminology)
- [Summary](#summary)
- [Motivation](#motivation)
    - [Goals](#goals)
    - [Non-Goals](#goals)
    - [Details](#details)
- [Proposal](#proposal)
    - [Way 1](#way1)
    - [Way 2](#way2)    
- [Appendices](#appendices)

## Document Terminology
- volume encryption: encryption of a volume attached by rbd
- LUKS: Linux Unified Key Setup: stores all the needed setup information for dm-crypt on the disk
- dm-crypt: linux kernel device-mapper crypto target
- cryptsetup: the command line tool to interface with dm-crypt
- DEK : Data Encryption Key
- KEK : Key Encryption Key

## Summary
The primary purpose of encryption is to protect the confidentiality of digital data stored on a device. Encryption removes the risk of data breach and unauthorized access. The proposal for storage encryption would outline the specific encryption method, key management, and other details that would be used to secure the data being stored. This can include policies and procedures for securing the encryption keys, as well as details on how the encryption will be implemented and managed. Overall, the goal of the proposal is to ensure that the data stored is protected against unauthorized access or disclosure. This proposal focuses on providing option to enable encryption for individual Volumes.


## Motivation
- Security: Security is an important concern and should be a strong focus of any Cloud Native IaaS. To protect the data against variety of security threats, including        data breaches, hacking and physical theft, encryption plays vital role.
- Privacy: Encryption helps to maintain privacy of data from being accessed by unauthorised parties.
- Compliance and business continuity: In order to meet compliance requirements of businesses keeping the standards and regulations in mind, it becomes essential to  store the encryption enabled data in the cloud so that in the events of disaster and data theft, organizations can recover the data quickly.

Overall, the motivation for a storage encryption proposal is to provide a holistic and secure approach to protect sensitive data and ensure compliance with regulations and standards.


### Goals
- User should be able to create encrypted volumes (https://github.com/onmetal/onmetal-api/blob/main/docs/proposals/06-storage-encryption.md)
- Encrypt block device (RBD image) that CEPH exposes. 


### Non-Goals
- OSD level encryption
- Object storage encryption

### Details
Ceph supports encryption of data at rest, including images, which can be encrypted using the AES-128 and AES-256 encryption format. Additionally, xts-plain64 is currently the only supported encryption mode. Two types of encryption are supported by Ceph: 
- OSD Level and 
- Block device Level

The volume object is created with a reference to a secret which contains DEK:

```
apiVersion: storage.api.onmetal.de/v1alpha1
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
  encryption:
    secretRef: encryption-key-secret
```

## Proposal
This proposal is divided into two ways of encrypting RBD image. Following two ways explains how RBD Image encryption is carried out.

### Way 1:
### Details
This way emphasizes more on providing encryption on ceph level.

#### Image encryption
- By default, RBD image is not encrypted, to encrypt RBD image following operations need to be performed. Image-level encryption can be handled internally by RBD clients. This means you can set a secret key that will be used to encrypt a specific RBD image.

##### Encryption Format and Encryption Load
- To encrypt an RBD image, it needs to be formatted to one of the supported encryption format and is a necessary pre-requisite for enabling encryption. The format operation persists encryption metadata to the image such as encryption format and version, cipher algorithm and mode specification as well as information used to secure the encryption key. This encryption key is protected by Passphrase (KEK) which is user-kept secret.
- In order to safely perform encrypted IO on the formatted image, an additional encryption load operation should be applied after opening the image.  The encryption load operation requires supplying the encryption format and a secret for unlocking the encryption key(KEK). The encryption key(DEK) will be stored in-memory by the RBD client until the image is closed.
- Encryption load can be automatically applied when mounting RBD images as block devices via rbd-nbd.

##### Supported Formats


### Way 2:
### Details
This way emphasizes more on providing encryption on kubernetes level using CSI, StorageClass, PVC.
 
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
key encryption key is generated by cephlet as

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

