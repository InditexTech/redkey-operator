<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Observability

This document describes the local observability stack provided for the Redkey project, including Prometheus, Grafana, and the pre-configured dashboards for the Redkey Operator and Redkey Robin.

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
| **Overview** | Leader election status, total reconciles, error rate %, active workers / max, reconcile panics |
| **Reconciliation Performance** | Reconciliation rate by result (stacked), duration percentiles (p50/p95/p99), terminal errors |
| **Work Queue** | Queue depth, adds rate, queue vs work duration (p95), retries rate, longest running processor, unfinished work |
| **Kubernetes API Client** | Request rate by method & status, request duration p95, request/response size p95, rate limiter wait p95, request retries |
| **Certificates & Transport** | Certificate reads vs errors, transport cache entries |

**Variables:**
- `datasource` — select the Prometheus data source
- `job` — filter by Prometheus job label (auto-filtered to `.*redkey.*`)

### Redkey Robin Dashboard

Located in `hack/observability/dashboards/redkey-robin.json`.

This dashboard monitors the Redis clusters managed by Robin:

| Section | Panels |
| ------- | ------ |
| **Cluster Overview** | Healthy, membership OK, slots covered, balanced, known nodes, cluster size, check errors, check warnings |
| **Redis Version & Node Topology** | Node info table (IP, role, state, slots), Redis version table per instance |
| **Memory** | RSS per node vs maxmemory, memory usage % (RSS/max), memory trend forecast (predict_linear 1h) |
| **Operations & Commands** | Commands/sec rate, connected clients, keyspace hit ratio, evicted & expired keys rate |
| **Network** | Network throughput (input/output rate), total network I/O (cumulative), AOF buffer |
| **CPU** | CPU usage rate (sys + user), CPU children rate, total CPU per node (stacked) |
| **Kubernetes API Client** | API request rate by method & status (`rest_client_requests_total`) |
| **Cluster Topology** | Primaries count, replicas count, disconnected nodes |

All metrics use the `redkey_` prefix (e.g., `redkey_cluster_healthy`, `redkey_connected_clients`, `redkey_used_memory_rss`). Additionally, Robin exposes `rest_client_requests_total` from the Kubernetes API client.

**Variables:**
- `datasource` — select the Prometheus data source
- `job` — filter by Prometheus job (discovered from `redkey_cluster_healthy` metric)
- `cluster` — filter by RedkeyCluster name

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
```

The ServiceMonitor selects the metrics Service by its labels:
- `control-plane: controller-manager`
- `app.kubernetes.io/name: redkey-operator`

This Service is created by the kustomize overlay (`config/default/metrics_service.yaml`) when deploying with `make deploy`.

> **Note:** `insecureSkipVerify: true` is acceptable for local development. For production, use cert-manager certificates (see `config/prometheus/monitor_tls_patch.yaml`).

### Robin Metrics

Robin is deployed as a standalone Deployment per RedkeyCluster. It exposes metrics over **HTTP on port 8080** without authentication. A PodMonitor scrapes Robin pods directly using `targetPort`:

```yaml
podMetricsEndpoints:
  - targetPort: 8080
    path: /metrics
    scheme: http
```

The PodMonitor selects pods with the label `app: redkey-robin` (applied by the operator when creating Robin Deployments).

Robin exposes metrics with the `redkey_` prefix:
- **Redis INFO metrics** — `redkey_connected_clients`, `redkey_used_memory_rss`, `redkey_maxmemory`, `redkey_total_commands_processed`, `redkey_used_cpu_sys`, `redkey_used_cpu_user`, `redkey_keyspace_hits`, `redkey_keyspace_misses`, `redkey_evicted_keys`, `redkey_expired_keys`, `redkey_total_net_input_bytes`, `redkey_total_net_output_bytes`, `redkey_mem_aof_buffer`
- **Cluster health metrics** — `redkey_cluster_healthy`, `redkey_cluster_membership_ok`, `redkey_cluster_slots_covered_ok`, `redkey_cluster_balanced_ok`, `redkey_cluster_check_errors`, `redkey_cluster_check_warnings`
- **Cluster node metrics** — `redkey_nodes_metrics` (per-node with role, slots, state labels)
- **Cluster info metrics** — `redkey_cluster_metrics` (cluster-level info as labels)

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
