<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Deletion of PVCs on successful scale-down and cluster deletion

The PVCs (Persisteng Volume Claims) of the Redis nodes not only holds its data, but also its configuration, including its knowledge about the other nodes in the cluster. While this is an expected behaviour for persistent clusters, it creates issues especially in development environments where people tend to delete and create clusters and scale up and down frequently.

Let's think a scenario where you delete a 5 primaries cluster and create a 3 primaries cluster with the same name, 3 pods will be created and will have the configuration files and data from the first 3 pods of the 5 primaries cluster. So they will be expecting 2 more nodes which aren't there and have some slots missing.

Or you may have a 7 primaries cluster, scale it down to 3 and scale it up to 5 primari. When the cluster is scaled down to 3, all the slots will be moved to the first 3 nodes, they'll forget about the tailing 4 nodes and their configuration will change accorgingly. When you scale it back to 5 nodes, the 2 newly added nodes will load configuration where there were 7 nodes and this configuration will not match the first 3 nodes' configurations.

Starting with Redkey Operator release 0.2.33, Redkey Operator supports deletion of PVCs after a scale down operation or a cluster deletions. In case of scaling down, the operator will delete de PVCs of the nodes that are deleted, and in case of cluster deletion, it'll delete all the PVCs owned by the nodes of the cluster. This feature can be enabled by setting `deletePVC` to `true` under `spec` section of a RedkeyCluster definition.

> This setting is optional. The PVCs belonging to the cluster without this setting is its manifest will not be deleted.

> There's a [feature](https://kubernetes.io/blog/2021/12/16/kubernetes-1-23-statefulset-pvc-auto-deletion/) of Kubernetes which optionally deletes PVCs of StatefulSets upon scaling down or deletion introduced in version 1.23. By the time we developed our own feature, this feature was still in Alpfa status.

## Example

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: RedkeyCluster
...
spec:
  ...
  deletePVC: true
```

## Data on the PVC during a Rolling N+1 upgrade

On persistent clusters (`ephemeral: false`), a [Rolling N+1 upgrade](upgrade.md) recycles
one node at a time. Before a drained node's pod is deleted, Robin issues a `FLUSHALL`
followed by a synchronous `SAVE`. By that point the node no longer owns any slots (its
slots have already been migrated away), so its remaining keys are stale. Persisting an
empty dataset guarantees the restarted pod reloads a clean RDB from its PVC instead of the
pre-reshard snapshot, which would otherwise still contain keys for already-migrated slots.

This affects only the per-node RDB written to the PVC during the upgrade — it does not
remove or recreate the PVC itself, and it does not affect data on nodes that still own
slots. Ephemeral clusters skip this step entirely.

