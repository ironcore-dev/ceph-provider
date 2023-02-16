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
- OSD Level and 
- Block device Level
(Currently we are moving ahead in this proposal with Block device level.)

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

### Way 1:
### Details
This way emphasizes more on providing encryption on ceph level.

#### Image encryption
- Image level encryption refers to the process of encrypting an image at the pixel level. It involves transforming the original image into a form that is                   unintelligible to anyone who does not have the proper decryption key. Image-level encryption can be handled internally by RBD clients. This means you can set a         secret key that will be used to encrypt a specific RBD image.

##### Encryption Format
- By default, RBD images are not encrypted. To encrypt an RBD image, it needs to be formatted to one of the supported encryption formats. The format operation persists     encryption metadata to the image. Ceph supports encryption of data at rest, including images, which can be encrypted using the  AES-128 and AES-256 encryption         format. Additionally, xts-plain64 is currently the only supported encryption mode.

- The encryption metadata includes format, version, cipher algorithms, mode specifications and methods to secure the encryption key. The normal encryption format           operation needs to specify encryption format and user-kept secret(usually a passphrase).

- The imagesize of the encrypted image will be lower than the raw image size as a result of storing encryption metadata as a part of image data including an encryption     header will be written to the beginning of the raw image data.

##### Encryption Load
- Formatting an image is a key pre-requisite for enabling encryption. The encryption load is influenced by several factors, including the encryption algorithm used,        the size of the image file, the level of encryption required, and the computing power available. 

- To reduce the encryption load in image encryption, techniques such as parallel processing, distributed computing or hardware-based encryption acceleration can be         used. Parallel processing involves breaking up the image file into smaller parts and encrypting them simultaneously using multiple processors, while                   distributed computing involves spreading the encryption process across multiple computers in a network.

- Though the images are formated, but RBD APIs will treat them as raw unencrypted images. So in this scenario an encrypted RBD image can be opened by the same APIs as      any other image resulting in reading or writing to the raw encrypted data. It will be a risk to the security.

- To eliminate this risk, an additional encryption load operation should be applied after opening the image. The encryption load operation requires supplying the           encryption format and a secret for unlocking the encryption key. After this process, all IOs for the opened image will be encrypted / decrypted. For a cloned           image, this includes IOs for ancestor images as well. Untill the image is closed, the encryption key will be stored in-memory by the RBD client.

- In terms of open image, once the encryption is loaded, no other load or formating can be applied. 

##### Supported Formats
- To perform RBD encryption directly at ceph level is by using the LUKS encryption technology(both LUKS1 and LUKS2 are supported), which is built into the Linux kernel     and can be used to encrypt block devices such as RBD. 

- The new RBD image can be created with RBD command line tool or directly from ceph dashboard. LUKS container on the RBD image can be created using `cryptsetup` tool       which can be further mapped to a block device on the client system.

- To use the LUKS format, start by formatting the image:

   ` $ rbd encryption format {pool-name}/{image-name} {luks1|luks2} {passphrase-file} [–cipher-alg {aes-128 | aes-256}] `
   
- The encryption format operation generates a LUKS header and writes it to the beginning of the image. The header is appended with a single keyslot holding a randomly-     generated encryption key, and is protected by the passphrase read from passphrase-file.
 
- A file system is created on the mapped block device and is mounted as per the requirement. Mounted file system is the point of contact to write or read the data from     encrypted RBD image.

- To mount a LUKS-encrypted image run:

   ` $ rbd -p {pool-name} device map -t nbd -o encryption-format={luks1|luks2},encryption-passphrase-file={passphrase-file} `

- When writing data to the encrypted RBD image, it is automatically encrypted by LUKS before being written to the RBD image. When reading data from the RBD image, it       is decrypted by LUKS on the client system before being made available to the application.

### Way 2:
### Details
This way emphasizes more on providing encryption on kubernetes level.
 
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

