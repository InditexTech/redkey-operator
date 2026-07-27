<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Redkey Architecture

This document describes the architecture of the Redkey system, which manages Redis/Valkey clusters on Kubernetes through two cooperating components: **Redkey Operator** and **Redkey Robin**.

## Table of Contents

- [Overview](#overview)
- [Components](#components)
  - [Redkey Operator](#redkey-operator)
  - [Redkey Robin](#redkey-robin)
- [Custom Resource Definitions](#custom-resource-definitions)
  - [Redkey](#redkey)
  - [RedkeyConfig](#redkeyconfig)
- [Communication Model](#communication-model)
- [Sequenced Configuration Changes](#sequenced-configuration-changes)
  - [Configuration Lifecycle](#configuration-lifecycle)
  - [Skip-If-Superseded Policy](#skip-if-superseded-policy)
  - [Cleanup](#cleanup)
- [State Machines](#state-machines)
  - [RedkeyConfig — Configuration Phase](#redkeyconfig--configuration-phase)
  - [RedkeyConfig — Cluster Operational Status](#redkeyconfig--cluster-operational-status)
  - [Redkey — Aggregated Phase](#redkey--aggregated-phase)
- [RBAC Model](#rbac-model)
- [Reconciliation Flows](#reconciliation-flows)
  - [Operator Reconciliation](#operator-reconciliation)
  - [Robin Reconciliation Loop](#robin-reconciliation-loop)
- [Kubernetes Resources Managed](#kubernetes-resources-managed)

---

## Overview

A **Redkey Cluster** is a key/value cluster built from either [Redis](https://hub.docker.com/_/redis) or [Valkey](https://hub.docker.com/r/valkey/valkey/) images, deployed and managed on Kubernetes through the operator pattern.

The architecture follows a clear separation of concerns:

- **Redkey Operator** is a lightweight umbrella coordinator that watches `Redkey` custom resources and translates user intent into sequenced `RedkeyConfig` CRs. It manages the Robin Deployment and its RBAC resources, aggregates status back to the user-facing CR, and cleans up superseded configurations.
- **Redkey Robin** is the self-contained operator for a single cluster instance. It owns all Kubernetes resource management (StatefulSet, Service, PDB, ConfigMap) and all Redis/Valkey operations (cluster formation, scaling, upgrades, rebalancing, health checks). It reads desired state from `RedkeyConfig` and writes status back via the status subresource.

Inter-component communication is fully declarative and Kubernetes-native — there is no REST API or synchronous HTTP coupling between the two components.

---

## Components

### Redkey Operator

The Operator is a single controller that reconciles `Redkey` CRs. Its responsibilities are deliberately minimal:

| Responsibility | Description |
| -------------- | ----------- |
| **Robin Deployment management** | Ensures the Robin `Deployment` exists and matches the desired spec derived from `spec.robin` in the `Redkey` CR. Detects drift and patches when needed. |
| **RBAC resource management** | Creates a dedicated `ServiceAccount`, `Role`, and `RoleBinding` per cluster, all owned by the `Redkey` CR for automatic garbage collection. |
| **Sequenced config creation** | On every relevant `Redkey` spec change, increments a sequence counter and creates a new `RedkeyConfig` CR carrying the full desired cluster state. |
| **Status aggregation** | Reads the status of the highest-sequence `RedkeyConfig` and mirrors a simplified summary to `Redkey.status`. |
| **Config cleanup** | Deletes superseded `RedkeyConfig` CRs (those with terminal `configPhase` that are no longer the current baseline). |

The Operator does **not** create or manage StatefulSets, Services, PodDisruptionBudgets, or Redis ConfigMaps. It does **not** call any HTTP API.

### Redkey Robin

Robin is deployed 1:1 per `Redkey` as a `Deployment` managed by the Operator. It is the single component that holds all cluster knowledge:

| Responsibility | Description |
| -------------- | ----------- |
| **K8s resource lifecycle** | Creates, updates, and deletes the `StatefulSet`, headless `Service`, `PodDisruptionBudget`, and `redis.conf` `ConfigMap` for the Redis/Valkey pods. |
| **Redis/Valkey management** | Manages the cluster topology at the Redis level: `CLUSTER MEET`, `CLUSTER REPLICATE`, `CLUSTER FORGET`, slot assignment, resharding, rebalancing, failovers, and version upgrades. |
| **Config processing** | Polls `RedkeyConfig` CRs and processes them sequentially by `spec.sequence`, driving each config through `Pending → InProgress → Applied`. |
| **Health verification** | Verifies K8s resources match expected state and repairs drift. Checks Redis cluster health (node membership, slot coverage, replication, balance) and fixes issues autonomously. |
| **Status reporting** | Writes operational status to the active `RedkeyConfig.status` subresource. |
| **Metrics** | Collects Redis `INFO` metrics from all nodes and exposes them as Prometheus metrics at `/metrics`. |

---

## Custom Resource Definitions

### Redkey

`Redkey` is the user-facing API. Users declare their desired cluster state here.

**Spec** — defines the desired cluster topology and configuration:

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: Redkey
metadata:
  name: my-cluster
spec:
  primaries: 3
  replicasPerPrimary: 1
  image: redis:8-bookworm
  ephemeral: false
  storage: 1Gi
  storageClassName: standard
  skipIfSuperseded: false
  config: |
    maxmemory-policy allkeys-lru
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
  auth:
    secret: my-redis-secret
  pdb:
    enabled: true
  robin:
    template:
      spec:
        containers:
          - image: redkey-robin:latest
    config:
      reconciler:
        intervalSeconds: 10
        intervalOnErrorSeconds: 10
        intervalOnWaitSeconds: 10
```

**Status** — a simplified, aggregated view derived from `RedkeyConfig`:

| Field | Description |
| ----- | ----------- |
| `phase` | High-level summary: `Ready`, `Configuring`, or `Error` |
| `status` | Operational phase mirrored from the highest-sequence config |
| `substatus` | Detailed sub-status (e.g. upgrading partition) |
| `nodes` | Per-node status map (role, IP, replication status) |
| `conditions` | Lifecycle: `Ready`, `ConfigPending`, `Error`; plus the aggregated [health conditions](redkey-cluster-status.md#cluster-health-conditions) (`Healthy`, `MembershipHealthy`, `SlotsCovered`, `SlotsBalanced`, `ReplicasBalanced`, `ClusterCheckPassing`) |

### RedkeyConfig

`RedkeyConfig` is the communication channel between the Operator and Robin. Each instance represents a sequenced desired-state snapshot.

**Spec** (written exclusively by the Operator):

| Field | Description |
| ----- | ----------- |
| `sequence` | Monotonically increasing counter |
| `skipIfSuperseded` | Whether Robin may skip this config if a newer one is pending |
| `primaries` | Number of Redis primary nodes |
| `replicasPerPrimary` | Replicas per primary |
| `ephemeral` | Whether storage is ephemeral |
| `storage` / `storageClassName` | Persistent storage configuration |
| `image` / `version` | Redis/Valkey image and version |
| `redisConfig` | Raw redis.conf content |
| `resources` | Pod resource requirements |
| `auth` | Secret reference for authentication |
| `labels` | Additional pod labels |
| `deletePVC` | PVC deletion policy |
| `purgeKeysOnRebalance` | Fast upgrade strategy (ephemeral only) |
| `override` | StatefulSet and Service template overrides |
| `pdb` | PodDisruptionBudget configuration |
| `robinConfig` | Robin operational settings (intervals, retries, etc.) |

**Status** (written exclusively by Robin via the status subresource):

| Field | Description |
| ----- | ----------- |
| `configPhase` | Configuration lifecycle: `Pending`, `InProgress`, `Superseded`, or `Applied` |
| `status` | Cluster operational status (e.g. `Initializing`, `Ready`, `ScalingUp`) |
| `substatus` | Detailed sub-status (scaling/upgrade step, or `Remediating`) and upgrading partition |
| `nodes` | Per-node status map |
| `conditions` | Data-plane [health conditions](redkey-cluster-status.md#cluster-health-conditions) written by Robin: `Healthy`, `MembershipHealthy`, `SlotsCovered`, `SlotsBalanced`, `ReplicasBalanced`, `ClusterCheckPassing` |

---

## Communication Model

All communication between the Operator and Robin flows through the Kubernetes API via `RedkeyConfig` CRs. There is no direct network communication between the two components.

```ascii
┌──────────────────────────────────────────────────────────────────────┐
│                         Kubernetes Cluster                           │
│                                                                      │
│  User                                                                │
│   │  apply Redkey CR                                          │
│   ▼                                                                  │
│  ┌──────────────────────────────────────────────────────────────┐    │
│  │                       Redkey Operator                        │    │
│  │  - Watches Redkey CR events                           │    │
│  │  - Creates new RedkeyConfig per change                │    │
│  │  - Manages Robin Deployment                                  │    │
│  │  - Mirrors highest-sequence status → Redkey           │    │
│  │  - Cleans up superseded configs                              │    │
│  └──────────────────────────────────────────────────────────────┘    │
│       │ creates new CRs       ▲ reads status    │ deletes old CRs    │
│       ▼                       │                 ▼                    │
│  ┌──────────────────────────────────────────────────────────────┐    │
│  │             RedkeyConfig CRs (sequenced)              │    │
│  │  mycluster-1 (Applied)  ← current baseline                   │    │
│  │  mycluster-2 (Pending)  ← next change                        │    │
│  │  mycluster-3 (Pending)  ← queued change                      │    │
│  └──────────────────────────────────────────────────────────────┘    │
│               │ polls (list by label)      │ writes status           │
│               ▼                            │                         │
│          ┌──────────────────────────────────────┐                    │
│          │            Redkey Robin              │                    │
│          │  - Polls on configurable interval    │                    │
│          │  - Processes Pending configs         │                    │
│          │  - Manages K8s resources + Redis     │                    │
│          │  - Rebalances if needed              │                    │
│          │  - Exposes Prometheus metrics        │                    │
│          └──────────────────────────────────────┘                    │
│               │ creates/manages                                      │
│               ▼                                                      │
│  ┌────────────────────────────────────────────────────────┐          │
│  │   StatefulSet │ Service │ PDB │ redis.conf ConfigMap   │          │
│  └────────────────────────────────────────────────────────┘          │
│               │                                                      │
│               ▼                                                      │
│  ┌──────────────────────────┐                                        │
│  │   Redis/Valkey Cluster   │                                        │
│  └──────────────────────────┘                                        │
└──────────────────────────────────────────────────────────────────────┘
```

This design eliminates synchronous, network-dependent coupling. The Operator writes desired state and moves on; Robin converges to that state in its own time. If the Operator is unavailable, running clusters are completely unaffected.

---

## Sequenced Configuration Changes

Rather than updating a single `RedkeyConfig` in-place, the Operator creates a **new** CR for each configuration change. This produces an ordered sequence of desired states that Robin processes one at a time.

### Naming and Ordering

Each `RedkeyConfig` is named `<cluster-name>-<sequence>` (e.g. `mycluster-3`) and carries the counter in `spec.sequence`. All configs for the same cluster share the label `redkey.inditex.dev/cluster: <cluster-name>`, enabling efficient listing. The Operator tracks the current generation of the `Redkey` CR via an annotation (`redkey.inditex.dev/cluster-generation`) on each config to avoid creating duplicate configs for the same generation.

### Configuration Lifecycle

Each `RedkeyConfig` transitions through the following phases in `status.configPhase`:

```ascii
Pending  ──►  InProgress  ──►  Applied  ──►  (deleted by Operator)
                                  │
Pending  ──►  Superseded  ──►  (deleted by Operator)
```

| Phase | Description |
| ----- | ----------- |
| `Pending` | Created by the Operator. Robin has not yet started processing it. |
| `InProgress` | Robin is actively applying this configuration. |
| `Applied` | Configuration has been fully applied. This is the current baseline. |
| `Superseded` | Skipped via skip-if-superseded without executing changes. |

### Skip-If-Superseded Policy

The `skipIfSuperseded` field is configured on the `Redkey` spec and propagated by the Operator to each `RedkeyConfig` it creates. When `spec.skipIfSuperseded` is `true` and Robin detects that a newer config with a higher sequence is also `Pending`, Robin may skip the current config by transitioning it directly from `Pending` to `Superseded` without executing any changes.

This is useful when intermediate states are not needed. For example, if two consecutive scaling changes are queued (`primaries: 3 → 5 → 7`), only the final target (`7`) matters.

### Cleanup

The Operator cleans up superseded configs during each reconciliation cycle:

1. Lists all `RedkeyConfig` CRs for the cluster (using the label selector).
2. Iterates over configs sorted by sequence (ascending).
3. Deletes contiguous configs in a terminal phase (`Applied` or `Superseded`) from the beginning of the list, stopping at the first non-terminal config.
4. Always preserves the highest-sequence config (the current baseline or an in-progress change).

This ensures the namespace contains at most: one `Applied` CR (current state) + zero or more `Pending`/`InProgress` CRs (queued changes).

### Example: Rapid Consecutive Changes

```ascii
Time   Operator action                Robin action              State in namespace
─────  ─────────────────────────────  ────────────────────────  ──────────────────────────────
t=0    Creates mycluster-1 (Pending)                            mycluster-1 (Pending)
t=1    Creates mycluster-2 (Pending)                            mycluster-1 (Pending)
                                                                mycluster-2 (Pending)
t=2    Creates mycluster-3 (Pending)                            mycluster-1 (Pending)
                                                                mycluster-2 (Pending)
                                                                mycluster-3 (Pending)
t=3                                   mycluster-1 → InProgress
t=10                                  mycluster-1 → Applied
t=11                                  mycluster-2 → InProgress
t=12   Cleanup: deletes                                         mycluster-2 (InProgress)
       mycluster-1 (superseded)                                 mycluster-3 (Pending)
t=20                                  mycluster-2 → Applied
t=21                                  mycluster-3 → InProgress
t=22   Cleanup: deletes                                         mycluster-3 (InProgress)
       mycluster-2 (superseded)
t=30                                  mycluster-3 → Applied
       ← only mycluster-3 remains (current baseline) →
```

---

## State Machines

### RedkeyConfig — Configuration Phase

Tracks the lifecycle of each configuration change as processed by Robin.

```ascii
                        ┌──────────────────┐
                        │                  │
              ┌─────────▼──────────┐       │
              │      Pending       │       │
              └────┬──────────┬────┘       │
                   │          │            │
    (skip policy)  │          │ (process)  │
                   │          │            │
         ┌─────────▼──┐   ┌───▼──────────┐ │
         │ Superseded │   │  InProgress  │ │
         └────────────┘   └───┬──────────┘ │
                              │            │
                              │ (complete) │
                              │            │
                        ┌─────▼────────┐   │
                        │   Applied    │   │
                        └──────────────┘   │
                                           │
         (All terminal phases eventually   │
          deleted by Operator cleanup) ────┘
```

| Transition | Trigger |
| ---------- | ------- |
| `Pending → InProgress` | Robin picks up the config as the next to process |
| `Pending → Superseded` | `skipIfSuperseded: true` and a higher-sequence `Pending` config exists |
| `InProgress → Applied` | Robin completes all K8s resource and Redis operations for this config |

### RedkeyConfig — Cluster Operational Status

Tracks the operational state of the Redis/Valkey cluster as reported by Robin in `status.status`.

```ascii
                    ┌────────────────┐
                    │ Initializing   │
                    └───────┬────────┘
                            │
                    ┌───────▼────────┐
               ┌───►│  Configuring   │◄──────────────────────────┐
               │    └───────┬────────┘                           │
               │            │                                    │
               │    ┌───────▼────────┐                           │
               │    │     Ready      │◄──────────────────────┐   │
               │    └──┬──┬──┬──┬────┘                       │   │
               │       │  │  │  │                            │   │
               │       │  │  │  └──────┐                     │   │
               │       │  │  │         │                     │   │
               │  ┌────▼┐ │ ┌▼─────┐  ┌▼───────────┐         │   │
               │  │Scale│ │ │Scale │  │  Upgrading │─────────┘   │
               │  │ Up  │ │ │Down  │  └────────────┘             │
               │  └──┬──┘ │ └──┬───┘                             │
               │     │    │    │                                 │
               │     └────┼────┘─────────────────────────────────┘
               │          │
               │   ┌──────▼──────┐
               │   │ Rebalancing │─────────────┐
               │   └─────────────┘             │
               │                        (back to Ready)
               │
               │   ┌─────────────┐
               ├───│    Error    │
               │   └─────────────┘
               │
               │   ┌─────────────┐
               └───│ Maintenance │
                   └─────────────┘
```

| Status | Description |
| ------ | ----------- |
| `Initializing` | K8s resources are being created; waiting for pods to be ready. |
| `Configuring` | Redis cluster is being formed: `CLUSTER MEET`, slot assignment, replication. |
| `Ready` | Cluster has the correct topology, is balanced, and is serving traffic. |
| `ScalingUp` | Adding nodes to the cluster and redistributing slots. |
| `ScalingDown` | Removing nodes from the cluster after migrating slots away. |
| `Upgrading` | Rolling out image, configuration, or resource changes. |
| `Rebalancing` | Autonomously redistributing slots to restore balance (e.g. after failover). |
| `Error` | An unrecoverable issue was detected. Robin attempts recovery. |
| `Maintenance` | Manual maintenance mode. |

### Redkey — Aggregated Phase

The Operator computes a simplified phase for `Redkey.status.phase` from the highest-sequence config:

```ascii
                  ┌─────────────┐
             ┌───►│ Configuring │◄──┐
             │    └──────┬──────┘   │
             │           │          │
             │    ┌──────▼──────┐   │
             └────│    Ready    │───┘
                  └──────┬──────┘
                         │
                  ┌──────▼──────┐
                  │    Error    │
                  └─────────────┘
```

| Highest-Sequence Config State | `Redkey.status.phase` | `ConfigPending` Condition |
| ----------------------------- | ---------------------------- | ------------------------- |
| `configPhase: Pending` | `Configuring` | `True` |
| `configPhase: InProgress` | `Configuring` | `True` |
| `configPhase: Applied`, `status: Ready` | `Ready` | `False` |
| `configPhase: Applied`, `status: Error` | `Error` | `False` |
| `configPhase: Applied`, any other status | `Configuring` | `False` |

> The [health conditions](redkey-cluster-status.md#cluster-health-conditions) (`Healthy`,
> `SlotsBalanced`, …) are aggregated onto the cluster **in addition** to the table above but do
> **not** influence `phase`: a cluster stays `Ready` while Robin heals or rebalances an applied
> cluster. They are an orthogonal health axis.

---

## RBAC Model

The architecture enforces strict ownership boundaries at the RBAC level. The `RedkeyConfig` CRD has a status subresource, which means writes to the spec and writes to the status are independent API calls controlled by separate RBAC rules.

### Operator Permissions

The Operator's `ClusterRole` grants:

| Resource | Verbs | Purpose |
| -------- | ----- | ------- |
| `Redkey` | `get`, `list`, `watch`, `create`, `update`, `patch`, `delete` | Full lifecycle management |
| `Redkey/status` | `get`, `update`, `patch` | Write aggregated status |
| `RedkeyConfig` | `get`, `list`, `watch`, `create`, `delete` | Create new configs, delete superseded |
| `RedkeyConfig/status` | `get`, `update`, `patch` | Read Robin's reported status |
| `Deployments` | `get`, `list`, `watch`, `create`, `update`, `patch`, `delete` | Manage Robin Deployment |
| `ServiceAccounts`, `Roles`, `RoleBindings` | `get`, `list`, `watch`, `create`, `update`, `patch`, `delete` | Manage per-cluster RBAC |

### Robin Permissions (per-cluster Role)

Each Robin instance gets a scoped `Role` created by the Operator:

| Resource | Verbs | Purpose |
| -------- | ----- | ------- |
| `StatefulSets` | `get`, `list`, `watch`, `create`, `update`, `patch`, `delete` | Manage Redis pods |
| `Services`, `ConfigMaps` | `get`, `list`, `watch`, `create`, `update`, `patch`, `delete` | Manage headless Service and redis.conf |
| `PodDisruptionBudgets` | `get`, `list`, `watch`, `create`, `update`, `patch`, `delete` | Manage PDB |
| `Secrets` | `get` | Read-only access for auth credentials |
| `RedkeyConfig` | `get`, `list`, `watch` | Read desired spec |
| `RedkeyConfig/status` | `update`, `patch` | Write operational status |

Robin is explicitly **denied** write access to the `RedkeyConfig` main resource — it cannot alter the desired spec set by the Operator.

---

## Reconciliation Flows

### Operator Reconciliation

The Operator is event-driven via watches on `Redkey` and its owned resources (`RedkeyConfig`, `Deployment`, `ServiceAccount`, `Role`, `RoleBinding`). A periodic resync (configurable via `--resync-interval`, default 5 minutes) acts as a safety net.

The controller processes its work queue with multiple concurrent workers (configurable via `--max-concurrent-reconciles`, default 10) so a single slow or backing-off cluster cannot starve reconciliation of the others.

On each reconciliation:

```ascii
┌─────────────────────────────────┐
│   Fetch Redkey CR        │
│   (exit if not found — GC)      │
└──────────┬──────────────────────┘
           │
┌──────────▼──────────────────────┐
│   Ensure RBAC resources         │
│   (ServiceAccount, Role,        │
│    RoleBinding for Robin)       │
└──────────┬──────────────────────┘
           │
┌──────────▼──────────────────────┐
│   Ensure Robin Deployment       │
│   (create or patch on drift)    │
└──────────┬──────────────────────┘
           │
┌──────────▼──────────────────────┐
│   List RedkeyConfigs     │
│   (sorted by sequence)          │
└──────────┬──────────────────────┘
           │
┌──────────▼──────────────────────┐
│   Create new config?            │
│   (if generation changed or     │
│    no configs exist)            │
└──────────┬──────────────────────┘
           │
┌──────────▼──────────────────────┐
│   Cleanup superseded configs    │
│   (delete terminal configs      │
│    except highest sequence)     │
└──────────┬──────────────────────┘
           │
┌──────────▼──────────────────────┐
│   Aggregate status              │
│   (highest-seq config status    │
│    → Redkey status)      │
└──────────┬──────────────────────┘
           │
┌──────────▼──────────────────────┐
│   Requeue after resync interval │
└─────────────────────────────────┘
```

### Robin Reconciliation Loop

Robin operates through a single polling loop with configurable idle, wait, and error intervals. It uses an adaptive strategy: in busy mode (processing configs or corrective actions) it loops immediately; in idle mode (all checks pass, no pending configs) it waits `intervalSeconds`; while waiting for readiness or convergence it uses `intervalOnWaitSeconds`; and after reconciliation errors it retries on `intervalOnErrorSeconds`.

On each tick:

```ascii
┌─────────────────────────────────────┐
│  List RedkeyConfigs          │
│  (by label, sorted by sequence)     │
└──────────┬──────────────────────────┘
           │
┌──────────▼──────────────────────────┐
│  Process pending configs            │
│  ┌────────────────────────────────┐ │
│  │ If InProgress: continue work   │ │
│  │ If Pending:                    │ │
│  │   - Check skipIfSuperseded     │ │
│  │   - Set InProgress             │ │
│  │   - Create/update K8s objects  │ │
│  │   - Drive Redis operations     │ │
│  │   - Set Applied when done      │ │
│  └────────────────────────────────┘ │
└──────────┬──────────────────────────┘
           │
┌──────────▼──────────────────────────┐
│  Verify Kubernetes resources        │
│  (StatefulSet, Service, PDB,        │
│   ConfigMap match expected state)   │
└──────────┬──────────────────────────┘
           │
┌──────────▼──────────────────────────┐
│  Verify Redis cluster health        │
│  - Node membership (CLUSTER MEET)   │
│  - Cluster integrity (CLUSTER FIX)  │
│  - Slot coverage (16384 slots)      │
│  - Replication topology             │
│  - Slot balance across primaries    │
│  → Trigger Rebalancing if needed    │
└──────────┬──────────────────────────┘
           │
┌──────────▼──────────────────────────┐
│  Write status to active             │
│  RedkeyConfig.status         │
└──────────┬──────────────────────────┘
           │
┌──────────▼──────────────────────────┐
│  Wait (idle) or loop (busy)         │
└─────────────────────────────────────┘
```

---

## Kubernetes Resources Managed

The following diagram shows resource ownership in the system:

```ascii
Redkey (user-facing CR)
│
├── owned by Operator:
│   ├── RedkeyConfig (sequenced, one per spec change)
│   ├── Deployment (<cluster>-robin)
│   ├── ServiceAccount (<cluster>-robin)
│   ├── Role (<cluster>-robin)
│   └── RoleBinding (<cluster>-robin)
│
└── managed by Robin (reads spec from RedkeyConfig):
    ├── StatefulSet (<cluster>)
    ├── Service (<cluster>, headless)
    ├── ConfigMap (<cluster>, redis.conf)
    └── PodDisruptionBudget (<cluster>)
```

Operator-owned resources carry `ownerReferences` pointing to the `Redkey` CR, so they are automatically garbage-collected by Kubernetes when the CR is deleted.
