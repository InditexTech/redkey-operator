<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Redkey Cluster Metrics

This guide explains how to configure, expose, and consume cluster metrics collected by Redkey Robin.

## Table of Contents

- [Overview](#overview)
- [Configuration](#configuration)
  - [Metrics Collection Interval](#metrics-collection-interval)
  - [Redis INFO Keys](#redis-info-keys)
  - [Custom Metrics Labels](#custom-metrics-labels)
  - [Full Example](#full-example)
- [Exposed Metrics](#exposed-metrics)
  - [Redis INFO Metrics](#redis-info-metrics)
  - [Keyspace Metrics](#keyspace-metrics)
  - [Cluster INFO Metrics](#cluster-info-metrics)
  - [Cluster Nodes Metrics](#cluster-nodes-metrics)
  - [Cluster Health Metrics](#cluster-health-metrics)
  - [String Values](#string-values)
  - [Compound Values](#compound-values)
- [Labels](#labels)
  - [Common Labels](#common-labels)
  - [Metric-Specific Labels](#metric-specific-labels)
  - [Custom Labels](#custom-labels)
- [Consuming Metrics](#consuming-metrics)
  - [Prometheus Endpoint](#prometheus-endpoint)
  - [ServiceMonitor](#servicemonitor)
  - [PromQL Examples](#promql-examples)
  - [Alerting Examples](#alerting-examples)
- [Dynamic Reconfiguration](#dynamic-reconfiguration)

---

## Overview

Redkey Robin collects node-level Redis metrics from every discovered node with `INFO ALL`, and cluster-level topology and health signals from a reachable seed node with `CLUSTER INFO`, `CLUSTER NODES`, and `redis-cli --cluster check`. The collected data is exposed as Prometheus gauges on Robin's HTTP metrics endpoint (default port 8080, path `/metrics`).

All exported metric names use the `redkey_` prefix.

```ascii
┌──────────────┐       INFO ALL         ┌──────────────┐
│              │◄───────────────────────│  Redis       │
│              │       CLUSTER INFO     │  Node 0      │
│              │◄───────────────────────│              │
│   Redkey     │                        └──────────────┘
│   Robin      │       INFO ALL         ┌──────────────┐
│              │◄───────────────────────│  Redis       │
│   :8080      │       CLUSTER INFO     │  Node 1      │
│   /metrics   │◄───────────────────────│              │
│              │                        └──────────────┘
└──────┬───────┘                        ┌──────────────┐
       │                                │  Redis       │
       │                                │  Node N      │
       │                                └──────────────┘
       ▼
┌──────────────┐
│  Prometheus  │
│  Scrape      │
└──────────────┘
```

## Configuration

All metrics configuration is placed under `spec.robin.config.metrics` of the `RedkeyCluster` resource:

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: RedkeyCluster
metadata:
  name: my-cluster
spec:
  robin:
    config:
      metrics:
        collectionIntervalSeconds: 60
        redisInfoKeys:
          - connected_clients
          - used_memory
          - total_commands_processed
        metricsLabels:
          environment: production
          team: platform
```

### Metrics Collection Interval

| Field | Type | Default | Description |
| ----- | ---- | ------- | ----------- |
| `metrics.collectionIntervalSeconds` | int | 60 (Set by Robin) | Seconds between consecutive collection cycles |

Robin polls all Redis nodes at this interval. Lower values provide more granular data but increase load on Redis and network traffic.

### Redis INFO Keys

| Field | Type | Description |
| ----- | ---- | ----------- |
| `metrics.redisInfoKeys` | []string | List of Redis INFO keys to collect and expose |

Only keys listed here will be exported as node-scoped Prometheus metrics. Keys are matched after normalizing dashes to underscores (e.g., `used-memory` → `used_memory`).

- If `metrics.redisInfoKeys` is omitted, Robin keeps its built-in default list.
- If `metrics.redisInfoKeys` is set explicitly to an empty list (`[]`), Robin disables node-scoped `INFO` metrics.
- Cluster-level metrics and cluster health gauges are still collected even when `metrics.redisInfoKeys` is empty.

**Default metrics collected by Robin (if `redisInfoKeys` is not set):**

These keys are collected by default when `redisInfoKeys` is not configured. You can override this default set by specifying your own list.

- keyspace_hits
- evicted_keys
- connected_clients
- total_commands_processed
- keyspace_misses
- expired_keys
- redis_version
- used_memory_rss
- maxmemory
- used_cpu_sys
- used_cpu_sys_children
- used_cpu_user
- used_cpu_user_children
- total_net_input_bytes
- total_net_output_bytes
- aof_base_size
- aof_current_size
- mem_aof_buffer

**Common keys by category:**

| Category | Keys |
| -------- | ---- |
| Memory | `used_memory`, `used_memory_rss`, `used_memory_peak`, `maxmemory`, `mem_fragmentation_ratio` |
| Clients | `connected_clients`, `blocked_clients`, `tracking_clients` |
| Commands | `total_commands_processed`, `instantaneous_ops_per_sec` |
| Keyspace | `keyspace_hits`, `keyspace_misses`, `evicted_keys`, `expired_keys` |
| Network | `total_net_input_bytes`, `total_net_output_bytes` |
| CPU | `used_cpu_sys`, `used_cpu_user`, `used_cpu_sys_children`, `used_cpu_user_children` |
| Persistence | `aof_base_size`, `aof_current_size`, `rdb_last_bgsave_time_sec` |
| Replication | `connected_slaves`, `repl_backlog_size` |
| Server | `redis_version`, `uptime_in_seconds` |

### Custom Metrics Labels

| Field | Type | Description |
| ----- | ---- | ----------- |
| `metrics.metricsLabels` | map[string]string | Additional labels attached to every metric |

Custom labels are added to **all** metrics that Robin exposes. They are useful for identifying clusters in multi-tenant environments or for filtering in Grafana dashboards.

```yaml
metrics:
  metricsLabels:
    environment: production
    team: platform
    application: my-app
    domain: e-commerce
```

> **Note**: Avoid using reserved label names (`cluster`, `namespace`, `instanceId`) as custom labels — they are already set automatically by Robin.

### Full Example

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: RedkeyCluster
metadata:
  name: my-cluster
  namespace: redis-prod
spec:
  primaries: 3
  replicasPerPrimary: 1
  image: redis:7.2.4
  auth:
    secret: redis-auth
  robin:
    template:
      spec:
        containers:
          - image: redkey-robin:latest
            name: robin
            ports:
              - containerPort: 8080
                name: prometheus
                protocol: TCP
    config:
      reconciler:
        intervalSeconds: 30
      metrics:
        collectionIntervalSeconds: 30
        redisInfoKeys:
          - connected_clients
          - used_memory
          - used_memory_rss
          - maxmemory
          - total_commands_processed
          - instantaneous_ops_per_sec
          - keyspace_hits
          - keyspace_misses
          - evicted_keys
          - expired_keys
          - total_net_input_bytes
          - total_net_output_bytes
          - redis_version
        metricsLabels:
          environment: production
          team: platform
      cluster:
        connectionMaxRetries: 3
        connectionBackOffSeconds: 5
```

## Exposed Metrics

Unless noted otherwise, metric names follow `redkey_<normalized_name>`: dashes are converted to underscores, and any existing leading `redis_` or `redkey_` is collapsed into a single `redkey_` prefix.

### Redis INFO Metrics

For each key in `redisInfoKeys`, Robin exposes a Prometheus gauge named `redkey_<normalized_key>`. Numeric values are set directly; string values are handled as described in [String Values](#string-values).

**Example:** With `redisInfoKeys: [connected_clients, used_memory]`, you get:

```
redkey_connected_clients{cluster="my-cluster",namespace="redis-prod",instanceId="my-cluster-0",environment="production",team="platform"} 42
redkey_used_memory{cluster="my-cluster",namespace="redis-prod",instanceId="my-cluster-0",environment="production",team="platform"} 1048576
```

### Keyspace Metrics

Keyspace entries (e.g., `db0:keys=1000,expires=50,avg_ttl=2000`) are automatically split into individual gauges with an additional `database` label:

```
redkey_keyspace_keys{cluster="my-cluster",namespace="redis-prod",instanceId="my-cluster-0",database="db0",...} 1000
redkey_keyspace_expires{cluster="my-cluster",namespace="redis-prod",instanceId="my-cluster-0",database="db0",...} 50
redkey_keyspace_avg_ttl{cluster="my-cluster",namespace="redis-prod",instanceId="my-cluster-0",database="db0",...} 2000
```

> **Note**: Keyspace metrics are always collected if any `redisInfoKeys` are configured — no need to explicitly list `db0` etc.

### Cluster INFO Metrics

Robin exposes all fields from `CLUSTER INFO` returned by the reachable seed node selected for cluster health evaluation as labels on a single metric:

```
redkey_cluster_metrics{cluster="my-cluster",namespace="redis-prod",cluster_state="ok",cluster_slots_assigned="16384",cluster_slots_ok="16384",cluster_known_nodes="6",...} <current_timestamp>
```

This metric uses `SetToCurrentTime()` so its value represents the last successful collection timestamp.

### Cluster Nodes Metrics

Each node entry from the selected seed node's `CLUSTER NODES` output is exposed as:

```
redkey_nodes_metrics{cluster="my-cluster",namespace="redis-prod",nodeId="abc123",nodeIp="10.0.0.1",role="master",slots="0-5460",primaryId="-",state="connected",...} <current_timestamp>
```

This metric also uses `SetToCurrentTime()`, so its value represents the last successful collection timestamp for that node entry.

### Cluster Health Metrics

Robin computes cluster health during every collection cycle, independently from `metrics.redisInfoKeys`, using four inputs:

- membership consistency across all discovered nodes using `CLUSTER NODES`
- full primary slot coverage for all 16384 cluster slots
- slot balance across primaries
- `redis-cli --cluster check` against a reachable seed node

The following health-related gauges are exported:

| Metric | Meaning |
| ------ | ------- |
| `redkey_cluster_membership_ok` | `1` when all discovered nodes report the same visible cluster membership, all nodes are connected, and no failing flags such as `fail`, `pfail`, `noaddr`, or `handshake` are present |
| `redkey_cluster_slots_covered_ok` | `1` when primary nodes cover all 16384 slots exactly once |
| `redkey_cluster_balanced_ok` | `1` when the slot distribution across primaries stays within Robin's balance threshold |
| `redkey_cluster_healthy` | `1` only when membership, slot coverage, balance, and `redis-cli --cluster check` all succeed |
| `redkey_cluster_check_errors` | Number of `[ERR]` lines reported by `redis-cli --cluster check` |
| `redkey_cluster_check_warnings` | Number of `[WARNING]` lines reported by `redis-cli --cluster check` |
| `redkey_cluster_check_command_output_code` | Exit code returned by `redis-cli --cluster check`; `0` means success, non-zero means the command reported problems, and `-1` means Robin could not complete the command and exported conservative health defaults |

**Example:**

```
redkey_cluster_membership_ok{cluster="my-cluster",namespace="redis-prod",environment="production"} 1
redkey_cluster_slots_covered_ok{cluster="my-cluster",namespace="redis-prod",environment="production"} 1
redkey_cluster_balanced_ok{cluster="my-cluster",namespace="redis-prod",environment="production"} 1
redkey_cluster_healthy{cluster="my-cluster",namespace="redis-prod",environment="production"} 1
redkey_cluster_check_errors{cluster="my-cluster",namespace="redis-prod",environment="production"} 0
redkey_cluster_check_warnings{cluster="my-cluster",namespace="redis-prod",environment="production"} 0
redkey_cluster_check_command_output_code{cluster="my-cluster",namespace="redis-prod",environment="production"} 0
```

### String Values

When a Redis INFO key has a non-numeric value (e.g., `redis_version: "7.2.4"`), Robin exposes it as a gauge with value `-1` and the string as an additional label:

```
redkey_version{cluster="my-cluster",namespace="redis-prod",instanceId="my-cluster-0",redis_version="7.2.4",...} -1
```

Because Robin keeps only one `redkey_` prefix, `redis_version` becomes `redkey_version` rather than `redkey_redis_version`.

This pattern allows you to use PromQL label matching to detect version drift or track changes across nodes.

### Compound Values

When a value contains `key=value` pairs separated by commas (e.g., `cmdstat_set:calls=100,usec=500,usec_per_call=5.0`), Robin splits them into individual sub-metrics:

```
redkey_cmdstat_set_calls{...} 100
redkey_cmdstat_set_usec{...} 500
redkey_cmdstat_set_usec_per_call{...} 5.0
```

## Labels

### Common Labels

Every metric includes the following labels automatically:

| Label | Source | Example |
| ----- | ------ | ------- |
| `cluster` | RedkeyCluster name | `my-cluster` |
| `namespace` | RedkeyCluster namespace | `redis-prod` |

### Metric-Specific Labels

Some metrics add extra labels depending on their scope:

| Metric family | Additional labels |
| ------------- | ----------------- |
| Node-scoped `INFO` and keyspace metrics | `instanceId` |
| Keyspace metrics | `database` |
| `redkey_nodes_metrics` | `nodeId`, `nodeIp`, `role`, `slots`, `primaryId`, `state` |
| `redkey_cluster_metrics` | all raw `CLUSTER INFO` fields as labels |

Cluster-scoped health gauges such as `redkey_cluster_healthy` do not include `instanceId` because they describe the cluster as a whole.

### Custom Labels

Labels defined in `metrics.metricsLabels` are appended to every metric. This enables:

- **Environment separation**: `environment: "production"` vs `environment: "staging"`
- **Team ownership**: `team: "platform"`
- **Application context**: `application: "checkout-service"`
- **Grafana variable filters**: Use label values as Grafana template variables for dashboard filtering

## Consuming Metrics

### Prometheus Endpoint

Robin exposes metrics on port 8080 at `/metrics` by default. Ensure the Robin Pod template includes the Prometheus port:

```yaml
robin:
  template:
    spec:
      containers:
        - name: robin
          ports:
            - containerPort: 8080
              name: prometheus
              protocol: TCP
```

### ServiceMonitor

To configure Prometheus scraping via the Prometheus Operator, create a `ServiceMonitor`:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: my-cluster-robin
  namespace: redis-prod
  labels:
    release: prometheus  # Match your Prometheus instance selector
spec:
  selector:
    matchLabels:
      app.kubernetes.io/component: robin
      app.kubernetes.io/instance: my-cluster
  endpoints:
    - port: prometheus
      interval: 30s
      path: /metrics
```

Alternatively, use Pod annotations for auto-discovery (if your Prometheus is configured for annotation-based scraping):

```yaml
robin:
  template:
    metadata:
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "8080"
        prometheus.io/path: "/metrics"
```

### PromQL Examples

**Memory usage per node:**

```promql
redkey_used_memory{cluster="my-cluster"}
```

**Hit rate:**

```promql
redkey_keyspace_hits / (redkey_keyspace_hits + redkey_keyspace_misses)
```

**Connected clients across the cluster:**

```promql
sum(redkey_connected_clients{cluster="my-cluster"}) by (cluster)
```

**Operations per second (rate):**

```promql
rate(redkey_total_commands_processed{cluster="my-cluster"}[5m])
```

**Memory fragmentation ratio per node:**

```promql
redkey_used_memory_rss / redkey_used_memory
```

**Detect version inconsistencies:**

```promql
count(count by (redis_version) (redkey_version{cluster="my-cluster"})) > 1
```

**Keyspace keys per database:**

```promql
redkey_keyspace_keys{cluster="my-cluster"}
```

**Alert on unhealthy clusters:**

```promql
redkey_cluster_healthy{cluster="my-cluster"} == 0
```

**Inspect cluster check findings:**

```promql
redkey_cluster_check_errors{cluster="my-cluster"} + redkey_cluster_check_warnings{cluster="my-cluster"}
```

**Filter by custom label:**

```promql
redkey_connected_clients{environment="production", team="platform"}
```

### Alerting Examples

The health gauges are cluster-scoped, so they are a good fit for Prometheus alerts. A practical starting point is to combine one high-level alert on `redkey_cluster_healthy` with more specific alerts that explain which structural check is failing.

Example `PrometheusRule`:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: redkey-cluster-health
  namespace: redis-prod
spec:
  groups:
    - name: redkey.cluster.health
      rules:
        - alert: RedkeyClusterUnhealthy
          expr: redkey_cluster_healthy == 0
          for: 5m
          labels:
            severity: critical
          annotations:
            summary: Redkey cluster is unhealthy
            description: >-
              Cluster {{ $labels.namespace }}/{{ $labels.cluster }} failed at least one
              health check for more than 5 minutes.

        - alert: RedkeyClusterMembershipInconsistent
          expr: redkey_cluster_membership_ok == 0
          for: 5m
          labels:
            severity: critical
          annotations:
            summary: Redkey cluster membership is inconsistent
            description: >-
              Cluster {{ $labels.namespace }}/{{ $labels.cluster }} does not report a
              consistent connected membership view across all discovered nodes.

        - alert: RedkeyClusterSlotsNotCovered
          expr: redkey_cluster_slots_covered_ok == 0
          for: 2m
          labels:
            severity: critical
          annotations:
            summary: Redkey cluster slot coverage is incomplete
            description: >-
              Cluster {{ $labels.namespace }}/{{ $labels.cluster }} is not covering all
              16384 hash slots exactly once.

        - alert: RedkeyClusterUnbalanced
          expr: redkey_cluster_balanced_ok == 0
          for: 15m
          labels:
            severity: warning
          annotations:
            summary: Redkey cluster is unbalanced
            description: >-
              Cluster {{ $labels.namespace }}/{{ $labels.cluster }} has a primary slot
              distribution outside Robin's balance threshold.

        - alert: RedkeyClusterCheckErrors
          expr: redkey_cluster_check_errors > 0
          for: 5m
          labels:
            severity: warning
          annotations:
            summary: redis-cli cluster check reported errors
            description: >-
              Cluster {{ $labels.namespace }}/{{ $labels.cluster }} has
              {{ $value }} redis-cli cluster check errors.

        - alert: RedkeyClusterCheckCommandUnavailable
          expr: redkey_cluster_check_command_output_code < 0
          for: 10m
          labels:
            severity: warning
          annotations:
            summary: redis-cli cluster check could not be completed
            description: >-
              Robin could not complete redis-cli --cluster check for cluster
              {{ $labels.namespace }}/{{ $labels.cluster }} and exported conservative
              health values.
```

Recommended usage:

- Keep `RedkeyClusterUnhealthy` as the paging alert and treat the other alerts as diagnostic context.
- Use longer `for` windows on balance and warning signals to avoid noise during expected rebalances.
- Route alerts by `cluster`, `namespace`, and any custom `metricsLabels` you attach to the cluster.

## Dynamic Reconfiguration

All metrics settings are **hot-reloadable** — changes take effect without a Robin Pod restart. When you update `spec.robin.config.metrics`:

1. The Operator creates a new `RedkeyClusterConfig` with the updated settings.
2. Robin picks it up on the next reconciliation cycle.
3. The metrics collector applies the new configuration on its next collection tick.

This means you can:

- Add/remove `redisInfoKeys` to control which node-scoped `INFO` metrics are collected
- Change the `collectionIntervalSeconds` to adjust polling frequency
- Add/remove/update `metricsLabels` to modify labels on all metrics

If you set `redisInfoKeys: []`, Robin stops exporting node-scoped `INFO` and keyspace metrics, but cluster topology and health metrics continue to be refreshed.

> **Important**: When `metricsLabels` change, metrics with the old label set stop being updated and will become stale in Prometheus after the configured stale timeout (default 5 minutes). The new labels appear immediately on the next collection cycle.

For more details on the hot-reload mechanism, see [Dynamic Configuration](dynamic-configuration.md).
