<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Dynamic Configuration (Hot Reload)

Robin applies configuration changes **at runtime without requiring a Pod restart**. This document explains which settings are hot-reloadable, how the mechanism works, and what the user needs to do.

## Which Settings Are Hot-Reloadable?

All settings in `spec.robin.config` are applied dynamically:

| Setting | Field | Effect |
| ------- | ----- | ------ |
| Reconciler idle interval | `reconciler.intervalSeconds` | How often Robin's reconciliation loop ticks when the cluster is idle |
| Reconciler error interval | `reconciler.intervalOnErrorSeconds` | How often Robin retries after a reconciliation error |
| Reconciler wait interval | `reconciler.intervalOnWaitSeconds` | How often Robin retries while waiting for readiness or convergence |
| Metrics collection interval | `metrics.collectionIntervalSeconds` | How often Robin polls Redis nodes for INFO metrics |
| Redis INFO keys | `metrics.redisInfoKeys` | Which metrics are collected and exposed to Prometheus |
| Connection retries | `cluster.connectionMaxRetries` | Max retries when connecting to a Redis node |
| Connection backoff | `cluster.connectionBackOffSeconds` | Wait time between connection retries |
| Cluster command timeout | `cluster.clusterCommandTimeoutSeconds` | Timeout for `redis-cli --cluster fix` commands (default: 24) |
| Rebalance timeout | `cluster.rebalanceTimeoutSeconds` | Timeout for `redis-cli --cluster rebalance` commands (default: 120) |
| Cluster meet wait | `cluster.clusterMeetWaitSeconds` | Time to wait for gossip propagation after `CLUSTER MEET` (default: 5) |
| Auth secret reference | `spec.auth.secret` | Which Secret holds the Redis password |

If any reconciler interval field is omitted from the CR, Robin keeps the value it started with. In particular, `intervalOnWaitSeconds` falls back to Robin's startup default of 10 seconds unless its CLI flag overrides it.

## How It Works

The hot-reload mechanism relies on the `RedkeyClusterConfig` CRD as the communication channel between the Operator and Robin:

```ascii
┌─────────────────────┐    creates     ┌────────────────────────┐
│  User edits         │──────────────► │  RedkeyClusterConfig   │
│  RedkeyCluster spec │   (Operator)   │  spec.robinConfig      │
└─────────────────────┘                │  spec.auth             │
                                       └───────────┬────────────┘
                                                   │
                                         polls each tick
                                                   │
                                       ┌───────────▼────────────┐
                                       │  Robin Reconciler      │
                                       │  reads config, updates │
                                       │  RuntimeConfig store   │
                                       └───────────┬────────────┘
                                                   │
                                        ┌──────────┼──────────┐
                                        │          │          │
                                        ▼          ▼          ▼
                                   Reconciler  Metrics    Auth
                                   interval    collector  password
                                   adjusted    re-reads   re-fetched
```

### Step by Step

1. **User updates `spec.robin.config`** in the `RedkeyCluster` resource (for example, changes `reconciler.intervalSeconds`, `reconciler.intervalOnErrorSeconds`, or `reconciler.intervalOnWaitSeconds`).

2. **Operator detects the change** and creates a new `RedkeyClusterConfig` with an incremented sequence number, carrying the updated `robinConfig`.

3. **Robin's reconciler** picks up the new `RedkeyClusterConfig` on its next tick (within the current interval).

4. **Robin writes the new values** into its in-memory `RuntimeConfig` store — a thread-safe shared structure protected by a read-write mutex.

5. **Components read from RuntimeConfig** on each cycle:
   - The **reconciler** checks its idle, error, and wait intervals at the end of each tick, so a new value takes effect immediately on the next sleep for that scheduling path.
   - The **metrics collector** reads the collection interval, `redisInfoKeys`, and `metricsLabels` at the start of each polling cycle.
   - If `metricsLabels` keys are added or removed, Robin resets only its RedKey metrics registry so Prometheus descriptors can be re-created with the new label-name set. Value-only label changes reuse the existing metric schema.
   - The **auth cache** detects when the secret name changes and invalidates the cached password, fetching from the new Secret on the next collection.

### No Restart Required

Unlike the legacy ConfigMap-based approach (which required Pod recreation via checksum annotations), the CRD-based `robinConfig` does **not** trigger a Deployment rollout. Robin continuously watches the `RedkeyClusterConfig` resources and applies changes in-flight.

## Example: Changing Reconciler Intervals

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: RedkeyCluster
metadata:
  name: my-cluster
spec:
  # ...
  robin:
    config:
      reconciler:
        intervalSeconds: 30         # Idle polling
        intervalOnErrorSeconds: 5  # Retry after errors
        intervalOnWaitSeconds: 10  # Retry while waiting/converging
```

After applying this change:

- The Operator creates a new `RedkeyClusterConfig` (e.g. sequence 5).
- Robin processes it within the current interval (≤ 10s in this case).
- From that point on, Robin sleeps 30 seconds when idle, 5 seconds after errors, and 10 seconds while waiting for readiness or cluster convergence.

## Example: Updating Metrics Collection

```yaml
robin:
  config:
    metrics:
      collectionIntervalSeconds: 15
      redisInfoKeys:
        - connected_clients
        - used_memory
        - total_commands_processed
        - evicted_keys        # ← added
        - keyspace_hits       # ← added
```

After applying:

- Robin picks up the new config on the next reconciliation tick.
- The metrics collector starts using the new 15-second interval and collecting the two additional keys.
- New Prometheus gauges (`redis_evicted_keys`, `redis_keyspace_hits`) appear automatically.

## Example: Switching Auth Secret

```yaml
spec:
  auth:
    secret: my-new-redis-secret  # Changed from my-redis-secret
```

After applying:

- Robin detects that the secret name changed.
- The cached password is invalidated.
- On the next metrics collection cycle, Robin fetches the password from `my-new-redis-secret`.

See [Redis Authentication](authentication.md) for full details on auth configuration.

## Propagation Timing

| Event | Latency |
| ----- | ------- |
| User applies RedkeyCluster change | Immediate (kubectl/API) |
| Operator creates new RedkeyClusterConfig | Operator reconcile interval |
| Robin picks up new config | Current Robin reconciler interval |

The new values take effect on the **next cycle** of the affected component after being written to `RuntimeConfig`. There is no additional delay.

## Design Principles

- **Pull-based**: Robin polls `RedkeyClusterConfig` resources. There is no push notification or webhook.
- **Thread-safe**: `RuntimeConfig` uses a read-write mutex. The reconciler is the sole writer; other components only read.
- **Atomic**: All settings from a single `RedkeyClusterConfig` are applied together in one write.
- **Monotonic**: Configs carry a sequence number. Robin always applies the highest-sequence config, ignoring stale ones.
