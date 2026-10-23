<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Cluster Configuration Defaults

Robin generates the `redis.conf` file for every Redis node in the cluster. This document describes the default parameters applied unconditionally and how user-provided configuration interacts with them.

## Default Parameters

The following parameters are applied to **every cluster**, regardless of topology (ephemeral/persistent, with/without replicas). They are defined in a single, centralized location in the codebase (`clusterDefaults` in `internal/kubernetes/objects.go`).

| Parameter | Value | Purpose |
|-----------|-------|---------|
| `cluster-enabled` | `yes` | Required for Redis Cluster mode. Without this, Redis runs as a standalone instance. |
| `cluster-node-timeout` | `5000` | Time in milliseconds before a node is considered unreachable by its peers. Controls failover speed: lower values trigger faster failover but risk false positives under network jitter. |
| `cluster-require-full-coverage` | `no` | Allows the cluster to continue serving requests even when some hash slots are temporarily uncovered. Without this set to `no`, any slot migration (during upgrade or scaling) causes `CLUSTERDOWN` for the entire cluster, blocking **all** client operations — reads and writes — on every node. |
| `cluster-allow-reads-when-down` | `yes` | Permits read operations even when the cluster detects partial failure states. Reduces impact on read-intensive workloads during transient conditions such as node restarts, network partition recovery, or upgrade operations. |
| `cluster-allow-replica-migration` | `no` | Disables Redis' automatic replica migration so the operator/Robin retains full, deterministic control over topology. Prevents drained primaries (zero slots) from auto-converting into replicas during scaling/upgrades and stops replicas from auto-migrating between primaries. |

### Why `cluster-require-full-coverage no`?

During Rolling N+1 upgrades and scaling operations, Robin migrates hash slots between nodes via `redis-cli --cluster reshard`. While a slot is in transit, it temporarily has no single owner (the source is draining it, the destination is importing it). If `cluster-require-full-coverage` were set to `yes` (Redis default), any uncovered slot — even for milliseconds — would trigger `CLUSTERDOWN`, halting all cluster operations. Setting it to `no` ensures unaffected slots continue to serve traffic normally during migrations.

### Why `cluster-allow-reads-when-down yes`?

When Redis detects that the cluster state is not healthy (e.g., a node is unreachable, slots are uncovered), it may reject client commands. With `cluster-allow-reads-when-down yes`, read operations continue to succeed on nodes that are otherwise healthy. This improves availability for read-heavy workloads during:

- Node restarts (planned or unplanned)
- Network partition recovery
- Rolling upgrades where a primary is temporarily removed from the cluster

### Why `cluster-allow-replica-migration no`?

With Redis' default (`yes`), the cluster performs two automatic reconfigurations that conflict with operator-managed topology:

- A primary that is **drained to zero slots** (for example, the surplus primaries during a scale-down) automatically turns itself into a replica of the node that absorbed its slots. In a cluster configured without replicas this leaves stray replicas behind and triggers unnecessary full-sync/replication traffic before Robin can remove the node.
- Replicas **migrate between primaries** to cover orphaned masters, drifting away from the placement Robin computed.

Because Robin already manages replica placement explicitly (via `CLUSTER REPLICATE` and the health reconciler), setting this to `no` keeps drained primaries as empty masters that are cleanly removed (`CLUSTER FORGET`) instead of becoming replicas, and keeps replica assignment deterministic.

## Persistence Parameters

Robin sets the working directory based on the `ephemeral` flag. The persistence directives
(`appendonly`, `save`) are written by **exactly one layer**, so the generated `redis.conf` never
repeats them:

| Condition | Parameters written by Robin | Persistence (`appendonly` / `save`) |
|-----------|-----------------------------|-------------------------------------|
| `ephemeral = true` | `dir /tmp`, `cluster-config-file /tmp/nodes.conf`, `appendonly no`, `save ""` | **Robin**: AOF and RDB disabled. No data volume is mounted, so Redis' working directory is `/tmp` and `nodes.conf` (cluster topology metadata) is written there. Pod restart = data loss for that node. Do not set `appendonly` / `save` in `spec.config`. |
| `ephemeral = false` | `dir /data`, `cluster-config-file /data/nodes.conf` | **The user**, through `spec.config`. Robin writes neither. If `spec.config` sets none, the Redis defaults apply: AOF off (`appendonly no`) and RDB snapshots with the default save points (`save 3600 1 300 100 60 10000`). Redis' working directory is the PVC mounted at `/data`, so `nodes.conf` and the AOF/RDB files survive pod restarts. |

For persistent clusters, set both directives explicitly so the intent is clear and a disabled
option really is disabled (omitting `save` keeps RDB on). For example, AOF only:

```yaml
spec:
  ephemeral: false
  storage: 1Gi
  config: |
    appendonly yes
    appendfsync everysec
    save ""
```

### Why does Robin disable persistence on ephemeral clusters?

Ephemeral clusters run Redis in the container writable layer (`/tmp`), which is discarded when the
container restarts, so an RDB or AOF file is never reloaded. Leaving the Redis defaults would only
add cost and risk: periodic `BGSAVE` forks (copy-on-write memory, OOMKill risk near the memory
limit), disk usage counted as ephemeral storage (eviction), a blocking `SAVE` on every shutdown,
and rejected writes (`MISCONF`) if a snapshot fails.

### Changing persistence on a running cluster

A change in `spec.config` recycles the Redis pods (see
[Which changes recycle the Redis pods?](#which-changes-recycle-the-redis-pods)), and each new
process reads `redis.conf` from scratch:

- **Cluster mode**: the Rolling N+1 upgrade migrates the slots off each node before recycling it,
  so enabling or disabling AOF/RDB does not lose data.
- **Standalone mode**: the single pod is restarted, and Redis reloads only what the new
  configuration reads. Enabling AOF on an existing dataset starts Redis **empty**: Redis does not
  load `dump.rdb` when AOF is enabled and no AOF exists yet. Disabling AOF while `save ""` is set
  also starts empty, because nothing is written at shutdown. To enable AOF on a standalone instance
  with data, run `CONFIG SET appendonly yes` on the running node first, wait for the rewrite to
  finish (`INFO persistence` shows `aof_rewrite_in_progress:0`), and then add `appendonly yes` to
  `spec.config`.

### Why `/tmp` for ephemeral clusters?

Redis writes `nodes.conf` (and any RDB/AOF files) into its working directory, set via `dir`. The
default Redis image working directory is `/data`, which is owned by the image's `redis` user. On
platforms that assign a **random, non-root UID** to the container — notably OpenShift's
`restricted-v2` SCC — that UID cannot write to `/data` unless a volume is mounted there with the
appropriate group ownership (`fsGroup`).

Persistent clusters get a PVC mounted at `/data`, which the platform makes group-writable, so
`dir /data` works. **Ephemeral clusters mount no data volume**, so `dir` is pointed at `/tmp`
instead — a location that is world-writable in the container image and therefore always writable by
the runtime UID. Without this, ephemeral Redis pods fail to start with
`Can't open nodes.conf in order to acquire a lock: Permission denied`.

## User Overrides

Any parameter specified in `spec.config` on the `Redkey` CR is appended **after** the defaults in the generated `redis.conf`:

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: Redkey
metadata:
  name: my-cluster
spec:
  primaries: 3
  config: |
    maxmemory 2gb
    maxmemory-policy allkeys-lru
```

Since Redis uses last-write-wins for duplicate parameters, user-specified values override defaults. This allows full customization while keeping safe defaults for new clusters.

> **Warning:** Overriding `cluster-require-full-coverage` to `yes` is strongly discouraged — it breaks upgrade and scaling operations. Overriding `cluster-enabled` to `no` will prevent the cluster from functioning entirely.

## Configuration Lifecycle

1. **Cluster creation**: Robin generates `redis.conf` with defaults + user config and stores it in a ConfigMap.
2. **Configuration update**: When the spec changes, the Operator creates a new `RedkeyConfig`. Robin detects the change and reconciles it.
3. **Hot-reload vs restart**: Redis does not hot-reload `redis.conf`. Configuration changes that affect the Redis pod template take effect only after the pods are recycled through the [upgrade](upgrade.md) flow.

## Which changes recycle the Redis pods?

Not every spec change recreates Redis pods. Robin classifies a change and reacts
accordingly:

| Change | Effect |
|--------|--------|
| `primaries`, `replicasPerPrimary` | [Scaling](scaling.md) (no full recycle; nodes are added/removed and slots rebalanced) |
| `image`, `version`, `config` (`redisConfig`) | [Upgrade](upgrade.md) — pods recycled with the new template |
| `resources`, `labels`, `annotations`, `override`, `pdb` | [Upgrade](upgrade.md) — pods recycled with the new template |
| `robin` configuration only | Hot-reloaded by Robin; **no** pod recycle |
| `purgeKeysOnRebalance` only | Recorded; no recycle on its own |

Any change that alters the Redis pod template is detected via the pod's
`controller-revision-hash` label, so it reliably triggers a recycle even when the
container image is unchanged (see [Upgrade](upgrade.md#detecting-which-pods-still-need-recycling)).

## Immutable fields

The following storage-related fields are **immutable after cluster creation**. They are
enforced by a CEL validation rule on the CRD, so the API server rejects any update that
changes them:

| Field | Rejection message |
|-------|-------------------|
| `ephemeral` | `Changing the ephemeral field is not allowed` |
| `storage` | `Changing the storage size is not allowed` |
| `storageClassName` | `Changing the storage class name is not allowed` |
| `accessModes` | `Changing the storage access modes is not allowed` |

These fields define the persistent volumes backing the cluster, which cannot be resized
or reprovisioned in place. To change them, create a new cluster and migrate the data
(side-by-side migration).

