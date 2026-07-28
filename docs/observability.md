<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Observability

This document describes the local observability stack provided for the Redkey project, including Prometheus, Grafana, and the pre-configured dashboards for the Redkey Operator, Redkey Cluster (Redis), and Redkey Robin (process health).

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Components](#components)
- [Installation](#installation)
  - [Prerequisites](#prerequisites)
  - [Deploy the Observability Stack](#deploy-the-observability-stack)
  - [Access Grafana](#access-grafana)
  - [Access Prometheus](#access-prometheus)
- [Dashboards](#dashboards)
  - [Redkey Operator Dashboard](#redkey-operator-dashboard)
  - [Redkey Robin Dashboard](#redkey-robin-dashboard)
- [Metrics Scraping Configuration](#metrics-scraping-configuration)
  - [Operator Metrics](#operator-metrics)
  - [Robin Metrics](#robin-metrics)
- [Uninstallation](#uninstallation)
- [Customization](#customization)
- [Troubleshooting](#troubleshooting)

---

## Overview

The Redkey project includes a fully integrated local observability stack designed for the Kind-based development environment. It uses the industry-standard [kube-prometheus-stack](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack) Helm chart to deploy:

- **Prometheus** — time-series database and metrics scraper
- **Grafana** — visualization and dashboarding
- **Prometheus Operator** — manages ServiceMonitors/PodMonitors for dynamic scrape target discovery

All configuration is centralized in the operator repository under `hack/observability/`.

## Architecture

```
┌──────────────────────────────────────────────────────────────────────┐
│  Kind Cluster                                                        │
│                                                                      │
│  ┌─────────────────────────────────────────────────────────────────┐ │
│  │  monitoring namespace                                           │ │
│  │                                                                 │ │
│  │  ┌──────────────┐    ┌──────────────┐    ┌──────────────────┐  │ │
│  │  │  Prometheus  │◄───│  Prometheus  │    │     Grafana      │  │ │
│  │  │  Server      │    │  Operator    │    │  (port 30300)    │  │ │
│  │  └──────┬───────┘    └──────────────┘    └────────┬─────────┘  │ │
│  │         │                                         │             │ │
│  │         │  scrapes /metrics                       │ queries     │ │
│  │         │                                         │             │ │
│  └─────────┼─────────────────────────────────────────┼─────────────┘ │
│            │                                         │               │
│  ┌─────────┼──────────────────┐   ┌─────────────────┼─────────────┐ │
│  │         ▼                  │   │                 ▼             │ │
│  │  Redkey Operator           │   │  Redkey Cluster Pods          │ │
│  │  (HTTPS :8443)             │   │  ┌─────────┐  ┌─────────┐   │ │
│  │  ServiceMonitor            │   │  │  Redis  │  │  Robin   │   │ │
│  │                            │   │  │         │  │ (HTTP    │   │ │
│  │  redkey-operator namespace │   │  │         │  │  :8080)  │   │ │
│  └────────────────────────────┘   │  └─────────┘  └─────────┘   │ │
│                                   │  PodMonitor                   │ │
│                                   │                               │ │
│                                   │  <any namespace>              │ │
│                                   └───────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────────┘
```

## Components

| Component | Description | Location |
| --------- | ----------- | -------- |
| **kube-prometheus-stack** | Helm chart that bundles Prometheus, Grafana, and the Prometheus Operator | Deployed via `hack/observability/install-observability.sh` |
| **ServiceMonitor (operator)** | Tells Prometheus how to scrape the operator's HTTPS metrics endpoint | `hack/observability/servicemonitors.yaml` |
| **PodMonitor (robin)** | Tells Prometheus how to scrape Robin sidecar pods on HTTP port 8080 | `hack/observability/servicemonitors.yaml` |
| **Grafana Dashboards** | Pre-built JSON dashboards loaded via a ConfigMap sidecar | `hack/observability/dashboards/` |
| **Helm values** | Customized settings for the local dev stack (low resources, NodePort access) | `hack/observability/values-prometheus-stack.yaml` |

## Installation

### Prerequisites

- A running Kind cluster (created via `make setup-kind`)
- `helm` CLI installed ([install guide](https://helm.sh/docs/intro/install/))
- `kubectl` configured to talk to the Kind cluster

### Deploy the Observability Stack

From the operator repository root:

```bash
make observability-install
```

This single command:
1. Adds the `prometheus-community` Helm repository
2. Creates the `monitoring` namespace
3. Installs (or upgrades) the `kube-prometheus-stack` Helm release
4. Applies ServiceMonitor and PodMonitor resources for the operator and Robin
5. Creates a ConfigMap with the Grafana dashboards

You can customize the namespace:

```bash
make observability-install OBSERVABILITY_NAMESPACE=my-monitoring
```

### Access Grafana

Grafana is exposed via NodePort `30300`. In a Kind cluster:

```bash
# Direct access (Kind maps NodePort to localhost)
open http://localhost:30300

# Alternative: port-forward
kubectl -n monitoring port-forward svc/prometheus-grafana 3000:80
open http://localhost:3000
```

**Credentials:** `admin` / `redkey`

### Access Prometheus

```bash
kubectl -n monitoring port-forward svc/prometheus-kube-prometheus-prometheus 9090:9090
open http://localhost:9090
```

## Dashboards

### Redkey Operator Dashboard

Located in `hack/observability/dashboards/redkey-operator.json`.

This dashboard monitors the health and performance of the operator's controller-runtime reconciliation loop:

| Section | Panels |
| ------- | ------ |
| **Managed Redkey Fleet** | Total Redkeys, Ready, Configuring, Error (stats); phase and config-phase distribution; operation duration percentiles, operations by result, and operations in progress — from operator-exposed fleet metrics (`redkey_phase`, `redkey_config_phase`, `redkey_operation_*`), see [Managed Redkey Fleet metrics](#managed-redkey-fleet-metrics) |
| **Overview** | Operator version (`redkey_operator_build_info`), uptime, leader election status, total reconciles, error rate %, active workers / max, reconcile panics |
| **Reconciliation Performance** | Count by result (`increase`), rate by result (stacked), duration percentiles (p50/p95/p99), reconciliations by controller, average reconcile duration (`rate(sum)/rate(count)`), reconcile errors terminal vs non-terminal (`increase` by controller) |
| **Work Queue** | Queue depth, adds rate, queue vs work duration (p95), retries rate, longest running processor, unfinished work |
| **Kubernetes API Client** | Requests by method & status (`increase()` per-interval counts), client errors (4xx / 5xx), request rate by method |
| **Process & Go Runtime** | Process memory (RSS vs virtual), Go heap (in-use/idle/stack), process CPU usage (cores), goroutines, GC pause quantiles, open file descriptors vs max, Go allocation rate, GC cycles rate, live heap objects, OS threads, next-GC target vs heap in-use |
| **Kubernetes Container (cAdvisor + kube-state-metrics)** | CPU usage vs requests/limits, CPU throttling ratio, memory working set vs requests/limits, memory usage %, container restarts, deployment replicas (desired vs available), last terminated reason (OOMKilled, etc.), pod ready |

**Variables:**
- `datasource` — select the Prometheus data source
- `namespace` / `pod` — primary filters; scope every panel to the operator pod(s)
- `job` — hidden **constant** pinned to `redkey-operator`. It anchors the `controller_runtime_*`, `workqueue_*` and `go_*` panels to the operator's series so they do not accidentally include series from other controller-runtime operators (e.g. the prometheus-operator, which exposes the same metric names). The value is stamped onto every sample by the ServiceMonitor via `relabelings` (see [Metrics Scraping Configuration](#metrics-scraping-configuration))

> **Kubernetes API Client** shows only `rest_client_requests_total`. controller-runtime v0.21 registers just that client-go metric — the request-duration, request/response-size, rate-limiter and retries histograms are **not** wired up, so those panels (and the `certwatcher` / transport-cache panels) were removed as they can never produce data with this version.
>
> The **Process & Go Runtime** panels come from the operator's own `/metrics` endpoint (`process_*`, `go_*`) and require no extra components. The **Kubernetes Container** panels depend on the kubelet/cAdvisor scrape (enabled by default in `kube-prometheus-stack`) and on kube-state-metrics (`kubeStateMetrics.enabled: true` in `values-prometheus-stack.yaml`). Node/host-level metrics are not available because `nodeExporter` is disabled in the dev stack.

### Redkey Cluster Dashboard

Located in `hack/observability/dashboards/redkey-instance.json`.

This dashboard monitors the Redis clusters managed by Robin — cluster health, topology, and per-node performance:

| Section | Panels |
| ------- | ------ |
| **Cluster Overview** | Healthy, membership OK, slots covered, balanced, known nodes, cluster size, check errors, check warnings |
| **Cluster Topology** | Primaries count, replicas count, disconnected nodes |
| **Redis Version & Node Topology** | Node info table (IP, role, state, slots), Redis version table per instance |
| **Keyspace** | Keys per node, keys with TTL (expires), average TTL (`redkey_keyspace_keys` / `_expires` / `_avg_ttl`, per `database`) |
| **Operations & Commands** | Row 1: commands count (`increase`), commands/sec rate, connected clients. Row 2: keyspace hits / evicted / expired counts (`increase`). Row 3: keyspace hit ratio, evicted keys rate, expired keys rate |
| **Persistence (AOF)** | AOF size base vs current per node (`redkey_aof_base_size`, `redkey_aof_current_size`) |
| **CPU** | CPU usage rate (sys + user), CPU children rate, total CPU per node (stacked) |
| **Memory** | RSS per node vs maxmemory, memory usage % (RSS/max), memory trend forecast (predict_linear 1h) |
| **Network** | Network throughput (input/output rate), total network I/O (cumulative), AOF buffer |
| **Kubernetes Container (Redis pods)** | CPU usage vs requests/limits, CPU throttling ratio, memory working set vs requests/limits, memory usage %, container restarts, StatefulSet replicas (desired vs ready) — filtered to `container="redis"`, pods `<cluster>-N` |

All metrics use the `redkey_` prefix (e.g., `redkey_cluster_healthy`, `redkey_connected_clients`, `redkey_used_memory_rss`).

**Variables:**
- `datasource` — select the Prometheus data source
- `job` — filter by Prometheus job (discovered from the node-level `redkey_used_memory_rss` metric, present in **both** cluster and standalone modes)
- `cluster` — filter by Redkey name

> The `job`/`cluster` variables are derived from `redkey_used_memory_rss` (node-level) rather than `redkey_cluster_healthy` (cluster-level). Robin skips cluster-level metrics in **standalone** mode, so a standalone cluster would otherwise never appear in the `cluster` list. Cluster-level panels (health, topology) are simply empty for standalone clusters, which is expected.

### Redkey Robin Dashboard

Located in `hack/observability/dashboards/redkey-robin.json`.

This dashboard monitors Robin itself as a process — its runtime health, resource usage, and Kubernetes API interactions:

| Section | Panels |
| ------- | ------ |
| **Overview** | Fleet summary: Robin instances (count) and Go version (per-version count), plus *goroutines per instance* and *open FDs per instance* time series for quick outlier/leak spotting |
| **Kubernetes API Client** | Per-interval counts using `increase()`: requests by method/status and errors (4xx/5xx) — the exact number of operations/errors per time bucket |
| **Memory** | Process RSS vs virtual memory, Go heap (in-use, idle, stack), allocation rate, GC cycles rate |
| **CPU & Scheduling** | Process CPU usage rate, goroutines over time, GC pause duration quantiles, live objects & heap objects |
| **File Descriptors** | Open FDs vs limit over time, FD utilisation gauge |
| **Kubernetes Container (cAdvisor + kube-state-metrics)** | CPU usage vs requests/limits, CPU throttling ratio, memory working set vs requests/limits, memory usage %, container restarts, deployment replicas (desired vs available) — filtered to `container="robin"` |

Metrics come from Go runtime collectors (`go_*`), process collectors (`process_*`), the Kubernetes client-go adapter (`rest_client_requests_total`), and — for the Kubernetes Container section — the kubelet/cAdvisor scrape and kube-state-metrics (`container_*`, `kube_pod_*`, `kube_deployment_*`).

**Variables:**
- `datasource` — select the Prometheus data source
- `namespace` / `pod` — primary filters; select one or several Robin instances
- `job` — hidden **constant** pinned to `redkey-robin`. Robin exposes only generic metrics (`process_*`, `go_*`, `rest_client_*`) whose names collide with the operator and the prometheus-operator, so `job` anchors the panels to Robin's series. The value is stamped onto every sample by the PodMonitor via `relabelings` (see [Metrics Scraping Configuration](#metrics-scraping-configuration)).

> The previous `cluster` variable was removed: the active scrape is the Robin **PodMonitor**, which only attaches `pod`/`namespace` target labels (not `cluster`), so that filter never resolved any values. Use `namespace` + `pod` instead.

## Metrics Scraping Configuration

### Operator Metrics

The operator exposes metrics over **HTTPS on port 8443** with bearer token authentication (Kubernetes TokenReview). The ServiceMonitor is configured with:

```yaml
endpoints:
  - port: https
    path: /metrics
    scheme: https
    bearerTokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
    tlsConfig:
      insecureSkipVerify: true
    relabelings:
      - targetLabel: job
        replacement: redkey-operator
```

The `relabelings` block pins a stable, human-friendly `job` label (`redkey-operator`) instead of the default Service name, so dashboards and queries can filter on `job="redkey-operator"`.

The ServiceMonitor selects the metrics Service by its labels:
- `control-plane: controller-manager`
- `app.kubernetes.io/name: redkey-operator`

This Service is created by the kustomize overlay (`config/default/metrics_service.yaml`) when deploying with `make deploy`.

> **Note:** `insecureSkipVerify: true` is acceptable for local development. For production, use cert-manager certificates (see `config/prometheus/monitor_tls_patch.yaml`).

### Robin Metrics

Robin is deployed as a standalone Deployment per Redkey. It exposes metrics over **HTTP on port 8080** without authentication. A PodMonitor scrapes Robin pods directly using `targetPort`:

```yaml
podMetricsEndpoints:
  - targetPort: 8080
    path: /metrics
    scheme: http
    relabelings:
      - targetLabel: job
        replacement: redkey-robin
```

The `relabelings` block pins a stable, human-friendly `job` label (`redkey-robin`) instead of the default pod name.

The PodMonitor selects pods with the label `redkey.inditex.dev/component: robin` (applied by the operator when creating Robin Deployments).

Robin exposes metrics with the `redkey_` prefix:
- **Redis INFO metrics** — `redkey_connected_clients`, `redkey_used_memory_rss`, `redkey_maxmemory`, `redkey_total_commands_processed`, `redkey_used_cpu_sys`, `redkey_used_cpu_user`, `redkey_keyspace_hits`, `redkey_keyspace_misses`, `redkey_evicted_keys`, `redkey_expired_keys`, `redkey_total_net_input_bytes`, `redkey_total_net_output_bytes`, `redkey_mem_aof_buffer`
- **Cluster health metrics** — `redkey_cluster_healthy`, `redkey_cluster_membership_ok`, `redkey_cluster_slots_covered_ok`, `redkey_cluster_balanced_ok`, `redkey_cluster_check_errors`, `redkey_cluster_check_warnings`
- **Cluster node metrics** — `redkey_nodes_metrics` (per-node with role, slots, state labels)
- **Cluster info metrics** — `redkey_cluster_metrics` (cluster-level info as labels)

### Managed Redkey Fleet metrics

The **control-plane state** of the Redkey fleet is exposed by the **operator itself**, on the same
`/metrics` endpoint already scraped for the controller-runtime metrics. They are updated from the
reconcile loop (event-driven, plus the periodic resync as a safety net): a Redkey's series are set
from its freshly aggregated status, removed when the reconciler observes the deletion, and the
in-memory registry starts clean on every operator restart.

This is deliberately **not** done via kube-state-metrics CustomResourceState. Although that would
avoid operator code, it requires configuring the *central* kube-state-metrics — which is not
possible in locked-down production clusters where the observability stack is managed by another
team. Exposing the metrics from the operator only needs the operator's **ServiceMonitor** (which
teams can create in their own namespace), so it works identically in local and production
environments.

Scope is deliberately narrow — only state the operator owns:
- **Lifecycle phase** (`redkey_phase`, `redkey_config_phase`) — control-plane state.
- **Operation timing** (`redkey_operation_duration_seconds`, `redkey_operation_total`) — how long
  scaling/upgrade/rebalance operations take and how they end, which nothing else measures.

Data-plane health (cluster healthy, slots covered, balance, memory, ...) is **not** mirrored here:
it is owned and published by Robin (`redkey_cluster_*`, shown in the Redkey Cluster dashboard).

| Metric | Type | Source / meaning | Labels |
| ------ | ---- | ---------------- | ------ |
| `redkey_phase` | gauge (state set) | `Redkey.status.phase` — 1 on the active phase | `namespace`, `name`, `phase` (`Ready`/`Configuring`/`Error`) |
| `redkey_config_phase` | gauge (state set) | active `RedkeyConfig.status.configPhase` — 1 on the active phase | `namespace`, `name`, `config_phase` (`Pending`/`InProgress`/`Superseded`/`Applied`) |
| `redkey_operation_duration_seconds` | histogram | duration from start until the cluster settles back to Ready/Error | `operation` |
| `redkey_operation_total` | counter | completed operations | `operation`, `result` (`success`/`error`/`superseded`) |
| `redkey_operation_in_progress` | gauge | the operation a Redkey is running **right now** (1 while in flight; removed on settle) | `namespace`, `name`, `operation` |

Because `redkey_phase`/`redkey_config_phase` carry the Redkey's own `namespace`/`name` labels, the
operator's ServiceMonitor sets `honorLabels: true` so those are preserved instead of being
overwritten by the operator pod's target labels.

Example queries:
- Redkeys currently in error: `redkey_phase{phase="Error"} == 1`
- Fleet phase distribution: `sum by (phase) (redkey_phase)`
- Total managed Redkeys: `count(redkey_phase{phase="Ready"})`
- Upgrade duration p95: `histogram_quantile(0.95, sum by (le) (rate(redkey_operation_duration_seconds_bucket{operation="Upgrading"}[$__rate_interval])))`
- Failed operations: `sum by (operation) (increase(redkey_operation_total{result="error"}[$__rate_interval]))`
- Operations running right now: `redkey_operation_in_progress`

> `redkey_operation_*` (duration/total) are recorded only when an operation **completes**, so an
> in-flight operation appears in `redkey_operation_in_progress` (live) but not yet in the
> counters/histogram. The completion metrics are also best-effort for very short operations: the
> operator samples `status` on each reconcile, so an operation that starts and finishes between two
> reconciles may not be timed. For precise, transition-accurate operation timing the natural owner
> is Robin (which drives every transition); the operator's view is a convenient approximation.


> These metrics arrive on the **kube-state-metrics** job (not Robin's), and require the Redkey CRDs
> to be installed so their GroupVersionKind exists. `customResourceState.only` is **not** set, so
> the standard `kube_*` metrics (used by the Kubernetes Container panels) keep flowing.

## Uninstallation

To completely remove the observability stack and leave the cluster clean:

```bash
make observability-uninstall
```

This removes:
1. The Grafana dashboards ConfigMap
2. All ServiceMonitors and PodMonitors
3. The Helm release (Prometheus, Grafana, Prometheus Operator)
4. Prometheus Operator CRDs
5. The `monitoring` namespace

## Customization

### Changing Grafana password

Edit `hack/observability/values-prometheus-stack.yaml`:

```yaml
grafana:
  adminPassword: your-new-password
```

Then re-run `make observability-install`.

### Adding more dashboards

1. Place your JSON dashboard file in `hack/observability/dashboards/`
2. Re-run `make observability-install` — the install script packages all JSON files in that directory into the ConfigMap

### Adjusting scrape intervals

Edit `hack/observability/servicemonitors.yaml` and change the `interval` field on any endpoint.

### Prometheus data retention

Edit `hack/observability/values-prometheus-stack.yaml`:

```yaml
prometheus:
  prometheusSpec:
    retention: 72h  # default is 24h for dev
```

### Using in a non-Kind cluster

The stack works in any Kubernetes cluster. The only Kind-specific setting is the NodePort `30300` for Grafana. For other environments, change the Grafana service type:

```yaml
grafana:
  service:
    type: ClusterIP  # or LoadBalancer
```

## Troubleshooting

### Prometheus is not scraping targets

1. Check that ServiceMonitors are created:
   ```bash
   kubectl get servicemonitors,podmonitors -n monitoring
   ```

2. Verify targets in the Prometheus UI:
   ```bash
   kubectl -n monitoring port-forward svc/prometheus-kube-prometheus-prometheus 9090:9090
   ```
   Then open http://localhost:9090/targets

3. Ensure the operator/Robin pods have the expected labels:
   ```bash
   kubectl get pods --show-labels -A | grep redkey
   ```

### Grafana dashboards not showing

1. Check the dashboards ConfigMap exists:
   ```bash
   kubectl get cm redkey-grafana-dashboards -n monitoring
   ```

2. Restart the Grafana pod to force the sidecar to reload:
   ```bash
   kubectl -n monitoring rollout restart deployment prometheus-grafana
   ```

### No data in dashboards

1. Confirm that the operator or Robin is running and exposing metrics
2. Check the Prometheus targets page for scrape errors
3. Verify the `job` variable in the dashboard matches the actual Prometheus job name
4. For the operator, ensure the RBAC allows Prometheus to scrape (the `metrics-reader` ClusterRole must exist)
5. For Robin, verify its metrics collector is working:
   ```bash
   kubectl logs <robin-pod> | grep "metrics collection"
   ```
   If you see `"Failed to list pods for node discovery"`, the Robin ServiceAccount is missing `pods` list permission. This was fixed in the operator's `DesiredRobinRules()` function. Redeploy the operator to update the Role.
