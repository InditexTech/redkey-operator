<!--
SPDX-FileCopyrightText: 2026 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Scaling a Redkey Cluster

![Redkey Operator icon](../images/redkey-logo-128.png)

Redkey clusters can be scaled **up** (more capacity / availability) or **down** (fewer
nodes) by changing the topology fields of the `Redkey` resource. Scaling is
orchestrated entirely by **Redkey Robin** — the operator only records the desired
topology in a new `RedkeyConfig`, and Robin drives the cluster through the
necessary Redis cluster operations.

> Robin owns the StatefulSet during scaling. It adjusts the replica count, migrates
> slots, and attaches/detaches replicas. The operator never reshapes the cluster
> topology directly.

## Triggering a scale operation

Scaling is triggered by updating either field of the `Redkey` spec:

| Field | Effect |
| :--- | :--- |
| `spec.primaries` | Number of primary (master) shards. Increasing scales up; decreasing scales down. |
| `spec.replicasPerPrimary` | Number of replicas attached to each primary. Changing it scales the cluster in or out. |

### Using `kubectl scale`

The `Redkey` CRD exposes a **scale subresource**, so you can change the number of
primaries with the standard Kubernetes scaling interface:

```bash
# Scale the cluster to 5 primaries
kubectl scale redkey my-cluster --replicas=5

# Verify
kubectl get redkey my-cluster
```

The `--replicas` flag maps to `spec.primaries`. The current number of primaries is
reported in `status.replicas`, so tools like Horizontal Pod Autoscalers (HPA) work as
expected.

> **Note:** `kubectl scale` only changes the number of primaries. To modify
> `replicasPerPrimary` you must edit the resource spec directly.

The **total node count** is always:

```text
totalNodes = primaries + (primaries * replicasPerPrimary)
```

Example — scaling primaries from 3 to 5 on a cluster with 1 replica each grows the
cluster from `3 + 3 = 6` nodes to `5 + 5 = 10` nodes.

When Robin detects a topology change it transitions the cluster status to
`ScalingUp` or `ScalingDown` and begins the corresponding flow. The status returns to
`Ready` once the new topology is applied and converged (slots stable, no in-flight
migration). Note that `Ready` reflects the **applied topology**; ongoing data-plane health —
including any background rebalancing Robin performs afterwards — is tracked separately via the
[health conditions](../redkey-cluster-status.md#cluster-health-conditions).

## Node ordinals and roles

Robin keeps the **lowest StatefulSet ordinals as primaries** and the higher ordinals as
replicas. This aligns with how Kubernetes scales a StatefulSet (it always removes the
highest ordinals first), so scale-down naturally drops replica/extra pods last.

During a scale-up, Robin uses topology-aware classification to assign roles:

- **Scale up** that increases `primaries`: Robin detects which pods are truly new (not
  yet part of the cluster) and introduces only the required number as new primaries.
  Existing replicas are **never** reset or disrupted — they remain attached to their
  original primaries throughout the operation.
- **Scale down** that decreases `primaries`: a pod that was a primary may need to become
  a replica. Robin drains its slots first, then re-attaches it as a replica.

## Normal scaling (data preserved)

Normal scaling migrates slots between nodes so **no data is lost**. It is used for all
clusters except those eligible for [fast scaling](#fast-scaling-data-purged).

### Scale up flow

1. **Grow the StatefulSet.** Robin sets the StatefulSet replica count to the new total
   (all new primaries *and* replicas) and waits for every pod to be `Running` and
   `Ready`.
2. **Discover topology.** Robin queries the existing cluster to classify nodes:
   - *Existing primaries*: cluster members that are masters with slots assigned.
   - *Existing replicas*: cluster members that are replicas (left untouched).
   - *New nodes*: pods not yet in the cluster. Robin calculates how many new primaries
     are needed (`targetPrimaries − existingPrimaries`) and assigns that many new nodes
     the primary role; the rest will become replicas.
3. **Join new primaries only.** Only the nodes destined to become primaries are
   introduced to the cluster with `CLUSTER MEET`. Existing replicas are never reset or
   disrupted. Future replica nodes are **not** joined yet — this prevents the rebalance
   from distributing slots to them.
4. **Rebalance slots** ⚠️ *(`TRYAGAIN` window)*. Robin runs
   `redis-cli --cluster rebalance <node> --cluster-use-empty-masters --cluster-pipeline 50 --cluster-yes`,
   moving slots from the existing primaries onto the new empty masters. The
   `--cluster-pipeline 50` flag batches 50 keys per `MIGRATE` call to speed up the
   transfer. A full reshard
   moves all affected slots (and their keys) in a single `redis-cli` invocation, which
   may take several minutes on clusters that hold data; the rebalance timeout
   (`cluster.rebalanceTimeoutSeconds`, default `600`) must be generous enough to let it
   finish. Robin retries a failed rebalance with backoff. The retry decision is based on
   the cluster's **live topology** (`CLUSTER NODES`), not on parsing `redis-cli` error
   text: before each attempt Robin inspects the reported slots and, if a previous
   (possibly interrupted) rebalance left any slot in an open `MIGRATING`/`IMPORTING`
   state, repairs it with `redis-cli --cluster fix` before retrying — so the cluster
   always converges. Retries stop only when the operation succeeds, the attempts are
   exhausted, or Robin is shutting down. This phase is **skipped on re-entry** if all
   target primaries already hold slots (idempotency).
5. **Wait for stable slots.** Robin waits until no slot is in a `MIGRATING`/`IMPORTING`
   state before continuing.
6. **Join and attach replicas.** Only once slots are stable, the new replica nodes are
   introduced to the cluster with `CLUSTER MEET`, and each replica is attached to its
   primary with `CLUSTER REPLICATE`. Attaching replicas after the rebalance avoids
   wasteful double resynchronisation and ensures the rebalance never assigns slots to
   replica-destined nodes.
7. **Validate.** Robin verifies the cluster state is `ok` with all 16384 slots assigned,
   then returns the status to `Ready`.

### Scale down flow

1. **Drain the outgoing primaries** ⚠️ *(`TRYAGAIN` window)*. Robin runs a weighted
   rebalance giving the surviving primaries weight `1` and the primaries to be removed
   weight `0`, moving all their slots away. Transient errors are retried with backoff.
   Robin confirms the drained primaries hold **0 slots** before proceeding.
2. **Remove topology — replicas first.** Replicas of the removed nodes are removed before
   their primaries to avoid spurious failovers. Each removal is propagated to every
   surviving node with `CLUSTER FORGET`, and each removed node is shut down cleanly with
   `SHUTDOWN SAVE`.
3. **Shrink the StatefulSet.** With the cluster no longer referencing the surplus pods,
   Robin reduces the StatefulSet replica count. Because the nodes were already forgotten
   and shut down, the cluster does not wait on `cluster-node-timeout`.
4. **Re-attach replicas** (if `replicasPerPrimary > 0`) and validate the cluster is
   healthy before returning to `Ready`.

## Fast scaling (data purged)

Fast scaling **recreates the cluster from scratch** at the new size. It is dramatically
faster but **destroys all data** and makes the cluster unavailable during the operation.

Robin uses fast scaling **only** when all of the following are true:

| Requirement | Reason |
| :--- | :--- |
| `spec.ephemeral: true` | No persistent volumes — there is no data to preserve. |
| `spec.replicasPerPrimary: 0` | No replicas to keep consistent. |
| `spec.purgeKeysOnRebalance: true` | Explicit opt-in to discarding keys on topology change. |
| Cluster currently has no replica nodes | Transitioning from `replicasPerPrimary > 0` to `0` requires gracefully removing replicas first. |

If any requirement is not met, Robin falls back to [normal scaling](#normal-scaling-data-preserved).

The fast scaling flow:

1. Robin deletes the existing StatefulSet so Kubernetes terminates all pods.
2. Robin recreates the cluster objects with the new node count.
3. Once the new pods are `Ready`, Robin forms a fresh cluster from scratch and returns to
   `Ready`.

See [Key purge (purgeKeysOnRebalance)](../purge-keys-on-rebalance.md) for more on this
mode.

## `TRYAGAIN` and application clients

While slots are migrating (during the rebalance phase of both scale up and scale down),
**multi-key operations** (`MGET`, `MSET`, transactions, Lua scripts) that touch a slot in
flight will fail with a `TRYAGAIN` error. This is a normal, transient cluster state — not
a failure.

Robin handles `TRYAGAIN` internally for its own rebalance commands. **Application
clients must also retry `TRYAGAIN` with exponential backoff** to achieve true
zero-downtime scaling. Most mature Redis cluster client libraries do this automatically;
verify your client is configured accordingly.

| Flow phase | Slot state | `TRYAGAIN` risk | Client action |
| :--- | :--- | :--- | :--- |
| Scale up — before rebalance | `NODE` (stable) | No | Normal. |
| Scale up — during rebalance | `MIGRATING` / `IMPORTING` | **Yes** | Retry with backoff. |
| Scale up — after rebalance | `NODE` (stable) | No | Normal. |
| Scale down — during drain | `MIGRATING` / `IMPORTING` | **Yes** | Retry with backoff. |
| Scale down — node removal | empty | No | Normal (may see `MOVED` on stale cache). |

## Scaling capability matrix

| Cluster type | Replicas | `purgeKeysOnRebalance` | Current replica nodes | Scaling mode |
| :--- | :--- | :--- | :--- | :--- |
| Ephemeral | 0 | `true` | None | **Fast** (data purged) |
| Ephemeral | 0 | `true` | Present (transitioning from >0) | Normal (data preserved) |
| Ephemeral | 0 | `false`/unset | any | Normal (data preserved) |
| Ephemeral | ≥ 1 | any | any | Normal (data preserved) |
| Persistent | 0 | n/a | any | Normal (data preserved) |
| Persistent | ≥ 1 | n/a | any | Normal (data preserved) |

## Scale to zero

A Redkey cluster can be **scaled to zero primaries**, which removes all Redis
infrastructure from the namespace while preserving the `Redkey` resource itself.
This is useful for development/staging environments, cost savings during off-peak hours,
or as a building block for external autoscalers.

### Behaviour

| Scenario | Effect |
| :--- | :--- |
| **Create with `spec.primaries: 0`** | No-op: the operator creates **nothing** — no Robin, no RBAC, no StatefulSet, no configs. Status is set to `Ready` with `replicas: 0`. |
| **Scale from >0 to 0** | The operator creates a `RedkeyConfig` with `primaries: 0`. Robin deletes the StatefulSet, Service, ConfigMap, and PDB. If `spec.deletePVC: true`, PVCs are also deleted. Once Robin marks the config as Applied, the operator cleans up Robin's Deployment, RBAC, and all configs. |
| **Scale from 0 to >0** | Normal creation path: the operator creates RBAC, Robin Deployment, and a new config. Robin creates all cluster objects from scratch. **For storage (non-ephemeral) clusters** the target topology is constrained — see [Scale-up-from-zero topology lock](#scale-up-from-zero-topology-lock-storage-clusters). |

### Scale-up-from-zero topology lock (storage clusters)

For **storage (non-ephemeral) clusters**, scaling **up from zero** is only allowed back to the
**exact same topology** the cluster last ran with — the same `spec.primaries` **and**
`spec.replicasPerPrimary`. This is enforced by a CEL validation rule on the `Redkey` CRD and
rejects the change at admission time (e.g. `kubectl apply`/`kubectl scale`).

**Why:** persistent volumes keep the Redis node/slot metadata of the topology that created them.
Coming back from zero with a *different* number of primaries (or replicas) would remount those PVCs
into a mismatched layout and produce an inconsistent cluster. Locking the scale-up to the previous
topology prevents that.

What is and isn't restricted:

- ✅ **Free scaling while `primaries > 0`** is unrestricted — scale up/down to any valid topology.
- ✅ **Ephemeral clusters** are never restricted (they hold no persistent data, so a fresh layout is fine).
- ✅ **Fresh clusters created at `primaries: 0`** (that never ran) can scale up to any topology.
- ❌ A storage cluster that ran at, say, `3` primaries / `1` replica, was scaled to `0`, and is then
  scaled up to a different topology (e.g. `2` primaries, or `3` primaries / `2` replicas) is **rejected**.
  The exact topology to return to is published in `.status.lastAppliedPrimaries` and
  `.status.lastAppliedReplicasPerPrimary`, and the rejection message points to those fields.

> **Race-free by design — do not change.** The rule reads `status.lastAppliedPrimaries` /
> `status.lastAppliedReplicasPerPrimary`, which the operator updates **continuously** on every
> successful reconcile while the cluster runs at `primaries > 0` (never only at scale-to-zero, and
> never cleared). Recording it continuously guarantees the value is already persisted **before** any
> scale-to-zero, which is what makes the check free of the admission-vs-reconcile race. Changing this
> to record-only-at-scale-down, or adding clearing logic, would reintroduce that race and allow
> inconsistent PVC remounts.

### Example

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: Redkey
metadata:
  name: my-cluster
spec:
  primaries: 0          # Scale to zero
  ephemeral: true
  robin:
    image: redkey-robin:latest
```

Or using `kubectl scale`:

```bash
# Scale to zero
kubectl scale redkey my-cluster --replicas=0

# Scale back up
kubectl scale redkey my-cluster --replicas=3
```

> **Storage clusters:** the scale-back-up target must match the topology the cluster had before
> scaling to zero (see [Scale-up-from-zero topology lock](#scale-up-from-zero-topology-lock-storage-clusters)).
> A mismatched `--replicas` value is rejected by the API server.

### PVC handling

When scaling to zero with persistent storage:

- **`spec.deletePVC: true`** (default): Robin deletes all PersistentVolumeClaims. Data is
  lost and a fresh cluster is created when scaling back up.
- **`spec.deletePVC: false`**: PVCs are preserved. Because the volumes keep the previous cluster's
  node/slot metadata, scaling back up is **locked to the same topology** (same primaries and
  replicas) via the [scale-up-from-zero topology lock](#scale-up-from-zero-topology-lock-storage-clusters),
  which avoids remounting the volumes into an inconsistent layout.

### Status after scale to zero

Once the scale-to-zero operation completes:

```yaml
status:
  phase: Ready
  replicas: 0
  nodes: {}
  conditions:
    - type: Ready
      status: "True"
      reason: ScaledToZero
    - type: ConfigPending
      status: "False"
      reason: ScaledToZero
    - type: Error
      status: "False"
      reason: ScaledToZero
```

### How it works internally

1. The operator detects `spec.primaries == 0`.
2. **If no configs exist** (creation with 0, or already cleaned up): set status to Ready
   with 0 replicas. No objects are created.
3. **If the latest config has primaries > 0**: create a new config with `primaries: 0` so
   Robin can process the teardown. Keep Robin alive during this phase.
4. **Robin receives the config**: transitions status to `ScalingToZero`, deletes its
   managed objects (STS, Service, ConfigMap, PDB, optionally PVCs), marks the config as
   Applied.
5. **Operator sees config Applied**: deletes Robin Deployment, RBAC, and all configs. Sets
   final status.

## Related documentation

* [Key purge (purgeKeysOnRebalance)](../purge-keys-on-rebalance.md)
* [Primary-Replica Clusters](primary-replica-cluster.md)
* [Ephemeral Mode / Zero Persistent Volume Claims](ephemeral-cluster.md)
* [Standalone Mode (Single-Node Redkey)](standalone-cluster.md)
* [Deleting PVCs on ScaleDown and Deletion](delete-pvc.md)
* [Cluster Health Checks and Remediation](../cluster-health-checks.md)
