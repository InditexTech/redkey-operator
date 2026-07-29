<!--
SPDX-FileCopyrightText: 2026 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Profiling Guide

This guide explains how to use Go's built-in profiling tools (pprof) to diagnose memory leaks, high CPU usage, and goroutine leaks in the Redkey ecosystem components (Operator and Robin).

## Prerequisites

- `kubectl` access to the cluster
- Go toolchain installed locally (`go tool pprof`)
- Optionally, [Graphviz](https://graphviz.org/) for SVG visualizations

## Common pprof Usage

Both the Operator and Robin expose standard Go pprof endpoints when profiling is enabled. The techniques in this section apply to either component — only the port and enablement method differ (see component-specific sections below).

### Available Endpoints

| Endpoint                         | Purpose                               |
| -------------------------------- | ------------------------------------- |
| `/debug/pprof/`                  | Index page with links to all profiles |
| `/debug/pprof/heap`              | Heap memory profile (live objects)    |
| `/debug/pprof/allocs`            | All allocations since start           |
| `/debug/pprof/profile?seconds=N` | CPU profile for N seconds             |
| `/debug/pprof/goroutine`         | Goroutine stacks                      |
| `/debug/pprof/block`             | Blocking profile                      |
| `/debug/pprof/mutex`             | Mutex contention                      |
| `/debug/pprof/trace?seconds=N`   | Execution trace for N seconds         |
| `/debug/pprof/cmdline`           | Command-line arguments                |
| `/debug/pprof/symbol`            | Symbol lookup (for pprof tool)        |

### Heap Profiling (memory leaks)

```bash
# Interactive mode
go tool pprof http://localhost:<PORT>/debug/pprof/heap

# Save to file for later analysis
curl -o heap.pb.gz http://localhost:<PORT>/debug/pprof/heap
go tool pprof heap.pb.gz
```

Useful commands inside the interactive pprof shell:

```text
top 20          # Top 20 allocators by in-use space
top 20 -cum     # Top 20 by cumulative allocations
list <func>     # Source-annotated view of a function
web             # Open SVG call graph in browser (needs Graphviz)
```

#### Diagnosing growing memory

1. Take a baseline heap profile after startup stabilizes (~2 minutes).
1. Wait for the growth period (e.g., 1–2 hours under normal load).
1. Take a second heap profile.
1. Compare:

   ```bash
   go tool pprof -base heap-baseline.pb.gz heap-after.pb.gz
   ```

In the interactive session, `top` shows only the **difference** — allocations that grew between snapshots.

#### allocs vs heap

- `/debug/pprof/heap` — **live** (in-use) objects in memory.
- `/debug/pprof/allocs` — **all** allocations since program start (allocation-rate analysis).

### CPU Profiling

CPU profiling samples the call stack at ~100 Hz for a configurable duration (default 30s).

```bash
go tool pprof http://localhost:<PORT>/debug/pprof/profile?seconds=30
```

Inside pprof:

```text
top 20         # Hottest functions
web            # Flame-graph-style SVG
list <func>    # Line-level CPU time
```

### Goroutine Profiling

```bash
# All goroutines with stack traces
curl http://localhost:<PORT>/debug/pprof/goroutine?debug=2

# Aggregated goroutine profile
go tool pprof http://localhost:<PORT>/debug/pprof/goroutine
```

Useful for detecting goroutine leaks (growing goroutine count over time).

### Block and Mutex Profiling

```bash
go tool pprof http://localhost:<PORT>/debug/pprof/block
go tool pprof http://localhost:<PORT>/debug/pprof/mutex
```

> Note: block and mutex profiling may show zero samples unless enabled at the Go runtime level (`runtime.SetBlockProfileRate`, `runtime.SetMutexProfileFraction`).

### Execution Trace

For low-level scheduler analysis (GC pauses, goroutine scheduling):

```bash
curl -o trace.out http://localhost:<PORT>/debug/pprof/trace?seconds=5
go tool trace trace.out
```

Opens a web UI with timeline visualization of goroutine activity, GC events, and syscalls.

### Comparing Profiles (before/after a fix)

```bash
curl -o before.pb.gz http://localhost:<PORT>/debug/pprof/heap
# ... apply fix, wait ...
curl -o after.pb.gz http://localhost:<PORT>/debug/pprof/heap
go tool pprof -base before.pb.gz after.pb.gz
```

### Web UI (alternative to CLI)

```bash
go tool pprof -http=:9090 http://localhost:<PORT>/debug/pprof/heap
```

Opens a browser with flame graphs, top view, source view, and graph view.

---

## Profiling the Operator

The Redkey Operator exposes pprof on port **6060** (configurable via `--pprof-bind-address`). Profiling is disabled by default and requires a restart to toggle.

### Enabling

Add `--enable-pprof` to the operator deployment args:

```yaml
# In the operator Deployment (or Helm values)
spec:
  template:
    spec:
      containers:
        - name: manager
          args:
            - "--enable-pprof"
            - "--pprof-bind-address=:6060"
```

To enable temporarily without Helm:

```bash
kubectl patch deployment redkey-operator-controller-manager \
  -n <operator-namespace> --type='json' \
  -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--enable-pprof"}]'
```

### Disabling

Remove `--enable-pprof` from the Deployment args and let it roll:

```bash
kubectl patch deployment redkey-operator-controller-manager \
  -n <operator-namespace> --type='json' \
  -p='[{"op":"test","path":"/spec/template/spec/containers/0/args","value":null}]'
```

Or redeploy from your Helm values/kustomize without the flag.

### Accessing

```bash
kubectl port-forward -n <operator-namespace> \
  deployment/redkey-operator-controller-manager 6060:6060

# Then use any pprof command against localhost:6060
go tool pprof http://localhost:6060/debug/pprof/heap
```

---

## Profiling Robin

Robin exposes pprof on the same port as Prometheus metrics (**8080** by default). It supports **hot-toggle without restart** via the Redkey CRD.

### Enabling via CRD (recommended — hot-toggle)

Set `spec.robin.config.profiling.enabled: true` in the Redkey resource:

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: Redkey
metadata:
  name: my-cluster
spec:
  robin:
    config:
      profiling:
        enabled: true
```

The operator propagates this to the `RedkeyConfig` CRD. Robin's reconciler picks it up on the next cycle (default 30s) and **activates pprof endpoints without restart**. To disable, set `enabled: false` or remove the field.

### Enabling via command-line flag (bootstrap default)

```bash
robin --enable-pprof --cluster-name my-cluster --namespace redis
```

### Enabling on a running cluster (manual patching)

Since the operator manages the Robin deployment, direct patches will be reverted. To work around this:

1. **Scale the operator to 0** to prevent reconciliation:

   ```bash
   kubectl scale deployment redkey-operator-controller-manager \
     -n <operator-namespace> --replicas=0
   ```

1. **Add the `--enable-pprof` flag** to the Robin container:

   ```bash
   kubectl patch deployment <cluster-name>-robin -n <namespace> --type='json' \
     -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--enable-pprof"}]'
   ```

1. **When done**, restore the operator:

   ```bash
   kubectl scale deployment redkey-operator-controller-manager \
     -n <operator-namespace> --replicas=1
   ```

### Accessing

```bash
kubectl port-forward -n <namespace> deployment/<cluster-name>-robin 8080:8080

go tool pprof http://localhost:8080/debug/pprof/heap
```

---

## Security Considerations

- pprof endpoints are **unauthenticated**. Anyone with network access can capture profiles.
- Profiles may contain sensitive information (function names, memory contents).
- Use Kubernetes NetworkPolicy to restrict access to pprof ports (6060 for operator, 8080 for Robin).
- **Always disable profiling** after debugging sessions.
- Never expose pprof ports via LoadBalancer or Ingress without authentication.
- Robin's hot-toggle via CRD ensures profiling can be disabled cluster-wide without SSH/kubectl access to pods.

### Recommended NetworkPolicy (restrict pprof access)

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: restrict-pprof
spec:
  podSelector:
    matchLabels:
      app: redkey-operator
  ingress:
    - from:
        - podSelector:
            matchLabels:
              role: debugger
      ports:
        - port: 6060
```
