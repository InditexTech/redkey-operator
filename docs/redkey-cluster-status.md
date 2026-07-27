<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Redkey Cluster Phase, Status and Substatus

The Redkey Operator exposes the state of each `Redkey` through three distinct fields —
**Phase**, **Status** and **Substatus** — that answer three different questions, plus an orthogonal
[health axis](#cluster-health-conditions). `Phase` is a user-facing rollup (is the cluster usable?),
`Status` is the detailed operational state of the underlying configuration lifecycle (what operation
is in progress?), and `Substatus` gives step-level visibility into long-running operations such as
scaling and upgrades. Together they let operators and automation understand exactly what the cluster
is doing at any point in time. See [Phase, Status and Substatus](#phase-status-and-substatus) for
the precise semantics and how they relate.

## Table of Contents

- [Phase, Status and Substatus](#phase-status-and-substatus)
- [Status codes](#status-codes)
- [Cluster health conditions](#cluster-health-conditions)
- [Redkey Cluster creation Status transitions](#redkey-cluster-creation-status-transitions)
- [Configuration change detection](#configuration-change-detection)
  - [Change categories](#change-categories)
  - [Status transition rules](#status-transition-rules)
- [Substatus](#substatus)
  - [Substatus values reference](#substatus-values-reference)
  - [Redkey Cluster Scaling Up (Fast scaling)](#redkey-cluster-scaling-up-fast-scaling)
  - [Redkey Cluster Scaling Up (Slow scaling)](#redkey-cluster-scaling-up-slow-scaling)
  - [Redkey Cluster Scaling Down (Fast scaling)](#redkey-cluster-scaling-down-fast-scaling)
  - [Redkey Cluster Scaling Down (Slow scaling)](#redkey-cluster-scaling-down-slow-scaling)
  - [Redkey Cluster Scaling to Zero](#redkey-cluster-scaling-to-zero)
  - [Redkey Cluster Upgrading (Fast upgrading)](#redkey-cluster-upgrading-fast-upgrading)
  - [Redkey Cluster Upgrading (Rolling N+1)](#redkey-cluster-upgrading-rolling-n1)

---

## Phase, Status and Substatus

A `Redkey` reports its state through three fields, each answering a different question:

| Field | JSONPath | Question it answers | Values |
|-------|----------|---------------------|--------|
| **Phase** | `.status.phase` | Is the cluster usable, working on something, or broken? | `Ready`, `Configuring`, `Error` |
| **Status** | `.status.status` | Which lifecycle operation is in progress? | `Initializing`, `Configuring`, `Ready`, `ScalingUp`, `ScalingDown`, `ScalingToZero`, `Upgrading`, `Maintenance`, `Error` |
| **Substatus** | `.status.substatus.status` | Which step *within* the current operation? | e.g. `WaitingForPods`, `Rebalancing`, `Remediating`, … (see the [Substatus reference](#substatus-values-reference)) |

- **Phase** is a **user-facing rollup** derived from the cluster's `Conditions` (`Ready`,
  `ConfigPending`, `Error`). It is the only one of the three shown in the default
  `kubectl get rk` view. Use it for simple “is it ready?” checks and automation.
- **Status** is the **operational state** of the underlying `RedkeyConfig` state machine, set
  by Robin and mirrored onto the cluster from the highest-sequence config. It is the detailed
  lifecycle state (creation, scaling, upgrading, …) described in [Status codes](#status-codes).
- **Substatus** is **purely informational** — it does not drive control flow. Robin sets it to expose
  the current step of a long-running operation (scaling, upgrade, or health remediation).

**Relationship.** `Phase` is *derived*, not stored independently:

- `Status ∈ {Initializing, Configuring, ScalingUp, ScalingDown, ScalingToZero, Upgrading}` ⇒
  `Phase = Configuring`.
- `Status = Ready` and the highest-sequence config is `Applied` ⇒ `Phase = Ready`.
- Any config in `Error` ⇒ `Phase = Error`.

**Lifecycle, not health.** `Phase`/`Status` describe the **configuration lifecycle** — whether the
desired spec has been applied — **not** whether the data plane is fully healthy at that instant. A
cluster can be `Ready` while Robin's health-reconciler is still healing or rebalancing an applied
cluster (surfaced as `Substatus = Remediating`). Live health is exposed on a separate axis via the
[health conditions](#cluster-health-conditions).

> Only the `PHASE` column is shown by `kubectl get rk`. The `STATUS`, `SUBSTATUS` and `PARTITION`
> columns require `kubectl get rk -o wide`.

## Status codes

The `Status` field (`.status.status`) reports the operational lifecycle state of a Redis cluster —
the detailed state machine that `Phase` rolls up (see [Phase, Status and Substatus](#phase-status-and-substatus)).

The implemented status are:

- **Initializing**: The necessary Kubernetes objects have been created (primarily a StatefulSet, which manages the pods on the Redis nodes, and Redkey Robin Deployment). The operator is waiting for all the pods to be ready and Robin to start responding.
- **Configuring**: Robin is responsible for building the cluster, performing the necessary meets between all nodes, assigning slots to ensure everyone is covered, and making sure the cluster is balanced. The operator waits for Robin to confirm that the cluster is ready.
- **Ready**: The cluster has the correct configuration, the desired number of primaries and replicas per primary, is rebalanced and ready to be used. The Redis clusters health in this status will be checked by the Operator periodically asking their Robin services.
  > `Ready` means the highest-sequence `RedkeyConfig` was **applied** — not that the data plane is fully healthy at that exact instant. After a config is applied, Robin's health-reconciler keeps the cluster healthy in the background (healing membership, recovering slot coverage, rebalancing) while the cluster **stays `Ready`**. The live data-plane health is exposed separately through the [health conditions](#cluster-health-conditions), and while remediation is in progress the informational [`Remediating`](#substatus-values-reference) substatus is shown.
- **Upgrading**: The cluster is being upgraded, reconfiguring the objects to solve the mismatches. A Redkey enters this status when:
  - there are differences between the existing configuration in the configmap and the configuration of the Redkey object merged with the default configuration set in the code.
  - there is a mismatch between the StatefulSet object labels and the Redkey Spec labels.
  - a mismatch exists between Redkey resources defined under spec and effective resources defined in the StatefulSet.
  - the images set in Redkey under spec and the image set in the StatefulSet object are not the same.
- **ScalingDown**: The cluster enters in this status to remove excess nodes. Redkey nodes (primaries * replicas per primary) > StatefulSet replicas.
- **ScalingUp**: The cluster enters in this status to create the needed nodes to equal the desired nodes with the current nodes. Redkey primaries * replicas per primary < StatefulSet replicas.
- **ScalingToZero**: The cluster is being scaled to zero primaries. Robin is deleting all its managed objects (StatefulSet, Service, ConfigMap, PDB, optionally PVCs). Once complete, all infrastructure is removed and the cluster reaches Ready with 0 replicas.
- **Error**: An error is detected in the cluster. The operator tries to recover the cluster from error checking the configuration and/or scaling the cluster.
  - Storage capacity mismatch.
  - Storage class mismatch.
  - Scaling up the cluster before upgrading raises an error.
  - Scaling down the cluster after upgradind raises an error.
  - Scaling up when in StatusScalingUp status goes wrong.
  - Scaling down when in StatusScalingDown status goes wrong.

## Cluster health conditions

`Status`/`Phase` describe the **configuration lifecycle**: `Ready` means the highest-sequence
`RedkeyConfig` was **applied**, not that the data plane is fully healthy at that instant.
After a config is applied, Robin's health-reconciler keeps the cluster healthy in the background
(healing membership, recovering slot coverage, rebalancing), and the cluster stays `Ready`
throughout.

To expose the **live data-plane health** independently of the lifecycle, Robin publishes a set of
conditions on each `RedkeyConfig`, which the Operator aggregates onto the `Redkey`
`.status.conditions`. `Status=True` always means the positive condition holds.

| Condition | `True` means |
|-----------|--------------|
| `Healthy` | Rollup — every health check below passed |
| `MembershipHealthy` | All nodes agree on a consistent membership |
| `SlotsCovered` | All 16384 hash slots are assigned |
| `SlotsBalanced` | Slots are evenly distributed across primaries |
| `ReplicasBalanced` | Replicas are correctly spread across primaries |
| `ClusterCheckPassing` | `redis-cli --cluster check` reports no problems |

These conditions are an **orthogonal health axis** and do **not** affect `Phase`
(`Ready`/`Configuring`/`Error`). While a configuration operation is in progress the health report is
stale, so the conditions are reported as `Unknown` (reason `Reconciling`) until the cluster settles
back to `Ready`. While the health-reconciler is actively remediating an applied cluster, the
informational [`Remediating`](#substatus-values-reference) substatus is shown (with `Status` still
`Ready`), so `kubectl get rk -o wide -w` surfaces that the data plane is not yet fully quiescent.

## Redkey Cluster creation Status transitions

The status flow in creating a new Redkey Cluster is as simple as this:

![Redkey Cluster Initialization](./images/redkey-cluster-initialization.png)

The following is an example of the sequence of states that we can see when deploying the sample Redkey Cluster* (command `kubectl get rk -o wide -w`):

```
NAME                      MODE      PRIMARIES   REPLICAS   EPHEMERAL   PURGEKEYS   STORAGE   DELETEPVC   PHASE         STATUS         SUBSTATUS
redis-cluster-ephemeral   cluster   3           0          true        true                  false
redis-cluster-ephemeral   cluster   3           0          true        true                  false       Configuring   Initializing
redis-cluster-ephemeral   cluster   3           0          true        true                  false       Configuring   Configuring
redis-cluster-ephemeral   cluster   3           0          true        true                  false       Configuring   Configuring
redis-cluster-ephemeral   cluster   3           0          true        true                  false       Ready         Ready
```

> **Note:** the `STATUS`, `SUBSTATUS` and `PARTITION` columns (along with `STORAGE` and `DELETEPVC`) are only shown with `-o wide`. The default `kubectl get rk` view shows the `MODE`, `PRIMARIES`, `REPLICAS`, `EPHEMERAL`, `PURGEKEYS` and `PHASE` columns.

\* The sample Redkey Cluster can be deployed from the project code by executing `make deploy-sample-ephemeral`.

## Configuration change detection

When a new `RedkeyConfig` is created for an existing cluster (i.e. there is a previous Applied config), Robin performs an automated **change detection** step before entering the state machine. This avoids unnecessary cluster operations when only lightweight (Robin-internal) settings have changed, and ensures the correct status transition is applied for structural changes.

The detection is implemented in `internal/reconciler/config_changes.go` and the transition logic in `internal/reconciler/status_transitions.go`.

### Change categories

Robin compares the previous (Applied) spec with the target (new) spec field by field and classifies differences into five categories:

| Category | Fields compared | Requires cluster operation? |
|----------|----------------|----------------------------|
| **Robin config** | `RobinConfig.*` (reconciler intervals, metrics, cluster connection, profiling) | No — hot-reloaded at runtime |
| **Topology** | `Primaries`, `ReplicasPerPrimary` | Yes — scaling |
| **Kubernetes** | `Image`, `Labels`, `Resources`, `Override`, `PodDisruptionBudget`, `Storage` | Yes — upgrade |
| **Redis config** | `RedisConfig`, `Version` | Yes — upgrade |
| **Auth** | `Auth` | No — hot-reloaded via CONFIG SET |
| **PurgeKeysOnRebalance** | `PurgeKeysOnRebalance` | Only if combined with topology changes |

Control fields (`Sequence`, `SkipIfSuperseded`, `Ephemeral`) are intentionally **ignored** — they do not affect the running cluster.

### Status transition rules

Once changes are categorized, the status transition is determined by priority:

1. **No changes / Robin-only changes / Auth-only changes** → Config is marked as **Applied** immediately. The cluster stays in its current status (typically Ready). No cluster operation is triggered. Auth changes are applied to all running nodes via `CONFIG SET requirepass` + `CONFIG SET masterauth` before the status is updated.
2. **Topology changes (scaling)** → Takes priority over all other changes.
   - If primaries increased OR (primaries unchanged AND replicas increased) → **ScalingUp**
   - If primaries decreased OR (primaries unchanged AND replicas decreased) → **ScalingDown**
   - If target primaries is 0 → **ScalingToZero** (special teardown flow; see [Scale to Zero](operator-guide/scaling.md#scale-to-zero))
   - When both primaries and replicas change, the primaries delta takes precedence in determining direction.
3. **Kubernetes / Redis config changes (without topology)** → **Upgrading**
4. **PurgeKeysOnRebalance alone** (no topology, no K8s, no Redis config) → Treated as no-op (Applied immediately), since the flag only affects future scaling/upgrade behaviour.

When topology changes are combined with Kubernetes/Redis changes, Robin handles the scaling first. After scaling completes and the cluster returns to Ready, any remaining non-topology changes are detected and trigger the Upgrading status.

## Substatus

To provide more detail about scaling and upgrade operations, a series of Substatus fields have been added to the ScalingUp, ScalingDown, ScalingToZero and Upgrading status fields. This allows you to see the current stage of the operation for the cluster.

Substatus values are **purely informational** — they do not control the reconciliation flow. Robin sets them at the beginning of each significant phase to provide observability via `kubectl get rkc -w`, dashboards, and alerting.

The Substatus that will be applied to scaling operations depends on whether the cluster qualifies for **fast scaling** (ephemeral, no replicas, purgeKeysOnRebalance=true) or uses the normal **slot-migration scaling** path.

### Substatus values reference

| Substatus | Applies to | Meaning |
|-----------|-----------|---------|
| `WaitingForPods` | ScaleUp, FastScaling | StatefulSet updated, waiting for all pods to become Ready |
| `InitializingNodes` | ScaleUp | New nodes being introduced to cluster (CLUSTER MEET + gossip convergence) |
| `Rebalancing` | ScaleUp, ScaleDown | Slot migration in progress (rebalance/reshard) |
| `DrainingPrimaries` | ScaleDown | Slots being migrated away from surplus primaries (weight=0) |
| `RemovingNodes` | ScaleDown | Surplus nodes being forgotten from cluster (CLUSTER FORGET) |
| `ShrinkingStatefulSet` | ScaleDown | StatefulSet being scaled down after nodes removed |
| `AttachingReplicas` | ScaleUp, ScaleDown | Configuring replication topology (CLUSTER REPLICATE) |
| `Verifying` | ScaleUp, ScaleDown | Running cluster health validation before marking Ready |
| `DeletingStatefulSet` | FastScaling | Old StatefulSet being deleted for recreation |
| `RecreatingCluster` | FastScaling | Cluster objects being recreated at new size |
| `FormingCluster` | FastScaling, FastUpgrade | Building/reforming the cluster from scratch on the new nodes |
| `DeletingResources` | ScaleToZero | Kubernetes objects (STS, SVC, CM, PDB) being deleted |
| `DeletingPVCs` | ScaleToZero | PersistentVolumeClaims being removed |
| `AddingExtraNode` | Upgrade (Rolling N+1) | Scaling the StatefulSet +1 (plus replicas) and meeting the extra node(s) |
| `DrainingNode` | Upgrade (Rolling N+1) | Migrating slots from the current partition node to the destination node |
| `RollingUpdate` | Upgrade (Rolling N+1) | Waiting for the partition pod to be recreated with the new spec, then re-joining it |
| `MovingLastSlots` | Upgrade (Rolling N+1) | Migrating slots from the extra node back to node 0 |
| `RemovingExtraNode` | Upgrade (Rolling N+1) | Forgetting the extra node and scaling the StatefulSet back to its original size |
| `FastUpgrading` | Upgrade (Fast) | StatefulSet updated and pods deleted, waiting for recreation with the new image |
| `Remediating` | Ready (health remediation) | The health-reconciler is healing an already-applied cluster (membership, slot coverage or rebalance); `Status` stays `Ready` |

> **Note:** the `FormingCluster` substatus string is reused by both fast scaling and the
> final phase of a fast upgrade (`EndingFastUpgrade`). In both cases it means Robin is
> waiting for the freshly created pods to reform the cluster, cover all slots, and become
> balanced. The high-level `Status` field (`ScalingUp`/`ScalingDown` vs `Upgrading`)
> disambiguates which operation is in progress.

### Redkey Cluster Scaling Up (Fast scaling)

When the cluster qualifies for fast scaling (ephemeral, no replicas, purgeKeysOnRebalance=true), the cluster is recreated from scratch:

**Substatus flow:** `DeletingStatefulSet` → `RecreatingCluster` → `WaitingForPods` → `FormingCluster` → *(cleared)*

![Redkey Cluster Scaling Up Fast](./images/redkey-cluster-substatus-scalingup-fast.png)

This is an example of the Status and SubStatus changes when scaling the sample Redkey Cluster from 3 to 5 primaries:

```
NAME                      MODE      PRIMARIES   REPLICAS   EPHEMERAL   PURGEKEYS   STORAGE   DELETEPVC   PHASE         STATUS      SUBSTATUS
redis-cluster-ephemeral   cluster   3           0          true        true                  false       Ready         Ready
redis-cluster-ephemeral   cluster   5           0          true        true                  false       Ready         Ready
redis-cluster-ephemeral   cluster   5           0          true        true                  false       Configuring   ScalingUp
redis-cluster-ephemeral   cluster   5           0          true        true                  false       Configuring   ScalingUp   DeletingStatefulSet
redis-cluster-ephemeral   cluster   5           0          true        true                  false       Configuring   ScalingUp   RecreatingCluster
redis-cluster-ephemeral   cluster   5           0          true        true                  false       Configuring   ScalingUp   WaitingForPods
redis-cluster-ephemeral   cluster   5           0          true        true                  false       Configuring   ScalingUp   FormingCluster
redis-cluster-ephemeral   cluster   5           0          true        true                  false       Ready         Ready
```

### Redkey Cluster Scaling Up (Slow scaling)

When using slot-migration scaling (purgeKeysOnRebalance=false or cluster has replicas), slots are migrated to new nodes without data loss:

**Substatus flow:** `WaitingForPods` → `InitializingNodes` → `Rebalancing` → `AttachingReplicas` → `Verifying` → *(cleared)*

![Redkey Cluster Scaling Up Slow](./images/redkey-cluster-substatus-scalingup-slow.png)

This is an example of the Status and SubStatus changes when scaling the sample Redkey Cluster from 3 to 5 primaries, having previously changed the `purgeKeysOnRebalance` parameter to **false**:

```
NAME                      MODE      PRIMARIES   REPLICAS   EPHEMERAL   PURGEKEYS   STORAGE   DELETEPVC   PHASE         STATUS      SUBSTATUS
redis-cluster-ephemeral   cluster   3           0          true        false                 false       Ready         Ready
redis-cluster-ephemeral   cluster   5           0          true        false                 false       Ready         Ready
redis-cluster-ephemeral   cluster   5           0          true        false                 false       Configuring   ScalingUp
redis-cluster-ephemeral   cluster   5           0          true        false                 false       Configuring   ScalingUp   WaitingForPods
redis-cluster-ephemeral   cluster   5           0          true        false                 false       Configuring   ScalingUp   InitializingNodes
redis-cluster-ephemeral   cluster   5           0          true        false                 false       Configuring   ScalingUp   Rebalancing
redis-cluster-ephemeral   cluster   5           0          true        false                 false       Configuring   ScalingUp   AttachingReplicas
redis-cluster-ephemeral   cluster   5           0          true        false                 false       Configuring   ScalingUp   Verifying
redis-cluster-ephemeral   cluster   5           0          true        false                 false       Ready         Ready
```

### Redkey Cluster Scaling Down (Fast scaling)

In this case, the same substatus flow applies as for the Fast Scaling Up operation.

**Substatus flow:** `DeletingStatefulSet` → `RecreatingCluster` → `WaitingForPods` → `FormingCluster` → *(cleared)*

![Redkey Cluster Scaling Down Fast](./images/redkey-cluster-substatus-scalingdown-fast.png)

This is an example of the Status and SubStatus changes when scaling the sample Redkey Cluster from 5 to 3 primaries:

```
NAME                      MODE      PRIMARIES   REPLICAS   EPHEMERAL   PURGEKEYS   STORAGE   DELETEPVC   PHASE         STATUS        SUBSTATUS
redis-cluster-ephemeral   cluster   5           0          true        true                  false       Ready         Ready
redis-cluster-ephemeral   cluster   3           0          true        true                  false       Ready         Ready
redis-cluster-ephemeral   cluster   3           0          true        true                  false       Configuring   ScalingDown
redis-cluster-ephemeral   cluster   3           0          true        true                  false       Configuring   ScalingDown   DeletingStatefulSet
redis-cluster-ephemeral   cluster   3           0          true        true                  false       Configuring   ScalingDown   RecreatingCluster
redis-cluster-ephemeral   cluster   3           0          true        true                  false       Configuring   ScalingDown   WaitingForPods
redis-cluster-ephemeral   cluster   3           0          true        true                  false       Configuring   ScalingDown   FormingCluster
redis-cluster-ephemeral   cluster   3           0          true        true                  false       Ready         Ready
```

### Redkey Cluster Scaling Down (Slow scaling)

When using slot-migration scaling, slots are drained from surplus primaries before removing them:

**Substatus flow:** `DrainingPrimaries` → `RemovingNodes` → `ShrinkingStatefulSet` → `AttachingReplicas` → `Verifying` → *(cleared)*

Note: `AttachingReplicas` only appears if the cluster has replicas configured.

![Redkey Cluster Scaling Down Slow](./images/redkey-cluster-substatus-scalingdown-slow.png)

This is an example of the Status and SubStatus changes when scaling the sample Redkey Cluster from 5 to 3 primaries, having previously changed the `purgeKeysOnRebalance` parameter to **false**:

```
NAME                      MODE      PRIMARIES   REPLICAS   EPHEMERAL   PURGEKEYS   STORAGE   DELETEPVC   PHASE         STATUS        SUBSTATUS
redis-cluster-ephemeral   cluster   5           0          true        false                 false       Ready         Ready
redis-cluster-ephemeral   cluster   3           0          true        false                 false       Ready         Ready
redis-cluster-ephemeral   cluster   3           0          true        false                 false       Configuring   ScalingDown
redis-cluster-ephemeral   cluster   3           0          true        false                 false       Configuring   ScalingDown   DrainingPrimaries
redis-cluster-ephemeral   cluster   3           0          true        false                 false       Configuring   ScalingDown   RemovingNodes
redis-cluster-ephemeral   cluster   3           0          true        false                 false       Configuring   ScalingDown   ShrinkingStatefulSet
redis-cluster-ephemeral   cluster   3           0          true        false                 false       Configuring   ScalingDown   Verifying
redis-cluster-ephemeral   cluster   3           0          true        false                 false       Ready         Ready
```

### Redkey Cluster Scaling to Zero

When scaling to zero primaries, all cluster infrastructure is removed:

**Substatus flow:** `DeletingResources` → `DeletingPVCs` (if deletePVC=true) → *(cleared)*

```
NAME                      MODE      PRIMARIES   REPLICAS   EPHEMERAL   PURGEKEYS   STORAGE   DELETEPVC   PHASE         STATUS          SUBSTATUS
redis-cluster-ephemeral   cluster   3           0          true        true                  false       Ready         Ready
redis-cluster-ephemeral   cluster   0           0          true        true                  false       Configuring   ScalingToZero
redis-cluster-ephemeral   cluster   0           0          true        true                  false       Configuring   ScalingToZero   DeletingResources
redis-cluster-ephemeral   cluster   0           0          true        true                  false       Configuring   ScalingToZero   DeletingPVCs
redis-cluster-ephemeral   cluster   0           0          true        true                  false       Ready         Ready
```

### Redkey Cluster Upgrading (Fast upgrading)

Two Substatus are defined:

* **FastUpgrading**: The StatefulSet is recreated and we wait for the new Redkey Cluster pods to be ready.
* **FormingCluster**: Robin forms a fresh cluster and waits for confirmation that it has been recreated correctly, covering all slots and remaining balanced.

![Redkey Cluster Upgrading Fast](./images/redkey-cluster-substatus-upgrading-fast.png)

This is an example of the Status and SubStatus changes when upgrading the sample Redkey Cluster:

```
NAME                      MODE      PRIMARIES   REPLICAS   EPHEMERAL   PURGEKEYS   STORAGE   DELETEPVC   PHASE         STATUS      SUBSTATUS
redis-cluster-ephemeral   cluster   3           0          true        true                  false       Ready         Ready
redis-cluster-ephemeral   cluster   3           0          true        true                  false       Configuring   Upgrading
redis-cluster-ephemeral   cluster   3           0          true        true                  false       Configuring   Upgrading   FastUpgrading
redis-cluster-ephemeral   cluster   3           0          true        true                  false       Configuring   Upgrading   FormingCluster
redis-cluster-ephemeral   cluster   3           0          true        true                  false       Ready         Ready
```

### Redkey Cluster Upgrading (Rolling N+1)

These SubStatus have been defined:

* **AddingExtraNode**: Robin scales the StatefulSet by 1 primary (+ replicas if configured), waits for the extra pods to be Ready, meets them into the cluster, and attaches extra replicas.
* **DrainingNode**: Slots are being migrated from the current victim node (at the current partition ordinal) to the destination node (the extra primary on first iteration, or the previously recycled primary on subsequent iterations).
* **RollingUpdate**: The drained node is being recreated via the StatefulSet partition mechanism. Once the new pod is Ready with the new image, it is met back into the cluster and dead nodes are cleaned up. If replicas are configured, they are also recycled and re-attached.
* **MovingLastSlots**: Slots are moved from the extra primary back to node 0 (which was just recycled and is empty).
* **RemovingExtraNode**: The extra node (and its replicas) are forgotten from the cluster, the StatefulSet is scaled back to its original size, and a cluster health check is performed.

When performing a Rolling N+1 upgrade, Robin iterates from the last partition to partition 0, applying **DrainingNode** and **RollingUpdate** to each partition in sequence.

Current partition can be shown using `kubectl get rkc -o wide`.

![Redkey Cluster Upgrading Slow](./images/redkey-cluster-substatus-upgrading-slow.png)

This is an example of the Status and SubStatus changes when upgrading the sample Redkey Cluster:

```
NAME                      MODE      PRIMARIES   REPLICAS   EPHEMERAL   PURGEKEYS   STORAGE   DELETEPVC   PHASE         STATUS      SUBSTATUS           PARTITION
redis-cluster-ephemeral   cluster   3           0          true        false                 false       Ready         Ready
redis-cluster-ephemeral   cluster   3           0          true        false                 false       Configuring   Upgrading
redis-cluster-ephemeral   cluster   3           0          true        false                 false       Configuring   Upgrading   AddingExtraNode
redis-cluster-ephemeral   cluster   3           0          true        false                 false       Configuring   Upgrading   DrainingNode        2
redis-cluster-ephemeral   cluster   3           0          true        false                 false       Configuring   Upgrading   RollingUpdate       2
redis-cluster-ephemeral   cluster   3           0          true        false                 false       Configuring   Upgrading   DrainingNode        1
redis-cluster-ephemeral   cluster   3           0          true        false                 false       Configuring   Upgrading   RollingUpdate       1
redis-cluster-ephemeral   cluster   3           0          true        false                 false       Configuring   Upgrading   DrainingNode        0
redis-cluster-ephemeral   cluster   3           0          true        false                 false       Configuring   Upgrading   RollingUpdate       0
redis-cluster-ephemeral   cluster   3           0          true        false                 false       Configuring   Upgrading   MovingLastSlots     0
redis-cluster-ephemeral   cluster   3           0          true        false                 false       Configuring   Upgrading   RemovingExtraNode   0
redis-cluster-ephemeral   cluster   3           0          true        false                 false       Ready         Ready
```
