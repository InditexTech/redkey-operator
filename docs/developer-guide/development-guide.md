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
  - [Deploy an example Redkey Cluster](#deploy-an-example-redkey-cluster)
  - [Cleanup](#cleanup)
- [Local testing](#local-testing)
  - [Unit tests](#unit-tests)
  - [Integration tests](#integration-tests)
  - [Chaos tests](#chaos-tests)
    - [Chaos test environment variables](#chaos-test-environment-variables)
    - [Relationship between parallelism and timeouts](#relationship-between-parallelism-and-timeouts)

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

This will create the CRDs `RedkeyCluster` and `RedkeyClusterConfiguration` in the cluster, which are required to deploy the Redkey Operator and create Redkey Cluster instances.

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

### Deploy an example Redkey Cluster

You can deploy an example Redkey Cluster from the `config/samples` folder with the following command:

```shell
make deploy-samples
```

This will create a Redkey Cluster with 3 nodes, ephemeral storage, and `purgeKeysOnRebalance` set to `true`. You can modify the sample manifest in `config/samples/redis-cluster-ephemeral.yaml` to test different configurations.

### Interact with the Redkey Cluster

You can interact with the Redkey Cluster using `kubectl` to edit the manifest, check the status of the cluster and its nodes, and access the Redis instances.

In order to launch a Redkey Cluster reconcile loop, you can edit the RedkeyCluster manifest with `kubectl edit redkeycluster <cluster-name>` and edit any field in the `spec` section. This will trigger a reconcile loop and you can see the changes reflected in the cluster.

To simulate Robin interactions with the `RedkeyClusterConfiguration` instances you can edit the `status` section with:

```shell
kubectl patch redkeyclusterconfig redkey-cluster-sample-1 --subresource=status --type merge -p '{"status": {"configPhase" : "Applied", "nodes": {}, "status": "Ready", "substatus": {"status": "", "upgradingPartition": 0}}}'
```

### Cleanup

To delete the example Redkey Cluster, you can run the following command:

```shell
make undeploy-samples
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

End-to-end tests

E2E tests simulate real-world scenarios, testing the Operator's functionality from start to finish, including interactions with the Kubernetes API and the Redkey cluster.

The E2E tests are located in the `test/e2e` directory. We need a Kubernetes cluster to run them, so make sure you have one running (e.g., with Kind), the CRDs are installed and Operator and Robin images are available before executing the following command:

```shell
make test-e2e
```

We provide an easy way to run the E2E tests with a local Kind cluster:

```shell
# Create a Kind cluster (no registry is created with this command, the Operator image will be built and loaded directly into the cluster)
make setup-test-e2e

# Run the E2E tests
make test-e2e

# Cleanup the Kind cluster
make cleanup-test-e2e
```

### Chaos tests

Chaos tests validate operator resilience under disruptive conditions: random pod
deletions, scaling, operator restarts, and topology corruption — all while the
cluster is under k6 write/read load. They live in `test/chaos/` and run via:

```shell
make test-chaos
```

Run a single scenario:

```shell
make test-chaos GINKGO_EXTRA_OPTS='--focus="survives continuous scaling"'
```

#### Chaos test environment variables

The chaos suite runs multiple Ginkgo processes in parallel, each creating its
own isolated namespace with an operator, Robin, and a Redis cluster. The
following variables control test behavior and should be set in your shell or
`.envrc` before running `make test-chaos`:

```shell
export TEST_PARALLEL_PROCESS=8
export GOMAXPROCS=8
export CHAOS_KEEP_NAMESPACE_ON_FAILED=true
export IMG_ROBIN=localhost:5001/redkey-robin:0.1.0
```

| Variable                        | Default                          | Description                                                                                                                                                                                                                                                                                   |
|---------------------------------|----------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TEST_PARALLEL_PROCESS`         | `8`                              | Number of parallel Ginkgo processes (`-procs`). Each process runs a separate test spec in its own namespace. Higher values run more specs concurrently but require more cluster resources. This also applies to E2E tests.                                                                     |
| `GOMAXPROCS`                    | `8`                              | Go runtime parallelism. Should match `TEST_PARALLEL_PROCESS` so each Ginkgo process has a dedicated OS thread. Setting this lower than `TEST_PARALLEL_PROCESS` causes goroutine contention; setting it higher wastes CPU without benefit.                                                      |
| `CHAOS_ITERATIONS`              | `10`                             | Number of chaos loop iterations per test spec. Each iteration performs disruptive actions (scale, delete pods, etc.) and waits for recovery. More iterations increase coverage but extend the total run time proportionally.                                                                    |
| `CHAOS_TIMEOUT`                 | `100m`                           | Maximum wall-clock time Ginkgo allows for the entire chaos suite (`--timeout`). Must be large enough to accommodate `CHAOS_ITERATIONS` x recovery time x number of specs / `TEST_PARALLEL_PROCESS`. With 10 iterations and 8 parallel processes, 100 minutes is typically sufficient.          |
| `CHAOS_SEED`                    | *(auto: Ginkgo random seed)*     | Fixed random seed for reproducibility. When a chaos run fails, the seed is printed in the output so you can replay the exact sequence of random actions.                                                                                                                                      |
| `CHAOS_KEEP_NAMESPACE_ON_FAILED`| *(unset)*                        | When set to any non-empty value, failed test namespaces are preserved instead of deleted. This allows post-mortem inspection of pods, logs, and cluster state with `kubectl`. Remember to clean up namespaces manually afterwards.                                                             |
| `IMG_ROBIN`                     | `ghcr.io/inditextech/redkey-robin:$(ROBIN_VERSION)` | Robin sidecar image. For local development, point this to your local registry (e.g. `localhost:5001/redkey-robin:0.1.0`). Passed to tests as `ROBIN_IMAGE` via `GINKGO_ENV`.                                                                                              |
| `K6_IMG`                        | `localhost:5001/redkey-k6:dev`   | k6 load generator image (built with xk6-redis extension). Build it with `make k6-build` before running chaos tests.                                                                                                                                                                          |

#### Relationship between parallelism and timeouts

`TEST_PARALLEL_PROCESS` controls how many test specs run concurrently. The chaos
suite has 8 specs (4 scenarios x 2 `purgeKeysOnRebalance` modes), so with
`TEST_PARALLEL_PROCESS=8` all specs run in parallel and the total wall-clock
time equals roughly the duration of the slowest single spec.

`CHAOS_TIMEOUT` must account for the worst case: if one spec takes longer than
expected (e.g. slow recovery after a scale-down), the timeout must be generous
enough to avoid killing a spec mid-recovery. As a rule of thumb:

- With `CHAOS_ITERATIONS=10` and `TEST_PARALLEL_PROCESS=8`: `CHAOS_TIMEOUT=100m`
- With `CHAOS_ITERATIONS=5` and `TEST_PARALLEL_PROCESS=4`: `CHAOS_TIMEOUT=60m`

`GOMAXPROCS` should always match `TEST_PARALLEL_PROCESS`. Each Ginkgo process
creates its own Kubernetes clients with independent rate limiters (QPS=5,
Burst=10), so they don't contend on API access — but they do share CPU.
