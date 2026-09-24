# ceph-provider

[![REUSE status](https://api.reuse.software/badge/github.com/ironcore-dev/ceph-provider)](https://api.reuse.software/info/github.com/ironcore-dev/ceph-provider)
[![GitHub License](https://img.shields.io/static/v1?label=License&message=Apache-2.0&color=blue)](LICENSE)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](https://makeapullrequest.com)

`ceph-provider` is a Ceph based provider implementation of the [ironcore](https://github.com/ironcore-dev/ironcore) `Volume` 
and `Bucket` types.

Please consult the [project documentation](https://ironcore-dev.github.io/ceph-provider/) for additional information.

## Rook monitor discovery

Set `--ceph-mon-configmap-namespace=rook-ceph` (and `--kubeconfig` when not running in-cluster) to read monitor addresses from `rook-ceph-mon-endpoints`. Apply `config/ceph-volume-provider/rook-mon-rbac`. The Role namespace and `resourceNames` must match `--ceph-mon-configmap-namespace` and `--ceph-mon-configmap-name`.

A parsed monitor list that differs from the list used at startup stops the process. Kubernetes starts the container again. The new process connects with the updated list and writes that list onto existing volumes when an address was added or removed. The same addresses in another order are left unchanged.

A running guest keeps the monitor hosts already configured on its disk until libvirt applies a monitor change to a disk that is already attached.

Volumes updated from the ConfigMap keep that address list if this binary is rolled back. The previous binary does not walk existing volumes.

ConfigMap addresses must be reachable from hypervisors. `--ceph-monitors` remains valid for non-Rook deployments and integration tests. Leave `--ceph-mon-configmap-namespace` empty to keep that static behavior.

## Getting Started
You’ll need a Kubernetes cluster to run against. You can use [KIND](https://sigs.k8s.io/kind) to get a local cluster for testing, or run against a remote cluster.
**Note:** Your controller will automatically use the current context in your kubeconfig file (i.e. whatever cluster `kubectl cluster-info` shows).

### Running on the cluster
1. Install Instances of Custom Resources:

```sh
kubectl apply -f config/samples/
```

2. Build and push your image to the location specified by `IMG`:
	
```sh
make docker-build docker-push IMG=<some-registry>/ceph-provider:tag
```
	
3. Deploy the controller to the cluster with the image specified by `IMG`:

```sh
make deploy IMG=<some-registry>/ceph-provider:tag
```

### Undeploy controller
UnDeploy the controller to the cluster:

```sh
make undeploy
```

## Contributing

We'd love to get feedback from you. Please report bugs, suggestions or post questions by opening a GitHub issue.

### How it works
This project aims to follow the Kubernetes [Operator pattern](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/)

It uses [Controllers](https://kubernetes.io/docs/concepts/architecture/controller/) 
which provides a reconcile function responsible for synchronizing resources untile the desired state is reached on the cluster 

### Test It Out

Run your controller (this will run in the foreground, so switch to a new terminal if you want to leave it running):

```sh
make run
```

**NOTE:** You can also run this in one step by running: `make install run`

**NOTE:** Run `make --help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## Licensing

Copyright 2025 SAP SE or an SAP affiliate company and IronCore contributors. Please see our [LICENSE](LICENSE) for
copyright and license information. Detailed information including third-party components and their licensing/copyright
information is available [via the REUSE tool](https://api.reuse.software/info/github.com/ironcore-dev/ceph-provider).
