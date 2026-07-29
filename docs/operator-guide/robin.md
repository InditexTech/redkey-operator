<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Redkey Cluster Robin

The Redkey Cluster CRD provides the field `spec.robin` to deploy the Redkey Cluster Robin, a faithful partner who assists the operator in the dangerous Gotham.
Robin is designed to help the Operator (Batman) in its duties, in particular:

- Provide Redkey Cluster prometheus metrics
- FUTURE WORK

The Operator deploys a Deployment and a ConfigMap for Robin given the configuration provided in `spec.robin` for each Redkey Cluster, if configured. The operator is responsible of reconcile any addition, update or delete in the `spec.robin` of a Redkey.

## How to deploy Robin

### Required fields

The Robin container image is specified in `spec.robin.image`:

```yaml
spec:
  robin:
    image: ghcr.io/inditextech/redkey-robin:0.1.0
```

The operator automatically exposes the metrics port (8080/TCP) on the Robin container.

### Resource requirements

Robin resource requests and limits are configured directly in `spec.robin.resources`:

```yaml
spec:
  robin:
    resources:
      requests:
        cpu: 100m
        memory: 128Mi
      limits:
        cpu: 500m
        memory: 256Mi
```

### Advanced template override

For advanced use cases (custom tolerations, node selectors, security contexts, volumes, etc.), the full PodTemplateSpec can be overridden via `spec.robin.template`. Fields set in the template take precedence over first-level fields like `resources`.

```yaml
spec:
  robin:
    resources:
      requests:
        cpu: 100m
        memory: 128Mi
    template:
      spec:
        tolerations:
          - key: "dedicated"
            operator: "Equal"
            value: "redis"
            effect: "NoSchedule"
        containers:
          - name: robin
            resources:  # overrides spec.robin.resources
              requests:
                cpu: 1
                memory: 512Mi
```

The template supports the following overrides:

| Level | Fields |
|-------|--------|
| Pod metadata | `labels`, `annotations` |
| Pod spec | `nodeSelector`, `tolerations`, `affinity`, `securityContext`, `imagePullSecrets`, `priorityClassName`, `topologySpreadConstraints`, `volumes` |
| Container (first) | `image`, `resources`, `env`, `envFrom`, `volumeMounts`, `securityContext` |

## How to configure Robin

Robin's operational settings are configured through the structured object `spec.robin.config`. Unlike `image`, `resources` and `template` (which are part of the Robin Deployment's pod template), the values under `spec.robin.config` are propagated by the Operator into the `RedkeyConfig` resource and **hot-reloaded by Robin at runtime without recreating the Pod** — see the [Dynamic Configuration](#dynamic-configuration) section below.

All subfields are optional. Any value omitted from the CR keeps Robin's built-in (or CLI-provided) startup default.

### Configuration fields

`spec.robin.config` accepts the following groups:

- `reconciler`: controls Robin's adaptive polling loop intervals.
  - `intervalSeconds` (int): idle interval between reconciliation cycles when there is no pending work.
  - `intervalOnErrorSeconds` (int): retry interval after a reconciliation error.
  - `intervalOnWaitSeconds` (int): interval used while waiting for readiness or convergence.
- `cluster`: controls how Robin connects to and operates on the Redis cluster.
  - `connectionMaxRetries` (int): maximum retries to connect to a Redis node.
  - `connectionBackOffSeconds` (int): back-off time in seconds between two consecutive connection attempts.
  - `clusterCommandTimeoutSeconds` (int): timeout in seconds for cluster commands.
  - `clusterMeetWaitSeconds` (int): wait time in seconds after a `CLUSTER MEET` before continuing.
  - `rebalanceTimeoutSeconds` (int): timeout in seconds for a rebalance operation.
- `metrics`: controls Prometheus metrics collection.
  - `collectionIntervalSeconds` (int): sleep time in seconds between two consecutive metrics polling iterations.
  - `redisInfoKeys` ([]string): Redis INFO keys requested from each Redis node and exported as Prometheus metrics.
  - `metricsLabels` (map[string]string): additional labels added to the exported Prometheus metrics.
- `profiling`: controls Go pprof endpoints (served on the metrics port). See the [Profiling Guide](profiling.md).
  - `enabled` (bool): activates pprof profiling endpoints at runtime. Defaults to `false`.

### Example

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: Redkey
...
spec:
  ...
  robin:
    ...
    config:
      reconciler:
        intervalSeconds: 30
        intervalOnErrorSeconds: 10
        intervalOnWaitSeconds: 10
      cluster:
        connectionMaxRetries: 10
        connectionBackOffSeconds: 10
        clusterCommandTimeoutSeconds: 24
        rebalanceTimeoutSeconds: 600
        clusterMeetWaitSeconds: 5
      metrics:
        collectionIntervalSeconds: 60
        metricsLabels:
          application: showpaas
          environment: des
        redisInfoKeys:
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
      profiling:
        enabled: false
```

## How to develop Robin

Please refer to [Redkey Robin](https://github.com/InditexTech/redkeyrobin/docs/developer-guide.md) section of the Operador Development Guide to know how to develop, build and deploy Robin for development and debugging purposes.

## Authentication

Robin authenticates to Redis using a password stored in a Kubernetes Secret. The secret name is configured declaratively in `spec.auth.secret` of the `Redkey` — no CLI flags or environment variables are required.

For full details on creating the Secret, configuring auth, password rotation, and troubleshooting, see the [Redis Authentication](authentication.md) guide.

## Dynamic Configuration

All Robin operational settings (`reconciler.intervalSeconds`, `reconciler.intervalOnErrorSeconds`, `reconciler.intervalOnWaitSeconds`, `metrics.collectionIntervalSeconds`, `metrics.redisInfoKeys`, `metrics.metricsLabels`, connection retries, and the auth secret reference) are applied **at runtime without a Pod restart**. Robin continuously polls `RedkeyConfig` resources and updates its internal state when new configurations are detected. If a reconciler interval is omitted from the CR, Robin keeps its startup value; for `intervalOnWaitSeconds` that startup default is 10 seconds unless overridden via CLI. Changing `metricsLabels` values is applied on the next metrics cycle; adding or removing label keys resets only Robin's RedKey metrics registry so Prometheus can ingest the new label schema.

For a complete explanation of the hot-reload mechanism, propagation timing, and examples, see the [Dynamic Configuration](dynamic-configuration.md) guide.
