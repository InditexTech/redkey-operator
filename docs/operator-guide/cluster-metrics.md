<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Redis Cluster Metrics

This guide explains how to configure, expose, and consume Redis cluster metrics collected by Redkey Robin.

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
  - [String Values](#string-values)
  - [Compound Values](#compound-values)
- [Labels](#labels)
  - [Base Labels](#base-labels)
  - [Custom Labels](#custom-labels)
- [Consuming Metrics](#consuming-metrics)
  - [Prometheus Endpoint](#prometheus-endpoint)
  - [ServiceMonitor](#servicemonitor)
  - [PromQL Examples](#promql-examples)
- [Dynamic Reconfiguration](#dynamic-reconfiguration)

---

## Overview

Redkey Robin collects Redis metrics from every node of a RedkeyCluster by periodically executing `INFO ALL`, `CLUSTER INFO`, and `CLUSTER NODES` commands. The collected data is exposed as Prometheus gauges on Robin's HTTP metrics endpoint (default port 8080, path `/metrics`).

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

Only keys listed here will be exported as Prometheus metrics. Keys are matched after normalizing dashes to underscores (e.g., `used-memory` → `used_memory`). If this list is empty, no INFO metrics are collected.

**Default metrics collected by Robin (if `redisInfoKeys` is empty):**

These keys are collected by default if `redisInfoKeys` is not set or is empty. You can override this default set by specifying your own list. This list is set by Robin to ensure that essential metrics are always collected.

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

### Redis INFO Metrics

For each key in `redisInfoKeys`, Robin exposes a Prometheus gauge named `redis_<key>`. Numeric values are set directly; string values are handled as described in [String Values](#string-values).

**Example:** With `redisInfoKeys: [connected_clients, used_memory]`, you get:

```
redis_connected_clients{cluster="my-cluster",namespace="redis-prod",instanceId="my-cluster-0",environment="production",team="platform"} 42
redis_used_memory{cluster="my-cluster",namespace="redis-prod",instanceId="my-cluster-0",environment="production",team="platform"} 1048576
```

### Keyspace Metrics

Keyspace entries (e.g., `db0:keys=1000,expires=50,avg_ttl=2000`) are automatically split into individual gauges with an additional `database` label:

```
redis_keyspace_keys{cluster="my-cluster",namespace="redis-prod",instanceId="my-cluster-0",database="db0",...} 1000
redis_keyspace_expires{cluster="my-cluster",namespace="redis-prod",instanceId="my-cluster-0",database="db0",...} 50
redis_keyspace_avg_ttl{cluster="my-cluster",namespace="redis-prod",instanceId="my-cluster-0",database="db0",...} 2000
```

> **Note**: Keyspace metrics are always collected if any `redisInfoKeys` are configured — no need to explicitly list `db0` etc.

### Cluster INFO Metrics

Robin exposes all fields from `CLUSTER INFO` as labels on a single metric:

```
redkey_cluster_metrics{cluster="my-cluster",namespace="redis-prod",cluster_state="ok",cluster_slots_assigned="16384",cluster_slots_ok="16384",cluster_known_nodes="6",...} <current_timestamp>
```

This metric uses `SetToCurrentTime()` so its value represents the last successful collection timestamp.

### Cluster Nodes Metrics

Each node from `CLUSTER NODES` output is exposed as:

```
redis_nodes_metrics{cluster="my-cluster",namespace="redis-prod",nodeId="abc123",nodeIp="10.0.0.1",role="master",slots="0-5460",primaryId="-",state="connected",...} <current_timestamp>
```

### String Values

When a Redis INFO key has a non-numeric value (e.g., `redis_version: "7.2.4"`), Robin exposes it as a gauge with value `-1` and the string as an additional label:

```
redis_redis_version{cluster="my-cluster",namespace="redis-prod",instanceId="my-cluster-0",redis_version="7.2.4",...} -1
```

This pattern allows you to use PromQL label matching to detect version drift or track changes across nodes.

### Compound Values

When a value contains `key=value` pairs separated by commas (e.g., `cmdstat_set:calls=100,usec=500,usec_per_call=5.0`), Robin splits them into individual sub-metrics:

```
redis_cmdstat_set_calls{...} 100
redis_cmdstat_set_usec{...} 500
redis_cmdstat_set_usec_per_call{...} 5.0
```

## Labels

### Base Labels

Every metric includes the following labels automatically:

| Label | Source | Example |
| ----- | ------ | ------- |
| `cluster` | RedkeyCluster name | `my-cluster` |
| `namespace` | RedkeyCluster namespace | `redis-prod` |
| `instanceId` | Redis node name | `my-cluster-0` |

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
redis_used_memory{cluster="my-cluster"}
```

**Hit rate:**

```promql
redis_keyspace_hits / (redis_keyspace_hits + redis_keyspace_misses)
```

**Connected clients across the cluster:**

```promql
sum(redis_connected_clients{cluster="my-cluster"}) by (cluster)
```

**Operations per second (rate):**

```promql
rate(redis_total_commands_processed{cluster="my-cluster"}[5m])
```

**Memory fragmentation ratio per node:**

```promql
redis_used_memory_rss / redis_used_memory
```

**Detect version inconsistencies:**

```promql
count(count by (redis_version) (redis_redis_version{cluster="my-cluster"})) > 1
```

**Keyspace keys per database:**

```promql
redis_keyspace_keys{cluster="my-cluster"}
```

**Filter by custom label:**

```promql
redis_connected_clients{environment="production", team="platform"}
```

## Dynamic Reconfiguration

All metrics settings are **hot-reloadable** — changes take effect without a Robin Pod restart. When you update `spec.robin.config.metrics`:

1. The Operator creates a new `RedkeyClusterConfig` with the updated settings.
2. Robin picks it up on the next reconciliation cycle.
3. The metrics collector applies the new configuration on its next collection tick.

This means you can:

- Add/remove `redisInfoKeys` to control which metrics are collected
- Change the `collectionIntervalSeconds` to adjust polling frequency
- Add/remove/update `metricsLabels` to modify labels on all metrics

> **Important**: When `metricsLabels` change, metrics with the old label set stop being updated and will become stale in Prometheus after the configured stale timeout (default 5 minutes). The new labels appear immediately on the next collection cycle.

For more details on the hot-reload mechanism, see [Dynamic Configuration](dynamic-configuration.md).
