<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Operator deployment

Operator deployment consists of two parts.

- CRDs (Custom Resource Definitions)
- Operator manager

Manifests to deploy the necessary components are located under [config](../../config) folder in the repository.

To facilitate the deployment of the Redkey Operator, the necessary logic has been included in the [Makefile](../../Makefile), so that the operations can be executed using the `make` command, avoiding the need to work with the manifest files.

## Quick deployment using `make`

In order to quickly deploy Redkey Operator into an existing Kubernetes cluster using `make` follow these steps:

Set the needed environment variables:

```shell
export NAMESPACE=<target_namespace>
export PROFILE=pro
export IMG=<your_registry_hostname>:<your_registry_port>/redkey-operator:<image_tag>
```

* *target_namespace*: The name of the namespace in your Kubernetes cluster in which you want to install Redkey Operator. This namespace must exist.
* *your_registry_hostmame*: Hostname of the registry where the operator image will be published.
* *your_registry_port*: Port of the registry where the operator image will be published.
* *image_tag*: Tag name of the operator image you want to publish in your registry.

Build and publish the operator image:

```shell
make docker-build
make docker-push
```

Finally, apply the manifest files to deploy the CRDs and the needed Kubernetes resources:

```shell
make install
make deploy
```
