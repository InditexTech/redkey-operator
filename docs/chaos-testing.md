<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Chaos Testing

![Redkey Operator icon](images/redkey-logo-128.png)

## What chaos tests are

Chaos tests validate that a Redkey cluster keeps serving traffic and self-heals while it is
subjected to disruptive, randomized failures. Unlike unit, integration or end-to-end tests — which
verify *expected* behavior under controlled conditions — chaos tests deliberately inject faults
(killing pods, corrupting the cluster topology, restarting the operator) **while a continuous k6
load** writes and reads keys against the cluster, and then assert that the cluster converges back to
a healthy state.

The suite lives in [`test/chaos/`](../test/chaos) and is built on
[Ginkgo v2](https://onsi.github.io/ginkgo/) and [Gomega](https://onsi.github.io/gomega/), mirroring
the structure of the end-to-end suite under `test/e2e/`.

## Architecture: who heals what

In the current architecture the responsibilities are split:

- **Robin** (a dedicated `Deployment` named `<cluster>-robin`) owns the Redis **topology**. Its
  `HealthReconciler` continuously meets/forgets nodes, fixes slot ownership, rebalances and recovers
  the cluster after corruption.
- **The operator** owns the Kubernetes **objects** (StatefulSet, Service, PodDisruptionBudget,
  Robin Deployment and RBAC) and aggregates the cluster status.

As a result, the topology-corruption scenarios verify that **Robin heals the cluster** once it is
brought back up — not the operator.

## Per-namespace operator isolation

Each chaos spec runs in its **own namespace** with a **dedicated, namespace-scoped operator**. The
suite deploys the operator into the test namespace and starts it with
`--watch-namespaces=<namespace>`, which restricts the manager's cache and reconcilers to that single
namespace. This guarantees that:

- Deleting or scaling the operator in one chaos namespace never affects clusters in other namespaces.
- Parallel specs (`TEST_PARALLEL_PROCESS > 1`) are fully isolated from each other.

The CRDs are cluster-scoped and installed **once** for the whole cluster (via `make install`); only
the operator `Deployment` and its `Role`/`RoleBinding`/`ServiceAccount` are per-namespace. Leader
election is **disabled** for the chaos operator so it restarts quickly after being killed.

## Scenarios

The suite is organized into three top-level `Describe` blocks. Every scenario starts a k6 load
deployment, runs `CHAOS_ITERATIONS` iterations of fault injection, and verifies the cluster recovers
(`status.phase == Ready`, the expected pod count, `redis-cli --cluster check` passes, and no node is
in a `fail`/migrating state) between iterations and at the end.

### Chaos Under Load (`Label: chaos, load` and `chaos, load, nopurge`)

Run with `PurgeKeysOnRebalance=true` (label `load`) and `PurgeKeysOnRebalance=false`
(label `load, nopurge`). When purge is enabled the StatefulSet is recreated on scaling, so the suite
waits for the new StatefulSet to acknowledge the target replica count before touching pods.

| Scenario | What it does |
| -------- | ------------ |
| Continuous scaling + pod deletion | Repeatedly scales the cluster up (3–10 primaries) and down (3–5), deleting random Redis pods each iteration, and verifies `spec.primaries` and health after every change. |
| Operator deletion | Deletes the operator pod and random Redis pods each iteration, verifying recovery. |
| Robin deletion | Deletes all Robin pods and random Redis pods each iteration, verifying recovery. |
| Full chaos | Fires operator deletion, Robin deletion, Redis pod deletion and scaling **in the same iteration** (randomly, possibly all at once) without waiting between actions, testing recovery from accumulated, overlapping failures. |

### Topology Corruption Recovery (`Label: chaos, topology`)

Each scenario scales the operator **and** Robin to zero, injects a topology fault directly with
`redis-cli`, then brings Robin (and the operator) back and asserts that Robin heals the cluster:
all 16384 slots assigned and no node in a fail state.

| Scenario | Injected fault |
| -------- | -------------- |
| Slot ownership conflict | Removes a slot from all nodes and reassigns it inconsistently to two different primaries. |
| Mid-migration slots | Leaves a slot in `migrating`/`importing` state across two nodes, simulating an interrupted resharding. |
| Primary → replica demotion | Flushes a primary's slots and forces it to replicate another primary. |

## How to run

The chaos suite needs a Kubernetes cluster plus the operator, Robin and k6 images. By default it
uses an isolated Kind cluster named `redkey-operator-test-chaos`.

```shell
# 1. Build the k6 load-generator image (xk6 + xk6-redis). Only needed once / when it changes.
make k6-build

# 2. Run the chaos suite. This creates the Kind cluster (if needed), loads the operator, Robin and
#    k6 images, installs the CRDs, and runs the tests.
make test-chaos \
  IMAGE_OPERATOR=localhost:5005/redkey-operator:dev \
  IMAGE_ROBIN=localhost:5005/redkey-robin:dev
```

Run only a subset of scenarios with a Ginkgo label filter via `LABEL`:

```shell
# Only the topology-corruption scenarios
make test-chaos LABEL=topology

# Only the no-purge load scenarios
make test-chaos LABEL=nopurge
```

Prepare or tear down the Kind cluster explicitly:

```shell
make setup-test-chaos    # create the Kind cluster ahead of time
make cleanup-test-chaos  # delete the Kind cluster
```

## Environment variables

| Variable | Default | Description |
| -------- | ------- | ----------- |
| `IMAGE_OPERATOR` | `localhost:5005/redkey-operator:dev` | Operator image deployed (namespace-scoped) into each chaos namespace. |
| `IMAGE_ROBIN` | `localhost:5005/redkey-robin:dev` | Robin image used by the cluster spec. |
| `REDIS_IMAGE` | `redis:8-bookworm` | Redis image for cluster nodes. |
| `K6_IMG` | `localhost:5005/redkey-k6:dev` | k6 load-generator image (built with the xk6-redis extension via `make k6-build`). |
| `CHAOS_ITERATIONS` | `3` | Number of fault-injection iterations per scenario. More iterations increase coverage and run time. |
| `CHAOS_SEED` | *(auto: Ginkgo random seed)* | Fixed RNG seed for reproducibility. The seed used is printed at suite start so a failing run can be replayed. |
| `CHAOS_K6_VUS` | `10` | Number of k6 virtual users generating load. |
| `CHAOS_KEEP_NAMESPACE_ON_FAILED` | *(unset)* | When set to a truthy value, the namespace of a failed spec is preserved instead of deleted, for post-mortem inspection with `kubectl`. Clean it up manually afterwards. |
| `TEST_PARALLEL_PROCESS` | `1` | Number of parallel Ginkgo processes. Each process runs a scenario in its own isolated namespace with a dedicated operator, so higher values require proportionally more cluster resources. |
| `LABEL` | *(unset)* | Ginkgo label filter (`load`, `nopurge`, `topology`, `chaos`). |

## Diagnosing failures

When a spec fails, the suite prints a diagnostics block containing the cluster phase and conditions,
the pod inventory, and the last log lines of the operator, Redis and Robin pods. Set
`CHAOS_KEEP_NAMESPACE_ON_FAILED=true` to keep the failed namespace alive and inspect it directly:

```shell
kubectl get redkeycluster,pods -n <namespace>
kubectl exec -n <namespace> <redis-pod> -- redis-cli --cluster check localhost:6379
kubectl logs -n <namespace> -l control-plane=redkey-operator
kubectl logs -n <namespace> -l redkey.inditex.dev/component=robin
```
