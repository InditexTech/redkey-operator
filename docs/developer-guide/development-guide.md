<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Development Guide

We have created this guide to help you get started with the development of the Redkey Operator. It includes instructions on how to set up your local environment, deploy the operator, and run tests.

We tried to make it as comprehensive as possible, but if you have any questions or suggestions, please don't hesitate to reach out to us.

## Table of Contents

- [Requirements](#requirements)
- [Devcontainers](#devcontainers)
- [Local development](#local-development)
  - [Create your local Kubernetes cluster](#create-you-local-kubernetes-cluster)
  - [Run the Operator in your local cluster](#run-the-operator-in-your-local-cluster)
    - [Run the Operator from your local machine](#run-the-operator-from-your-local-machine)
    - [Build and push the Operator image](#build-and-push-the-operator-image)
    - [Build and push the Robin image](#build-and-push-the-robin-image)
  - [Deploy an example Redkey Cluster](#deploy-an-example-redkey-cluster)
  - [Cleanup](#cleanup)
- [Local testing](#local-testing)
  - [Unit tests](#unit-tests)
  - [Integration tests](#integration-tests)
  - [End-to-end tests](#end-to-end-tests)
    - [GitHub Actions E2E workflow](#github-actions-e2e-workflow)
    - [Parallel execution](#parallel-execution)
    - [E2E environment variables](#e2e-environment-variables)
    - [Resource optimization](#resource-optimization)
    - [Available labels](#available-labels)
  - [Chaos tests](#chaos-tests)

---

## Requirements

We recommend using a local Kubernetes cluster for development and testing. You can use tools like Kind, Docker Desktop, Rancher Desktop, or K3D to create a local cluster.

`Kind` is the tool we chose for this project, but we do our best to make the development guide compatible with other tools. The following sections will provide instructions for setting up a local Kubernetes cluster using `Kind`, but you can adapt them to your preferred tool.

The main development operations can be accomplished with the `Makefile` commands. The required tools will be downloaded and installed in the `bin` directory by the corresponding `make` goals. However, you can also install them manually if you prefer and your installation will be used by the `Makefile` commands.

The required tools are:

- [`Go`](https://go.dev/) — the programming language used to build the operator
- [`Make`](https://www.gnu.org/software/make/) — used to run the development workflow commands defined in the `Makefile`
- [`Docker`](https://docs.docker.com/) or [`Podman`](https://podman.io/) — container engine for building and pushing operator images
- [`kubectl`](https://kubernetes.io/docs/reference/kubectl/) — Kubernetes CLI for interacting with the cluster
- [`oc`](https://docs.openshift.com/container-platform/latest/cli_reference/openshift_cli/getting-started-cli.html) — OpenShift CLI, required for OpenShift and OLM-based deployments

The tools that will be **downloaded and installed by `make`** in the `bin` directory are:

- [`kind`](https://sigs.k8s.io/kind) — local Kubernetes clusters using Docker
- [`kustomize`](https://sigs.k8s.io/kustomize) — Kubernetes manifest customization
- [`controller-gen`](https://sigs.k8s.io/controller-tools) — CRD and RBAC manifest generation
- [`setup-envtest`](https://sigs.k8s.io/controller-runtime/tools/setup-envtest) (derived from `controller-runtime` module version) — downloads Kubernetes binaries for integration tests
- [`golangci-lint`](https://github.com/golangci/golangci-lint) — Go linter aggregator
- [`operator-sdk`](https://github.com/operator-framework/operator-sdk) — Operator SDK CLI for OLM bundle management

The project is configured to use [`mise`](https://mise.jdx.dev/) or [`asdf`](https://asdf-vm.com/) for managing Go tool versions. If you have either of these tools installed, it will automatically use the Go version specified in the `.tools-version` file.

## Devcontainers

The project includes a `devcontainer` configuration for Visual Studio Code Remote Containers. This allows you to develop inside a containerized environment with all dependencies pre-installed and consistent across different machines.

## Local development

Base development cycle:

- Create your local Kubernetes cluster (e.g., with Kind)
- Edit the code and implement new features or fix bugs
- Run the Operator in your local cluster
- Deploy a Redkey Cluster to test your changes
- Delete your local Kubernetes cluster when you are done

By default, `Docker` is used as the container engine for building and pushing operator images. If you prefer to use `Podman`, set the `CONTAINER_ENGINE` environment variable to `podman` before running the `make` commands:

```shell
export CONTAINER_ENGINE=podman
```

### Create you local Kubernetes cluster

You can easyly create a local Kubernetes cluster with Kind using the following `make` command:

```shell
make setup-kind
```

This will create a container registry and a Kind cluster configured to use it. The cluster will be named `redkey-operator` and the registry will be available at `localhost:5001`.

Now you can access your cluster with `kubectl` and deploy the Redkey Operator to it. The `Makefile` includes commands to automate these steps, as well as deploying an example Redkey Cluster.

### Run the Operator in your local cluster

First required step is to install the CRDs in the cluster:

```shell
make install
```

This will create the CRDs `Redkey` and `RedkeyConfiguration` in the cluster, which are required to deploy the Redkey Operator and create Redkey Cluster instances.

#### Run the Operator from your local machine

The easiest way to run the Operator in your local cluster is to run the controller manager directly from your local machine. This way you can edit the code and see the changes reflected in the cluster without having to build and push a new image.

```shell
make run
```

#### Build and push the Operator image

If you prefer to run the Operator from a container image, you can build and push the image to your local registry with the following commands:

```shell
make docker-build
make docker-push
make deploy
```

These commands will build the Operator image, push it to the local registry, and deploy it to your cluster in the `redkey-operator` namespace.

When you need to publish a cross-platform image, use `make docker-buildx` instead. The release workflow for the Operator uses this target to publish `linux/amd64` and `linux/arm64` runtime images.

```shell
make docker-buildx IMG=ghcr.io/inditextech/redkey-operator:<tag>
```

#### Build and push the Robin image

Robin lives in the sibling `redkey-robin` repository and imports the API types from `redkey-operator`. To build the Robin image from source, keep both repositories checked out side by side:

```text
<workspace>/
  redkey-operator/
  redkey-robin/
```

Robin now builds from its own repository using a Docker BuildKit named context that points back to the operator checkout. From the operator repository, a typical local flow looks like this:

```shell
pushd ../redkey-robin
make docker-build IMG=localhost:5005/redkey-robin:dev OPERATOR_DIR=../redkey-operator
make docker-push IMG=localhost:5005/redkey-robin:dev
popd
```

If your checkout names or paths differ, adjust `OPERATOR_DIR` accordingly.

For cross-platform publication, Robin also provides `make docker-buildx`, and its release workflow publishes `linux/amd64` and `linux/arm64` images using the same mechanism:

```shell
pushd ../redkey-robin
make docker-buildx IMG=ghcr.io/inditextech/redkey-robin:<tag> OPERATOR_DIR=../redkey-operator
popd
```

### Deploy an example Redkey Cluster

You can deploy example Redkey Clusters from the `config/samples` folder. Each sample demonstrates a different feature:

```shell
# Ephemeral cluster (3 primaries, no persistence, purgeKeysOnRebalance: true)
make deploy-sample-ephemeral

# Cluster with persistent storage (AOF + RDB snapshots, purgeKeysOnRebalance: false)
make deploy-sample-storage

# Cluster with replicas (3 primaries + 1 replica each)
make deploy-sample-replicas

# Cluster with Redis authentication (includes a sample Secret)
make deploy-sample-auth
```

The sample manifests are in `config/samples/{ephemeral,storage,replicas,auth}/redkey.yaml`. You can modify them to test different configurations.

### Interact with the Redkey Cluster

You can interact with the Redkey Cluster using `kubectl` to edit the manifest, check the status of the cluster and its nodes, and access the Redis instances.

In order to launch a Redkey Cluster reconcile loop, you can edit the Redkey manifest with `kubectl edit redkey <cluster-name>` and edit any field in the `spec` section. This will trigger a reconcile loop and you can see the changes reflected in the cluster.

To simulate Robin interactions with the `RedkeyConfiguration` instances you can edit the `status` section with:

```shell
kubectl patch redkeyconfig redkey-cluster-ephemeral-1 --subresource=status --type merge -p '{"status": {"configPhase" : "Applied", "nodes": {}, "status": "Ready", "substatus": {"status": "", "upgradingPartition": 0}}}'
```

### Cleanup

To delete an example Redkey Cluster, run the corresponding undeploy target:

```shell
make undeploy-sample-ephemeral
make undeploy-sample-storage
make undeploy-sample-replicas
make undeploy-sample-auth
```

To delete the Operator and all associated resources from your cluster, you can run the following commands:

```shell
make undeploy
```

To remove the CRDs from your cluster, you can run:

```shell
make uninstall
```

To remove the Kind cluster and the local registry, you can run:

```shell
make kind-cleanup
```

### Observability (Prometheus & Grafana)

You can deploy a full observability stack (Prometheus + Grafana) with pre-configured dashboards for the Redkey Operator and Robin metrics:

```shell
make observability-install
```

This installs `kube-prometheus-stack` via Helm, configures scraping for both the operator and Robin, and provisions Grafana dashboards. Access Grafana at `http://localhost:30300` (credentials: `admin` / `redkey`).

To remove it:

```shell
make observability-uninstall
```

For full details, see the [Observability documentation](../observability.md).

## Local testing

To ensure the quality of the code and the functionality of the Operator, we have implemented a set of tests that can be run locally. These tests include:

- Unit tests
- Integration tests
- End-to-end tests
- Chaos tests

### Unit tests

Unit tests are designed to test individual functions and methods in isolation. They are fast to run and provide quick feedback on the correctness of the code.

They are usually ran with the majority of the `make` commands, but you can also run them separately with:

```shell
make test
```

### Integration tests

Integration tests verify the interactions between different components of the Operator, ensuring they work together as expected.

We provide a suite of integration tests that use the `envtest` framework to simulate a Kubernetes API server and test the Operator's reconciliation logic without needing a full cluster. This allows for faster feedback during development while still providing confidence that the Operator's core logic is functioning correctly.

The suite is located in the `test/integration` directory and can be run with:

```shell
make test-integration
```

You can run Unit and Integration tests together with:

```shell
make test-all
```

### End-to-end tests

E2E tests simulate real-world scenarios, testing the Operator's functionality from start to finish, including interactions with the Kubernetes API and the Redkey cluster.

The E2E tests are located in the `test/e2e` directory. We need a Kubernetes cluster to run them, so make sure the operator and Robin images are already built locally before executing the following command. `make test-e2e` will create the Kind cluster if needed and load both images into it.

```shell
make test-e2e IMAGE_OPERATOR=localhost:5005/redkey-operator:dev IMAGE_ROBIN=localhost:5005/redkey-robin:dev
```

To run only a subset of specs, pass a Ginkgo label filter through `LABEL`:

```shell
make test-e2e IMAGE_OPERATOR=localhost:5005/redkey-operator:dev IMAGE_ROBIN=localhost:5005/redkey-robin:dev LABEL=creation
```

We provide an easy way to prepare and clean up the local Kind cluster:

```shell
# Create the Kind cluster ahead of time if you want to keep it between runs
make setup-test-e2e

# Cleanup the Kind cluster
make cleanup-test-e2e
```

#### GitHub Actions E2E workflow

The `.github/workflows/e2e-tests.yml` workflow in this repository checks out `redkey-operator/` and `redkey-robin/` side by side under `${{ github.workspace }}`. Robin is then built from its own repository with `OPERATOR_DIR=../redkey-operator`.

This layout is important: nesting the Robin checkout inside the operator repository breaks `controller-gen paths="./..."` and similar targets because the operator tooling starts scanning the nested Robin module and hits Robin's `replace ../redkey-operator` directive.

When the workflow needs a Robin ref, it resolves it in this order:

1. The `workflow_dispatch` input `robin_ref`.
2. A line in the pull request body with the form `robin-ref: <ref>`.
3. A branch in `InditexTech/redkey-robin` with the same name as the operator pull request branch, if it exists.
4. `main`.

To force the workflow to test a specific Robin branch from an operator pull request, add a line like this to the PR body:

```text
robin-ref: feature/full-cluster-management
```

#### Parallel execution

E2E tests support parallel execution via Ginkgo's `-procs` flag. Each top-level
`Describe` block runs in its own isolated namespace, making most specs safe for
concurrent execution. Tests that interact with global operator state (Manager,
Operator Resources, Helm Deployment) are marked `Serial` and will never run
concurrently with other specs.

To run tests in parallel, set `TEST_PARALLEL_PROCESS`:

```shell
make test-e2e TEST_PARALLEL_PROCESS=2 IMAGE_OPERATOR=localhost:5005/redkey-operator:dev IMAGE_ROBIN=localhost:5005/redkey-robin:dev
```

When `TEST_PARALLEL_PROCESS=1` (default), tests run serially — this is
recommended for local development to minimize resource usage. In CI (GitHub
Actions), the default is `2` to balance speed and resource constraints on
`ubuntu-24.04` runners (4 vCPUs, 16 GB RAM).

#### E2E environment variables

The following environment variables control E2E test behavior and can be passed
to `make test-e2e` or exported in your shell:

| Variable | Default | Description |
| -------- | ------- | ----------- |
| `TEST_PARALLEL_PROCESS` | `1` | Number of parallel Ginkgo processes. Set to `2` in CI. Each parallel process creates its own namespace with a full Redis cluster, so higher values require proportionally more cluster resources. |
| `GOMAXPROCS` | *(Go default)* | Go runtime parallelism. Should match `TEST_PARALLEL_PROCESS` for optimal performance. |
| `E2E_RECONCILE_INTERVAL` | `5` | Robin reconcile loop interval in seconds. Applies to **all three intervals** (normal, on-error, on-wait). Controls how frequently Robin checks and reconciles the Redis cluster state. Lower values make clusters reach Ready faster but increase CPU usage. |
| `E2E_POLL_INTERVAL` | `3` | Polling interval in seconds for test wait helpers (`WaitForClusterReady`, `WaitForClusterPhase`, etc.). Controls how often the test framework checks whether a condition has been met. |
| `E2E_CREATION_TIMEOUT` | `180` | Maximum seconds to wait for a cluster to reach Ready state after creation. Increase if tests timeout during cluster creation on slow environments. |
| `E2E_HEALTH_TIMEOUT` | `600` | Maximum seconds to wait for health-related operations (remediation, node recovery, pod restart). Increase for tests that involve pod deletions or network disruptions. |
| `IMAGE_OPERATOR` | `localhost:5005/redkey-operator:$(VERSION)` | Operator image loaded into Kind and deployed by the suite. |
| `IMAGE_ROBIN` | `localhost:5005/redkey-robin:$(ROBIN_VERSION)` | Robin image used by the operator when creating Robin deployments. |
| `REDIS_IMAGE` | `redis:8.10` | Redis image for cluster nodes. |
| `CERT_MANAGER_INSTALL_SKIP` | *(unset)* | Set to `true` to skip CertManager installation (if already present). |
| `OPERATOR_DEPLOY_SKIP` | *(unset)* | Set to `true` to skip operator deployment (if already deployed manually). |
| `LABEL` | *(unset)* | Ginkgo label filter to run a subset of specs (e.g. `creation`, `helm`, `health`). |

#### Resource optimization

The E2E framework uses minimal resource requests to allow multiple clusters to
coexist on a single Kind node without overcommitting:

| Component | CPU request | CPU limit | Memory request | Memory limit |
| --------- | ----------- | --------- | -------------- | ------------ |
| Redis pod | 10m | 100m | 32Mi | 64Mi |
| Robin pod | 10m | 100m | 32Mi | 64Mi |
| Operator manager | 10m | 500m | 64Mi | 128Mi |

The Robin reconciler is configured with aggressive intervals during tests
(`reconcileInterval=5s`, `reconcileIntervalOnError=3s`, `reconcileIntervalOnWait=3s`,
`clusterMeetWait=2s`) to minimize time waiting for clusters to converge. These
values can be tuned via `E2E_RECONCILE_INTERVAL`.

#### Available labels

Each test file has a Ginkgo label for selective execution:

| Label | File | Description |
| ----- | ---- | ----------- |
| `creation` | `cluster_creation_test.go` | Cluster creation and Ready state |
| `features` | `cluster_features_test.go`, `additional_features_test.go` | PDB, labels, custom configs, profiling, redis config propagation |
| `lifecycle` | `cluster_lifecycle_test.go` | Cascading deletion |
| `hotreload` | `config_hotreload_test.go` | Runtime config changes (reconciler, cluster, metrics) |
| `helm` | `helm_deploy_test.go` | Helm chart deployment (Serial) |
| `health` | `health_remediation_test.go`, `health_remediation_matrix_test.go` | Meet/forget, slot fix, rebalance across all cluster types |
| `manager` | `manager_test.go` | Operator pod and metrics (Serial) |
| `operator-resources` | `operator_resources_test.go` | Robin deployment/RBAC (Serial) |
| `resilience` | `resilience_test.go` | Robin pod restart recovery |
| `robin-changes` | `robin_changes_test.go` | Robin image and resources updates |
| `superseding` | `superseding_test.go` | Config superseding behavior |
| `validation` | `validation_test.go` | Webhook/admission validations |
| `auth` | `auth_test.go` | Authentication scenarios (with and without replicas, password rotation) |
| `auth+upgrade` | `auth_and_upgrade_test.go` | Combined auth + rolling/fast upgrade scenarios |

### Chaos tests

Chaos tests validate cluster resilience under disruptive conditions: random pod
deletions, scaling, operator restarts, and topology corruption — all while the
cluster is under continuous k6 write/read load. A background disruptor deletes
pods **continuously throughout each operation and its recovery** (paused only
around verification), so faults overlap with the operator/Robin work instead of
firing once per iteration. They live in `test/chaos/` and each spec runs in its
own namespace with a dedicated, namespace-scoped operator (`--watch-namespaces`)
so parallel specs stay isolated.

Build the k6 load image once, then run the suite (it creates the Kind cluster,
loads the operator/Robin/k6 images, installs the CRDs and runs the tests):

```shell
make k6-build
make test-chaos IMAGE_OPERATOR=localhost:5005/redkey-operator:dev IMAGE_ROBIN=localhost:5005/redkey-robin:dev
```

Run a subset of scenarios with a Ginkgo label filter:

```shell
make test-chaos LABEL=topology
```

The chaos environment variables (`CHAOS_ITERATIONS`, `CHAOS_SEED`,
`CHAOS_K6_VUS`, `CHAOS_DISRUPTION_INTERVAL`,
`CHAOS_KEEP_NAMESPACE_ON_FAILED`, `K6_IMG`, …), the full list of scenarios, the
continuous disruption model, and the per-namespace operator isolation model are
documented in the dedicated [Chaos Testing guide](../chaos-testing.md).
