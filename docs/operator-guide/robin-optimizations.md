<!--
SPDX-FileCopyrightText: 2026 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Redkey Robin — Connection & Memory Optimizations

This document describes optimizations applied to **Redkey Robin** to eliminate growing memory and object counts observed under steady-state operation.

## Problem Statement

With a stable 3P+1R Redis cluster and no topology changes, Grafana showed monotonically growing:

- `go_memstats_heap_objects` (Live Objects)
- `go_memstats_mallocs_total - go_memstats_frees_total` (Live Objects, alternate formula)

Root causes were identified via pprof profiling and code review.

## 1. CLUSTER INFO as Prometheus Labels (infinite time series)

**Problem:** `processClusterInfo()` used all raw CLUSTER INFO values (including counters like `cluster_stats_messages_sent`, `cluster_stats_messages_received`) as Prometheus label values. Since these counters change on every scrape, each cycle created a unique label combination → new Gauge object → infinite accumulation.

**Fix:** Only `cluster_state` is used as a label. Numeric values are exposed as individual metrics (e.g., `redkey_cluster_info_cluster_known_nodes`). This bounds the number of time series to a fixed, small set.

**Files:** `internal/metrics/collector.go` — `processClusterInfo()`

## 2. Redis Client Leak (~18 clients per cycle)

**Problem:** The metrics collector created a new `redis.Client` for every node on every 30s collection cycle, then closed it. With 6 nodes × 3 operations (INFO, CLUSTER INFO, CLUSTER NODES), this created ~18 short-lived connections per cycle, each allocating bufio buffers, TLS state, and goroutines.

**Fix:** Client pooling via `reconcileClientPool()`. Clients are cached by address and reused across cycles. They are only discarded on error or when a node leaves the cluster.

**Files:** `internal/metrics/collector.go` — `reconcileClientPool()`, `getOrCreateClient()`, `discardClient()`, `closeAllClients()`

## 3. Health Checker Client Leak (same pattern)

**Problem:** Same pattern as the collector — new client per node per health check cycle.

**Fix:** Same pooling pattern applied to the health checker.

**Files:** `internal/health/checker.go` — `reconcileClients()`, `getOrCreateClient()`, `discardClient()`, `CloseAll()`

## 4. Excessive go-redis Pool Size

**Problem:** go-redis defaults `PoolSize` to `10 * runtime.GOMAXPROCS(0)`. On a 4-core container, that's 40 idle connections per client. Since Robin uses clients sequentially (one command at a time), this wastes memory.

**Fix:** `PoolSize=1`, `MaxIdleConns=1`, explicit timeouts (`DialTimeout=5s`, `ReadTimeout=10s`, `WriteTimeout=5s`).

**Files:** `internal/redis/client.go` — `NewClient()`

## 5. Metric Reset on Every Cycle (unnecessary allocations)

**Problem:** `processClusterNodes()` called `ResetMetricByName()` on every 30s cycle, then re-registered all node metrics. Each reset+re-register allocated new Gauge objects.

**Fix:** Track a node fingerprint (ID+IP+Flags+Slots+Primary+State). Only reset metrics when the fingerprint changes (node added, removed, or role changed). Under steady state, no resets occur.

**Files:** `internal/metrics/collector.go` — `lastClusterNodeIDs` field, fingerprint comparison in `processClusterNodes()`

## Verification

After applying all fixes, pprof profiling confirmed:

- HeapObjects oscillates between ~23k–45k (GC sawtooth) with no monotonic growth
- HeapAlloc stable at ~7–9 MB
- Goroutine count stable at 12
- 10-minute differential heap profile shows net **negative** delta (memory freed by GC)

The `go_memstats_heap_objects` metric in Prometheus confirmed oscillation without upward trend.
