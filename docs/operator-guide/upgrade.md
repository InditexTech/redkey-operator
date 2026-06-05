<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Upgrade (Rolling Config)

This document describes the Redis image/configuration upgrade mechanism implemented by Robin (the sidecar controller). The upgrade process handles all four topologies (ephemeral/persistent, with/without replicas).

## Scope and Limitations

> **IMPORTANT**: This upgrade mechanism assumes **compatible images** (same major version or known-compatible minor/patch upgrades). For major version upgrades with breaking changes (e.g., Redis 7 → Redis 8 with incompatible RDB format), use the **side-by-side migration strategy** (create a destination cluster and migrate data externally).

The mechanism triggers when a new `RedkeyClusterConfig` changes the `image`, `version`, or `redisConfig` fields while the cluster is in `Ready` status.

## Upgrade Strategies

Robin selects the upgrade strategy based on the cluster configuration:

| Strategy | Condition | Data Loss | Downtime |
|----------|-----------|-----------|----------|
| **Fast Upgrade** | `ephemeral = true` AND `replicasPerPrimary = 0` AND `purgeKeysOnRebalance = true` | Yes (full) | Brief (pods restart) |
| **Rolling N+1** | All other configurations | No | None (zero-downtime) |

### Fast Upgrade

Used only for ephemeral clusters without replicas where data loss is acceptable (caches, test environments). All three conditions must be met: `ephemeral = true`, `replicasPerPrimary = 0`, and `purgeKeysOnRebalance = true`. Clusters with replicas always use the Rolling N+1 strategy to guarantee zero service disruption.

**Substatus flow:**
```
(default) → FastUpgrading → FormingCluster → Ready
```

**Steps:**
1. Update ConfigMap with new `redis.conf`
2. Update StatefulSet template (new image, config checksum annotation)
3. Delete all pods — StatefulSet recreates them with the new template
4. Wait for all pods to be Ready
5. Form a fresh cluster (CLUSTER ADDSLOTS + CLUSTER REPLICATE)
6. Set status to Ready, mark config as Applied

### Rolling N+1 Upgrade

Used for production clusters where data must be preserved. This strategy ensures zero downtime by maintaining slot ownership throughout the process.

It uses a **pivot pattern**: slots always move to the *previously recycled* node (which already has the new image), not back and forth to the extra node. This means each slot is migrated exactly twice: once away from its original node, and once back to it (in the ending phase for node 0's slots).

**Substatus flow:**
```
(default) → AddingExtraNode → DrainingNode ⟷ RollingUpdate → MovingLastSlots → RemovingExtraNode → Ready
```

**StatefulSet layout (with replicas):**
For a cluster with 3 primaries and 1 replica/primary: pods 0–2 are primaries, pods 3–5 are replicas, pods 6–7 are the extra primary + extra replica.

**Steps:**

1. **Start**: Update ConfigMap + StatefulSet template (using **OnDelete** update strategy — no pods are recreated automatically). Scale StatefulSet up by 1 primary (+ replicas if configured). Extra pods get the new image.

2. **ScalingUp**: Wait for extra pods to be Ready. Meet all nodes into the cluster via `CLUSTER MEET`. If replicas configured: run `CLUSTER REPLICATE` on extra replica(s) to attach them to the extra primary. Set initial partition = (primaries - 1).

3. **Resharding** (per partition, from last to first):
   - **Verify pivot replica** — ensure the extra primary still has its replica attached (guards against Redis auto-migration via `cluster-allow-replica-migration yes`). If missing, re-attach via `CLUSTER MEET` + `CLUSTER REPLICATE`
   - Run `redis-cli --cluster fix` to resolve any open/stuck slots from a previous partial reshard
   - Determine destination: first iteration → extra primary (ordinal = primaries + primaries×replicas); subsequent iterations → partition+1 (previously recycled primary)
   - Migrate all slots from the current victim (partition ordinal) to the destination via `redis-cli --cluster reshard`
   - Verify source has 0 slots
   - If replicas configured: `CLUSTER FORGET` all replicas of the victim from all cluster members

4. **RollingUpdate** (per partition):
   - **Delete only the drained primary pod** — OnDelete strategy means the StatefulSet recreates it with the new image. No other pods are affected.
   - Wait for the pod to be recreated with the new image and become Ready
   - Meet the recycled pod back into the cluster (using the extra primary as seed)
   - `CLUSTER FORGET` all nodes in FAIL state (dead nodes from previous recycles cause reshard timeouts if not cleaned)
   - If replicas configured: **delete only the replica pods of this specific drained primary** (not replicas of other primaries). Wait for each replica to be recreated, meet it, and run `CLUSTER REPLICATE` to attach it to the recycled primary. Replicas of primaries that still hold slots are **never touched**, preserving HA throughout the entire process.
   - **Verify pivot replica** — re-check that the extra primary's replica was not auto-migrated after the replica recycle disruption
   - Advance to next partition (partition - 1) or proceed to Ending

5. **Ending**: Migrate all remaining slots from the extra node back to node 0 (which was just recycled and is empty). Run `redis-cli --cluster fix` before reshard. `CLUSTER FORGET` the extra node (and its replicas) from all cluster members.

6. **ScalingDown**: Scale StatefulSet back to original size. Restore **RollingUpdate** strategy with partition=0 (normal operating mode). Run `CLUSTER CHECK` to verify health. If replicas configured: **rebalance replicas** — force the correct primary→replica mapping for all primaries via `CLUSTER REPLICATE` to fix any drift caused by Redis auto-migration during the upgrade. Set status to Ready.

## Config Checksum

A SHA-256 checksum of `image + version + redisConfig` is stored as an annotation on the pod template:

```
redkey.inditex.dev/config-checksum: <16-char hex>
```

This ensures Kubernetes detects a template change even when only the Redis configuration (not the image) changes.

## Observability

During upgrade, the `RedkeyClusterConfig` status tracks progress:

```yaml
status:
  status: Upgrading
  substatus:
    status: DrainingNode  # Current phase
    upgradingPartition: 1      # Which primary is being upgraded (counts down to 0)
```

Robin logs all state transitions with structured fields including partition number, slot counts, and node IDs.

## Topology Support

| Topology | Fast Upgrade | Rolling N+1 |
|----------|:---:|:---:|
| Ephemeral, no replicas, purge=true | ✓ | — |
| Ephemeral, no replicas, purge=false | — | ✓ |
| Ephemeral, with replicas | — | ✓ |
| Persistent, no replicas | — | ✓ |
| Persistent, with replicas | — | ✓ |

## Error Handling

- Each substatus is idempotent — if Robin restarts mid-upgrade, it resumes from the last recorded substatus
- Reshard failures trigger a retry on the next reconcile interval
- If a pod fails to become Ready, Robin waits and retries indefinitely
- `CLUSTER CHECK` at the end validates the cluster is healthy before marking Ready

## Example: Upgrading Redis Image

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: RedkeyCluster
metadata:
  name: my-cluster
spec:
  primaries: 3
  replicasPerPrimary: 1
  image: redis:7.4.2  # Change from redis:7.2.5
```

The operator creates a new `RedkeyClusterConfig` with the updated image. Robin detects the change, transitions to `Upgrading`, and executes the Rolling N+1 strategy (since `purgeKeysOnRebalance` is not set to `true`).
