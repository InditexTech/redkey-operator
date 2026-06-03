<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Key purge (purgeKeysOnRebalance)

When using Redkey Cluster as a cache (`ephemeral` parameter set to **true**), we have the option to tell the Operator whether we want to keep the keys when performing scaling or upgrade operations, or whether we want to discard them.

When using Redkey Cluster as a cache (ephemeral parameter set to true), we have the option to tell the Operator whether we want to keep the keys when performing scaling or upgrade operations, or whether we want to discard them.

Scaling or upgrade operations have a **high computational and time cost**, as they involve resharding or moving slots (and their keys) between nodes of the cluster itself.

In order to speed up these operations, a fast and slow mode has been implemented.

More details about the Status and Sustatus for these operations can be found [here](.redkey-cluster-status.md).

## Fast operations

The fast mode of scaling and upgrade operations implies a **total loss of the keys** stored in the Redkey Cluster.

In this mode, the Operator is actually recreating the Redkey Cluster, recreating the pods that make it up and relying on Robin to meet the new nodes, distribute the slots, and ensure that it remains in a healthy state.

The cluster will be properly scaled or have the updated configuration.

This is the fastest way to perform scaling and upgrade operations.

Quick operations will only be applied if the Redkey Cluster is set to `purgeKeysOnRebalance` set to **true**.

## Slow Operations

If we want to preserve keys during Redkey Cluster upgrades and scales, then we need to use slow operations. To do this, we must configure the cluster with `purgeKeysOnRebalance` set to **false**.

In this case, the Operator makes sure to move the slots (and their keys) from one node to another node or nodes within the cluster itself, before recreating that node or discarding it.

The necessary reharding operations involve a high cost, which is impacted if the cluster receives a lot of activity from clients.

> Fast scaling additionally requires the cluster to have **no replicas**
> (`replicasPerPrimary: 0`). Clusters with replicas always use slow (key-preserving)
> operations regardless of `purgeKeysOnRebalance`.

## See also

* [Scaling a Redkey Cluster](operator-guide/scaling.md) — full scale up/down flows,
  fast vs. normal scaling, and `TRYAGAIN` guidance for application clients.
