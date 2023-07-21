# End-to-End Testing

- [End-to-End Testing](#end-to-end-testing)
   - [Introduction](#introduction)
   - [Install Kubernetes](#install-kubernetes)
   - [Deploy Cephlet](#deploy-Cephlet)
   - [Test parameters](#test-parameters)
   - [Running E2E](#running-e2e)


   ## Introduction
   End-to-end (e2e) in Cephlet testing provides a mechanism to test the end-to-end behavior
   of the cephlet, and it will involve the CRUD operations over the volume.
1. Install K8S cluster
2. Install Go.
3. Take pull of new cephlet & onmetal-api
4. Run new cephlet (Cephlet)
5. Run volumepoollet (onmetal-api)
6. Try to do CRUD operations with volume.yaml

   ## Install Kubernetes
   k3s installation script

   ## Deploy Cephlet

   ## Test parameters

   In addition to standard go tests parameters, the following custom parameters are
  available while running tests:

    | flag              | description                                                                                       |
    | ----------------- | ------------------------------------------------------------------------------------------------- |
    | create-volume  | Create the volume with manifest provided |
    | get-volume    | Get the volume  |
    | delete-volume  | Delete the volume with Volume ID  |

   ## Running E2E
