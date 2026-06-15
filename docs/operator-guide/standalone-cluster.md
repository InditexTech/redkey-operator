<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Standalone Mode (Single-Node Redkey)

Standalone mode deploys a single Redis instance with Redis Cluster support disabled (`cluster-enabled no`) instead of a sharded Redkey Cluster. It is intended for development, pre-production, or workloads that do not require sharding or high availability, while still benefiting from the operator's lifecycle management, metrics, authentication, and configuration hot-reload.

## How To Enable Standalone Mode

Set the `mode` field to `standalone` when creating the cluster:

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: RedkeyCluster
metadata:
  name: redis-standalone
  namespace: my-namespace
spec:
  mode: standalone
  primaries: 1
  replicasPerPrimary: 0
```

The `mode` field accepts two values:

* `cluster` (default): deploys a sharded Redkey Cluster.
* `standalone`: deploys a single-node Redis instance.

When `mode` is omitted, the operator defaults to `cluster`.

## Constraints

The following constraints are enforced by the CRD via validation rules. Invalid configurations are rejected at creation/update time:

* **At most one primary**: `primaries` must be `0` or `1`. A standalone instance cannot be sharded.
* **No replicas**: `replicasPerPrimary` must be `0`. Replica/failover topologies are not supported in standalone mode.
* **`mode` is immutable**: it cannot be changed after the cluster is created. To switch between `cluster` and `standalone`, delete and recreate the resource.

## Persistent Storage

Standalone mode supports persistent storage. Set the `storage` field to provision a `PersistentVolumeClaim` for the Redis data directory:

```yaml
spec:
  mode: standalone
  primaries: 1
  storage: 5Gi
```

When `storage` is omitted, the instance runs in ephemeral mode and no PVC is created.

## Scaling to Zero and Back

Standalone instances can be scaled to zero to release compute resources (for example, to pause a development environment) and scaled back up to one without losing persistent data when storage is configured:

```bash
# Scale down to zero
kubectl patch redkeycluster redis-standalone --type merge -p '{"spec":{"primaries":0}}'

# Scale back up to one
kubectl patch redkeycluster redis-standalone --type merge -p '{"spec":{"primaries":1}}'
```

Scaling to zero removes the StatefulSet and the running pod. Scaling beyond one primary is rejected by validation.

## Upgrades and Configuration Changes

Standalone instances support in-place upgrades and configuration changes. When you change the
Redis image, the version, the pod resources, or the Redis configuration (`redisConfig`) of a
running standalone cluster, the operator transitions it to the `Upgrading` state and applies the
change:

```bash
# Change the Redis image
kubectl patch redkeycluster redis-standalone --type merge -p '{"spec":{"image":"redis:8"}}'

# Change the Redis configuration
kubectl patch redkeycluster redis-standalone --type merge \
  -p '{"spec":{"redisConfig":"maxmemory 256mb\nmaxmemory-policy allkeys-lru"}}'
```

The upgrade is performed as a single-pod recycle:

1. The Redis configuration `ConfigMap` and the StatefulSet pod template (image, resources,
   labels, annotations and the configuration checksum) are updated to the desired state.
2. The single pod is restarted exactly once so it is recreated from the new pod template. The
   restart is guarded by comparing the pod's controller revision against the StatefulSet's
   update revision, so the operation is idempotent and never recycles a pod that already runs
   the desired configuration.
3. Once the recreated pod is `Ready` and reachable, the cluster returns to the `Ready` state.

Because a standalone instance has a single node with no replica to fail over to, the recycle
incurs a brief downtime while the pod restarts. When persistent storage is configured, the data
is preserved across the restart; in ephemeral mode the data is lost when the pod is recreated.

## Differences From Cluster Mode

| Aspect | Cluster | Standalone |
|---|---|---|
| Redis Cluster (`cluster-enabled`) | `yes` | `no` |
| Sharding | Yes | No |
| `primaries` | `0`, `3`+ | `0` or `1` |
| `replicasPerPrimary` | `0`+ | `0` only |
| PodDisruptionBudget | Created when `primaries > 1` | Never created |
| Cluster-level metrics | Collected | Not collected |
| Node-level (INFO) metrics | Collected | Collected |

Node-level metrics (from `INFO`) are collected in both modes. Cluster-level metrics (slots, shard topology, cluster state) do not apply to standalone instances and are not collected.

## Authentication

Standalone mode supports authentication the same way as cluster mode. See [Redkey Authentication](authentication.md).
