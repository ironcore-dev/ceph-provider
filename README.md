# ceph-provider

[![REUSE status](https://api.reuse.software/badge/github.com/ironcore-dev/ceph-provider)](https://api.reuse.software/info/github.com/ironcore-dev/ceph-provider)
[![GitHub License](https://img.shields.io/static/v1?label=License&message=Apache-2.0&color=blue)](LICENSE)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](https://makeapullrequest.com)

`ceph-provider` is a Ceph based provider implementation of the [ironcore](https://github.com/ironcore-dev/ironcore) `Volume` 
and `Bucket` types.

Please consult the [project documentation](https://ironcore.dev/iaas/architecture/providers/ceph-provider.html) for additional information.

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

### Integration Tests

The integration tests start a single-node Ceph cluster ([ceph-test](https://github.com/clyso/ceph-test)) with
Docker. The volume provider links against the Ceph client libraries (`librados`, `librbd`). If they are not installed,
e.g. on macOS, run the tests in a container that has them:

```sh
make docker-integration-tests
```

With the libraries installed, run them directly:

```sh
make integration-tests
```

Variables:

- `CEPH_CLIENT_VERSION`: the Ceph release tag of the client libraries in the container, e.g. `v19.2.7`. The default is
  set in the [Dockerfile](Dockerfile), which also uses it for the published images.
- `CEPH_TEST_CLUSTER_VERSION`: the Ceph release tag of the cluster, e.g. `v19.2.7`. Defaults to `CEPH_CLIENT_VERSION`.
- `DEBUG=1` (container only): wait for a debugger to attach on port `2345` before running the tests.

## Licensing

Copyright 2025 SAP SE or an SAP affiliate company and IronCore contributors. Please see our [LICENSE](LICENSE) for
copyright and license information. Detailed information including third-party components and their licensing/copyright
information is available [via the REUSE tool](https://api.reuse.software/info/github.com/ironcore-dev/ceph-provider).
