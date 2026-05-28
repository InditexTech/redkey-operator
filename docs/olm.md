<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Test Operator deployment in Kubernetes using OLM

We will describe how to deploy the Redkey Operator in a Kubernetes cluster using Operator Lifecycle Manager (OLM). Starting from OLM installation, we will deploy the operator and then create a RedkeyCluster resource to verify that the operator is working correctly.

## Table of Contents

- [Installing Operator Lifecycle Manager (OLM)](#installing-operator-lifecycle-manager-olm)
  - [Install using `operator-sdk`](#install-the-operator-lyfecycle-manager-olm-using-operator-sdk)
  - [Install using `kubectl`](#install-the-operator-lyfecycle-manager-olm-using-kubectl)
- [Deploy the Redkey Operator using OLM](#deploy-the-redkey-operator-using-olm)
  - [Use `operator-sdk` to deploy the Redkey Operator bundle](#use-operator-sdk-to-deploy-the-redkey-operator-bundle)
  - [Use Kubernetes manifests to deploy the Redkey Operator bundle](#use-kubernetes-manifests-to-deploy-the-redkey-operator-bundle)
    - [Adding the catalog containing the operator to OLM](#adding-the-catalog-containing-the-operator-to-olm)
    - [Check the available operators in the catalog](#check-the-available-operators-in-the-catalog)
    - [Create an OperatorGroup](#create-an-operatorgroup)
    - [Create a Subscription to the operator](#create-a-subscription-to-the-operator)
    - [Deploy a Redkey Cluster using the Operator](#deploy-a-redkey-cluster-using-the-operator)

---

## Installing Operator Lifecycle Manager (OLM)

If you are not using OpenShift, you can install OLM in your Kubernetes cluster by following the following instructions.

We assume you already have a Kubernetes cluster up and running, and this cluster is currently selected as your `current-context` vie `kubectl`. If you don't have one, you can create a local cluster using tools like `Kind`, `K3s`, or `Minikube`.

We will use `Kind` in this example, as this is the tool we propose.

Create a local Kubernetes cluster:

```shell
make setup-cluster
```

Operator Lifecycle Manager (OLM) can be installed using either `kubectl` or `operator-sdk`.

### Install the Operator Lyfecycle Manager (OLM) using `operator-sdk`

```shell
make olm-install
```

Verify that OLM is installed correctly:

```shell
operator-sdk olm status
```

This command should show you the status of OLM components, including the `catalog-operator`, `olm-operator`, and `packageserver`.

```shell
INFO[0000] Fetching CRDs for version "v0.28.0"
INFO[0000] Fetching resources for resolved version "v0.28.0"
INFO[0000] Successfully got OLM status for version "v0.28.0"

NAME                                            NAMESPACE    KIND                        STATUS
global-operators                                operators    OperatorGroup               Installed
olm                                                          Namespace                   Installed
operatorconditions.operators.coreos.com                      CustomResourceDefinition    Installed
catalog-operator                                olm          Deployment                  Installed
olm-operator-binding-olm                                     ClusterRoleBinding          Installed
olmconfigs.operators.coreos.com                              CustomResourceDefinition    Installed
system:controller:operator-lifecycle-manager                 ClusterRole                 Installed
clusterserviceversions.operators.coreos.com                  CustomResourceDefinition    Installed
packageserver                                   olm          ClusterServiceVersion       Installed
operatorgroups.operators.coreos.com                          CustomResourceDefinition    Installed
aggregate-olm-view                                           ClusterRole                 Installed
cluster                                                      OLMConfig                   Installed
aggregate-olm-edit                                           ClusterRole                 Installed
subscriptions.operators.coreos.com                           CustomResourceDefinition    Installed
operators.operators.coreos.com                               CustomResourceDefinition    Installed
olm-operator                                    olm          Deployment                  Installed
installplans.operators.coreos.com                            CustomResourceDefinition    Installed
operatorhubio-catalog                           olm          CatalogSource               Installed
olm-operators                                   olm          OperatorGroup               Installed
operators                                                    Namespace                   Installed
catalogsources.operators.coreos.com                          CustomResourceDefinition    Installed
olm-operator-serviceaccount                     olm          ServiceAccount              Installed
```

### Install the Operator Lyfecycle Manager (OLM) using `kubectl`

```shell
kubectl apply -f https://github.com/operator-framework/operator-lifecycle-manager/releases/download/v0.40.0/crds.yaml
kubectl apply -f https://github.com/operator-framework/operator-lifecycle-manager/releases/download/v0.40.0/olm.yaml
```

Verify that OLM is installed correctly:

```shell
kubectl get pods -n olm
```

You should see the following output, indicating that the OLM components are running:

```shell
NAME                               READY   STATUS    RESTARTS   AGE
catalog-operator-9f6dc8c87-v9ljl   1/1     Running   0          12m
olm-operator-6bccddc987-xz7cv      1/1     Running   0          12m
operatorhubio-catalog-lmcsk        1/1     Running   0          12m
packageserver-7899cbcfc6-gkc2j     1/1     Running   0          12m
packageserver-7899cbcfc6-hpxzw     1/1     Running   0          12m
```

## Deploy the Redkey Operator using OLM

### Use `operator-sdk` to deploy the Redkey Operator bundle

This is the easiest way to deploy the operator using OLM. Using `make` goals, you can execute all the required steps.

We will assume you are using the `Kind` cluster created in the previous step. If you are using another cluster, make sure to adjust the `IMAGE_TAG_BASE` variable to point to a registry that your cluster can access. Here is the complete sequence of commands (skip the `make setup-kind` and `make olm-install` steps if you have already executed them):

```shell
# Create the `Kind` cluster (if you haven't already) with its registry.
make setup-kind

# Deploy OLM.
make olm-install

# Build and push the operator image to the local registry.
make docker-build docker-push

# Build the Operator bundle files.
make bundle

# Build and push the Operator bundle image to the local registry.
make bundle-build bundle-push

# Deploy the Operator bundle using operator-sdk.
make bundle-deploy
```

The Operator should be up and running in the `operators` namespace. The command `kubectl get pods -n operators` should show you the operator pod running:

```shell
NAME                                                              READY   STATUS      RESTARTS   AGE
82e02669894138e3217057b72c0c34993f32f68477f84069c15d5fa44ez7kkm   0/1     Completed   0          23s
localhost-5005-redkey-operator-bundle-v0-2-0                      1/1     Running     0          44s
redkey-operator-controller-manager-9764b7b87-g58nk                1/1     Running     0          13s
```

Now you can proceed to create a `RedkeyCluster` resource to verify that the operator is working correctly.

```shell
make deploy-sample-ephemeral
```

and then check the status of the `RedkeyCluster`:

```shell
$ kubectl get rkcl
NAME                   PRIMARIES   REPLICAS   EPHEMERAL   PURGEKEYS   IMAGE              STORAGE   PHASE         STATUS   SUBSTATUS
redkey-cluster-sample   3           0          true        true        redis:8-bookworm             Configuring
```

To **clean up** the cluster:

```shell
# Undeploy the Operator bundle (with all the resources created: CSV, Subscription, CatalogSource, OperatorGroup, etc.).
make bundle-undeploy

# Uninstall OLM.
make olm-uninstall
```

### Use Kubernetes manifests to deploy the Redkey Operator bundle

#### Adding the catalog containing the operator to OLM

To add the catalog containing the operator to OLM, you need to create a `CatalogSource` resource. This resource defines the source of the operator's catalog, which can be a container image, a local directory, or a gRPC server.

In this example, we will create a `CatalogSource` that points to a container image hosted on ghcr.io (change the version tag as needed). The image should contain the operator's catalog in the format expected by OLM.

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: redkey-catalog
  namespace: olm
spec:
  displayName: Redkey Operator Catalog
  publisher: InditexTech
  sourceType: grpc
  image: ghcr.io/inditextech/redkey-operator-catalog:v0.1.0
```

Apply the `CatalogSource` resource to your cluster:

```shell
kubectl apply -f catalogSource.yaml
```

Verify that the catalog is added correctly:

```shell
$ kubectl get catalogsource -n olm
NAME                               READY   STATUS    RESTARTS   AGE
redkey-catalog-vvcnj               1/1     Running   0          14s
[...]
```

Verify the health of the catalog:

```shell
kubectl get catalogsource redkey-catalog -n olm -o yaml
```

#### Check the available operators in the catalog

You can inspect the loaded `packagemanifests` list to check the available operators in the catalog:

```shell
$ kubectl get packagemanifests -n olm | grep redkey-operator
redkey-operator                                                   4m37s
```

#### Create an OperatorGroup

The namespaces where the operator will be installed must be defined in an `OperatorGroup` resource. This resource defines the scope of the operator, which can be a single namespace, multiple namespaces, or the entire cluster.

Create an `OperatorGroup` resource that targets the `default` namespace:

```yaml
apiVersion: operators.coreos.com/v1alpha2
kind: OperatorGroup
metadata:
  name: redkey-operatorgroup
  namespace: default
spec:
  targetNamespaces:
  - default
```

Create a file named `operatorGroup.yaml` with the above content and apply it to your cluster:

```shell
kubectl apply -f operatorGroup.yaml
```

#### Create a Subscription to the operator

Finally, you need to create a `Subscription` resource that defines the operator you want to install, the channel you want to subscribe to, and the source of the catalog.

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: redkey-operator-subscription
  namespace: default
spec:
  channel: alpha
  name: redkey-operator
  startingCSV: redkey-operator.v0.1.0
  source: redkey-catalog
  sourceNamespace: olm
```

Create a file named `subscription.yaml` with the above content and apply it to your cluster:

```shell
kubectl apply -f subscription.yaml
```

An `InstallPlan` will be automatically created and executed to install the operator. You can check the status of the `InstallPlan` to see if the operator is being installed correctly:

```shell
kubectl get installplan -n default
```

Then, you can check the status of the `ClusterServiceVersion` (CSV) to see if the operator is running:

```shell
$ kubectl get clusterserviceversion -n default -w
NAME                     DISPLAY           VERSION   REPLACES   PHASE
redkey-operator.v0.1.0   Redkey Operator   0.1.0                
redkey-operator.v0.1.0   Redkey Operator   0.1.0                
redkey-operator.v0.1.0   Redkey Operator   0.1.0                
redkey-operator.v0.1.0   Redkey Operator   0.1.0                Pending
redkey-operator.v0.1.0   Redkey Operator   0.1.0                Pending
redkey-operator.v0.1.0   Redkey Operator   0.1.0                InstallReady
redkey-operator.v0.1.0   Redkey Operator   0.1.0                Installing
redkey-operator.v0.1.0   Redkey Operator   0.1.0                Installing
redkey-operator.v0.1.0   Redkey Operator   0.1.0                Installing
redkey-operator.v0.1.0   Redkey Operator   0.1.0                Succeeded
```

And the Operator should be up and running in the `default` namespace:

```shell
$ kubectl get pods -n default | grep redkey-operator
NAME                               READY   STATUS    RESTARTS   AGE
redkey-operator-749595567c-qdq4g   1/1     Running   0          72s
```

#### Deploy a Redkey Cluster using the Operator

Use the included sample manifests to create a `RedkeyCluster` by executing the following command:

```shell
make deploy-sample-ephemeral
```

You can check the status of the `RedkeyCluster` to see if it is being created correctly:

```shell
$ kubectl get rkcl -o wide -w
NAME                      PRIMARIES   REPLICAS   EPHEMERAL   PURGEKEYS   IMAGE              STORAGE   STORAGECLASSNAME   DELETEPVC   STATUS   SUBSTATUS   PARTITION
redis-cluster-ephemeral   3           0          true        true        redis:8-bookworm                                false                            
redis-cluster-ephemeral   3           0          true        true        redis:8-bookworm                                false                            
redis-cluster-ephemeral   3           0          true        true        redis:8-bookworm                                false                            
redis-cluster-ephemeral   3           0          true        true        redis:8-bookworm                                false       Initializing               
redis-cluster-ephemeral   3           0          true        true        redis:8-bookworm                                false       Configuring                
redis-cluster-ephemeral   3           0          true        true        redis:8-bookworm                                false       Configuring                
redis-cluster-ephemeral   3           0          true        true        redis:8-bookworm                                false       Configuring                
redis-cluster-ephemeral   3           0          true        true        redis:8-bookworm                                false       Ready
```
