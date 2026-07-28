<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Metrics

This document describes the metrics exposure mechanism implemented in the Redkey Operator, how to enable and disable it, the metrics currently exposed, and the custom business metrics that could be added.

## Table of Contents

- [Overview](#overview)
- [Enabling and Disabling Metrics](#enabling-and-disabling-metrics)
  - [Command-Line Flags](#command-line-flags)
  - [Kustomize Configuration](#kustomize-configuration)
  - [Helm Chart](#helm-chart)
  - [RBAC for Metrics](#rbac-for-metrics)
  - [NetworkPolicy](#networkpolicy)
  - [Prometheus ServiceMonitor](#prometheus-servicemonitor)
- [Querying Metrics](#querying-metrics)
  - [Via Port-Forward](#via-port-forward)
  - [From a Pod Inside the Cluster](#from-a-pod-inside-the-cluster)
  - [Useful Filter Commands](#useful-filter-commands)
- [Standard Metrics (controller-runtime)](#standard-metrics-controller-runtime)
  - [Controller Reconciliation Metrics](#controller-reconciliation-metrics-controller_runtime_)
  - [Work Queue Metrics](#work-queue-metrics-workqueue_)
  - [Kubernetes API Client Metrics](#kubernetes-api-client-metrics-rest_client_)
  - [What to Monitor in Production](#what-to-monitor-in-production)

---

## Overview

The Redkey Operator controller-manager exposes a Prometheus-compatible `/metrics` endpoint powered by [controller-runtime's metrics server](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/metrics/server). By default, this endpoint serves over **plain HTTP on port 8080** with no authentication, so it works out of the box with Prometheus setups that scrape by pod annotations. Serving over **HTTPS on port 8443** with authentication and authorization (enforced via Kubernetes TokenReview and SubjectAccessReview APIs) is available as an explicit opt-in.

The metrics endpoint publishes two categories of metrics:

1. **Standard controller-runtime metrics** — operational health of the reconciler, work queues, and the Kubernetes API client.
2. **Custom business metrics** (proposed) — domain-specific signals about Redkey lifecycle, configuration changes, and readiness latency.

## Enabling and Disabling Metrics

### Command-Line Flags

The controller-manager binary accepts the following flags to control metrics behavior:

| Flag | Default | Description |
| ---- | ------- | ----------- |
| `--metrics-bind-address` | `:8080` | Address the metrics endpoint binds to. Use `:8080` for HTTP (default) or `:8443` for HTTPS. Set to `0` to disable. |
| `--metrics-secure` | `false` | Serve metrics over HTTPS with authentication. Set to `true` to opt into HTTPS. |
| `--metrics-cert-path` | `""` | Directory containing custom TLS certificates for the metrics server. |
| `--metrics-cert-name` | `tls.crt` | Certificate file name within `--metrics-cert-path`. |
| `--metrics-cert-key` | `tls.key` | Key file name within `--metrics-cert-path`. |

With the defaults (`--metrics-secure=false`), the endpoint is served over plain HTTP without authentication. When `--metrics-secure=true`, the server uses controller-runtime's `filters.WithAuthenticationAndAuthorization` filter, which requires clients to present a valid bearer token with permission to `GET /metrics`.

When serving over HTTPS and no custom certificate is provided via `--metrics-cert-path`, controller-runtime automatically generates self-signed certificates. This is suitable for development but **not recommended for production**.

### Kustomize Configuration

The default deployment exposes metrics via the kustomize patch in `config/default/manager_metrics_patch.yaml`:

```yaml
- op: add
  path: /spec/template/spec/containers/0/args/0
  value: --metrics-bind-address=:8080
```

To **disable metrics**, remove this patch from `config/default/kustomization.yaml` or set the value to `--metrics-bind-address=0`.

To **serve over HTTPS with authentication**, set the value to `--metrics-bind-address=:8443` and add a second arg `--metrics-secure=true`. To **use cert-manager certificates**, uncomment the `[METRICS-WITH-CERTS]` section in `config/default/kustomization.yaml`, which adds the `cert_metrics_manager_patch.yaml` patch to mount certificates from a cert-manager-managed Secret.

### Helm Chart

When deploying via the Helm chart (`charts/redkey-operator`), metrics are enabled by default over plain HTTP on port 8080, and the operator pod is annotated for Prometheus annotation-based scraping. The `metrics` block in `values.yaml` controls the behavior:

```yaml
metrics:
  enabled: true      # expose the /metrics endpoint
  port: 8080         # bind/expose port
  secure: false      # set true for HTTPS + authentication
  service:
    enabled: true    # create a Service pointing at the metrics port
  serviceMonitor:
    enabled: false   # create a Prometheus Operator ServiceMonitor
    labels: {}
    interval: 30s
  annotations:       # annotation-based scraping (no Prometheus Operator)
    enabled: true    # add prometheus.io/* scrape annotations to the pod
    path: /metrics   # port and scheme are derived from port/secure above
```

To opt into HTTPS with authentication, set `metrics.secure: true` (and typically `metrics.port: 8443`); the Service and ServiceMonitor switch to the `https` scheme with a bearer token automatically.

### RBAC for Metrics

The following RBAC resources are required **only when serving metrics over HTTPS with authentication** (`--metrics-secure=true`):

| Resource | Kind | Purpose |
| -------- | ---- | ------- |
| `metrics-auth-role` | ClusterRole | Grants permission to create `tokenreviews` and `subjectaccessreviews` (required by the metrics server to validate client tokens). |
| `metrics-auth-rolebinding` | ClusterRoleBinding | Binds `metrics-auth-role` to the controller-manager ServiceAccount. |
| `metrics-reader` | ClusterRole | Grants `GET` access to the `/metrics` non-resource URL. Bind this to any ServiceAccount or user that needs to scrape metrics. |

These are included by default in `config/rbac/kustomization.yaml`. With the default plain-HTTP endpoint they are not needed by the scraper; they only matter once `--metrics-secure=true` is enabled.

### NetworkPolicy

An optional NetworkPolicy (`config/network-policy/allow-metrics-traffic.yaml`) restricts access to the metrics port (`8080`) to pods running in namespaces labeled with `metrics: enabled`. Because the default endpoint is unauthenticated, enabling this policy is **recommended in production**. To enable it, uncomment the `../network-policy` line in `config/default/kustomization.yaml`.

### Prometheus ServiceMonitor

A ServiceMonitor for Prometheus Operator is provided in `config/prometheus/monitor.yaml`. It targets the metrics service on port `http` (8080) over plain HTTP by default. When serving metrics over HTTPS, update the endpoint to `scheme: https`, add `bearerTokenFile` and the appropriate `tlsConfig`.

## Querying Metrics

### Via Port-Forward

From your local machine, forward the metrics service port and query it with `curl`. With the default plain-HTTP endpoint no token is required:

```shell
# Forward the metrics service port
kubectl -n redkey-operator port-forward \
  svc/redkey-operator-controller-manager-metrics-service 8080:8080
```

In a separate terminal:

```shell
# Query all metrics
curl http://127.0.0.1:8080/metrics

# Quick-check reconcile metrics
curl -s http://127.0.0.1:8080/metrics \
  | grep controller_runtime_reconcile_total
```

> **Note:** When serving over HTTPS (`--metrics-secure=true`), forward `8443:8443` and add a bearer token: grant the `metrics-reader` ClusterRole to the scraping ServiceAccount, then
> `TOKEN=$(kubectl -n redkey-operator create token redkey-operator-controller-manager)` and
> `curl -k -H "Authorization: Bearer $TOKEN" https://127.0.0.1:8443/metrics`.

### From a Pod Inside the Cluster

Launch a temporary pod in the operator namespace and curl the plain-HTTP endpoint:

```shell
kubectl -n redkey-operator run curl-metrics \
  --restart=Never \
  --image=curlimages/curl:latest \
  -- curl -s http://redkey-operator-controller-manager-metrics-service.redkey-operator.svc.cluster.local:8080/metrics
```

Check the output and clean up:

```shell
kubectl -n redkey-operator logs -f pod/curl-metrics
kubectl -n redkey-operator delete pod curl-metrics
```

> **Note:** If the `allow-metrics-traffic` NetworkPolicy is active, the source namespace must have the label `metrics: enabled`.

### Useful Filter Commands

To list all controller-runtime, workqueue, and REST client metric families:

```shell
curl -s http://127.0.0.1:8080/metrics \
  | grep -E '^(# HELP|# TYPE|controller_runtime_|workqueue_|rest_client_)'
```

To list only the unique metric names (no samples or labels):

```shell
curl -s http://127.0.0.1:8080/metrics \
  | grep -E '^(controller_runtime_|workqueue_|rest_client_)' \
  | cut -d'{' -f1 \
  | cut -d' ' -f1 \
  | sort -u
```

## Standard Metrics (controller-runtime)

These metrics are automatically exposed by controller-runtime and require no additional instrumentation.

### Controller Reconciliation Metrics (`controller_runtime_*`)

| Metric | Type | Description |
| ------ | ---- | ----------- |
| `controller_runtime_reconcile_total` | Counter | Total number of reconciliations per controller and result. |
| `controller_runtime_reconcile_errors_total` | Counter | Total number of reconciliation errors per controller. |
| `controller_runtime_terminal_reconcile_errors_total` | Counter | Total number of terminal (non-retryable) reconciliation errors. |
| `controller_runtime_reconcile_time_seconds` | Histogram | Time taken by each reconciliation, per controller. |
| `controller_runtime_max_concurrent_reconciles` | Gauge | Maximum number of concurrent reconciles per controller. |
| `controller_runtime_active_workers` | Gauge | Number of currently active workers per controller. |

### Work Queue Metrics (`workqueue_*`)

| Metric | Type | Description |
| ------ | ---- | ----------- |
| `workqueue_depth` | Gauge | Current depth of the work queue. |
| `workqueue_adds_total` | Counter | Total number of items added to the queue. |
| `workqueue_queue_duration_seconds` | Histogram | Time an item spends waiting in the queue before being processed. |
| `workqueue_work_duration_seconds` | Histogram | Time spent processing an item from the queue. |
| `workqueue_unfinished_work_seconds` | Gauge | Time that unfinished work has been in progress. |
| `workqueue_longest_running_processor_seconds` | Gauge | Duration of the longest running processor. |
| `workqueue_retries_total` | Counter | Total number of retries handled by the queue. |

### Kubernetes API Client Metrics (`rest_client_*`)

| Metric | Type | Description |
| ------ | ---- | ----------- |
| `rest_client_requests_total` | Counter | Total number of HTTP requests to the Kubernetes API server, partitioned by status code and method. |

### What to Monitor in Production

These standard metrics cover the **operational health** of the operator process. The most useful signals for production alerting are:

- **`controller_runtime_reconcile_errors_total`** — the most direct signal that the reconciler is failing. Filter by the `redkey` controller name.
- **`controller_runtime_reconcile_time_seconds`** — reconcile latency. A rising trend without a corresponding increase in load may indicate slow API calls or heavier logic.
- **`controller_runtime_reconcile_total`** — useful for distinguishing normal reconcile volume from requeue storms.
- **`workqueue_depth` and `workqueue_retries_total`** — backlog and retries. A growing queue that does not drain indicates the controller is saturated or blocked.
- **`workqueue_queue_duration_seconds` and `workqueue_work_duration_seconds`** — item wait time and processing time.
- **`controller_runtime_active_workers` vs `controller_runtime_max_concurrent_reconciles`** — worker pool saturation.
- **`rest_client_requests_total`** — pressure and errors against the API server. Error spikes often correlate with control-plane issues.

> **Important:** These metrics tell you whether the operator is healthy as a process, not whether your product is healthy. For example, you can have fast, error-free reconciles while your clusters remain stuck in `Configuring` or `Error` indefinitely.
