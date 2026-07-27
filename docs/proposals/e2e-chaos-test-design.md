<!--
SPDX-FileCopyrightText: 2026 INDUSTRIA DE DISENO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# E2E Chaos Test Design

## Overview

This document describes the design for end-to-end chaos tests for the Redis Kubernetes Operator (Redkey Operator). The
chaos tests replace the legacy bash-based tests with modern, maintainable Go + Ginkgo tests
executable via `make test-chaos`.

**Key Constraints:**
- All new code goes in `/test/chaos/` (do not modify `/test/e2e/`)
- Code may be copied from `/test/e2e/` to `/test/chaos/` (annotate if done)
- When modifying Redis configuration, scale down both the **operator** AND **robin** deployments

---

## 1. Objectives & Non-Goals

### Objectives

Build **end-to-end chaos tests** that validate **Redis cluster resilience** and **operator self-healing** under
disruptive events **while the cluster is under write/read load**.

The suite must:
- Run via `make test-chaos`
- Be Go + Ginkgo v2 (same repo conventions as `test/e2e`)
- Treat the BASH tests as the **behavioral specification**
- Be deterministic enough for CI (randomness with seed capture + bounded actions)
- Use **Behavior Driven Development (BDD)** style with explicit, high-level test descriptions

### Non-Goals

- Not replacing the existing functional E2E suite in `test/e2e/`
- Not adding a full "chaos framework" abstraction layer
- Not implementing sophisticated fault injection (e.g., tc/netem) until pod-delete & topology-corruption tests are stable

---

### 2.1 Key Behaviors from `test-chaos.sh`

The main chaos loop validates:
1. **Continuous chaos loop**: Scale up → delete N random pods → wait ready → scale down → repeat
2. **k6 load running throughout** the chaos duration
3. **Recovery verification** after each disruption

> **Implementation update — continuous background disruptor (M2).** The implemented suite is more
> aggressive than the legacy script: instead of a single pod deletion per iteration, a long-lived
> **background disruptor** goroutine deletes pods at a randomized cadence
> (`CHAOS_DISRUPTION_INTERVAL`) **continuously while each operation is in flight and recovering**. It
> is paused only around each verification checkpoint (so `WaitForChaosReady` can converge) and during
> a configurable calm window (`CHAOS_STABILIZATION_WINDOW`) that lets k6 drive traffic against the
> healthy cluster before the next operation. Each scenario drives a focused disruptor (redis /
> operator / robin / mixed). See the [Chaos Testing guide](../chaos-testing.md#continuous-disruption-model).

### 2.2 Test Scenarios Covered

| Script                                   | Behavior Validated                                    | Failure Scenario                                             |
|------------------------------------------|-------------------------------------------------------|--------------------------------------------------------------|
| `test-chaos.sh`                          | Continuous scale up/down with pod deletion under load | Random pod deletion, random scaling (1-N replicas), k6 load  |
| `test-slot-busy.sh`                      | Slot ownership conflict resolution                    | Delete slots from all nodes, reassign to different owners    |
| `test-migrating-in-the-air.sh`           | Mid-migration slot recovery                           | Set slot to migrating/importing state, stop operator, resume |
| `test-with-a-master-becoming-a-slave.sh` | Primary → replica role change recovery                | Delete slots, force replication of a primary                 |
| `test-scaling-up-with-load.sh`           | Scale up during continuous k6 load                    | Scale to N replicas mid-test                                 |
| `test-scaling-up-and-delete.sh`          | Scale up followed by pod deletion                     | Scale to N, delete pods, verify recovery                     |

### 2.3 Operator/Robin Outage Pattern

From legacy scripts, when modifying Redis cluster state directly:
1. **Scale operator to 0** (stop reconciliation)
2. **Scale robin to 0** (stop sidecar management)
3. Perform disruptive action via `redis-cli`
4. **Scale robin to 1** (restore sidecar)
5. **Scale operator to 1** (resume reconciliation)
6. Verify cluster heals

---

## 3. k6 Load Testing Strategy

### 3.1 Workload Characteristics (`test-300k.js`)

- Uses `k6/x/redis` (requires **xk6-redis** extension)
- Connects to Redis **cluster** via list of nodes from `REDIS_HOSTS`
- Each iteration:
  - `SET` a key with TTL=30s
  - Either `DEL` (~10%) or `GET` (~90%)
  - Random sleep up to 100ms
- Stressors:
  - Large values (up to ~300KB) → stresses rebalance/migration edge cases
  - Steady churn with TTL → stresses cluster changes during active keyspace changes

### 3.2 k6 Execution Mode

k6 runs as a **Kubernetes Job** inside the same namespace as the cluster under test.

Benefits:
- No port-forwarding / node IP assumptions
- Stable DNS/service discovery
- Logs captured in pod logs and emitted on test failure

---

## 4. Chaos Test Scenarios

### 4.1 BDD-Style Test Structure

Each test case follows **Behavior Driven Development** style with explicit, high-level descriptions:

```go
Describe("Chaos Under Load", func() {
    It("survives continuous scaling and pod deletion while handling traffic", func() {
        // Given: A ready Redis cluster with k6 load running
        // When:  Chaos loop executes (scale, delete pods, wait recovery)
        // Then:  Cluster remains healthy, k6 completes successfully
    })
})
```

### 4.2 Scenario Definitions

#### Scenario 1: Continuous Chaos Under Load (maps: `test-chaos.sh`)

```gherkin
Given a 5-primary Redis cluster with k6 load running
When  the chaos loop executes for the configured duration:
      - Scale to random(3, 10) primaries
      - A background disruptor deletes random redis pods continuously during the scale and recovery
      - Pause the disruptor and wait for Ready status
      - Scale down to random(3, 5) primaries under continuous disruption
      - Pause the disruptor and wait for Ready status
Then  the cluster status is OK
      All 16384 slots are assigned
      k6 completes without fatal errors
```

#### Scenario 2: Chaos with Operator Deletion

```gherkin
Given a ready Redis cluster with k6 load running
When  a background disruptor continuously:
      - Deletes the operator pod (deployment recreates it)
      - Deletes random redis pods
      then it is paused and the cluster is left to reach Ready
Then  the operator recovers and heals the cluster
      k6 completes successfully
```

#### Scenario 3: Chaos with Robin Deletion

```gherkin
Given a ready Redis cluster with k6 load running
When  a background disruptor continuously:
      - Deletes robin pods
      - Deletes random redis pods
      then it is paused and the cluster is left to reach Ready
Then  robin pods are recreated
      Cluster heals to Ready status
```

#### Scenario 4: Full Chaos (Operator + Robin + Redis)

```gherkin
Given a ready Redis cluster with k6 load running
When  a background disruptor continuously deletes a random mix of:
      - operator pod
      - robin pods
      - redis pods
      while the loop periodically scales the cluster up/down,
      then it is paused between major disruptions to let the cluster reach Ready
Then  all components recover
      Cluster reaches Ready status
      k6 completes with acceptable error rate
```

#### Scenario 5: Slot Ownership Conflict Recovery

```gherkin
Given a 5-primary Ready cluster
When  operator and robin are scaled to 0
      Slot ownership is corrupted (CLUSTER DELSLOTS + SETSLOT inconsistent)
      Robin is scaled to 1
      Operator is scaled to 1
Then  the cluster heals within timeout
      Slot has exactly one owner
      Status is Ready
```

#### Scenario 6: Mid-Migration Slot Recovery

```gherkin
Given a 3-primary Ready cluster
When  operator and robin are scaled to 0
      A slot is put in migrating/importing state across nodes
      Robin is scaled to 1
      Operator is scaled to 1
Then  the cluster heals
      No slots remain in migrating/importing state
      Status is Ready
```

#### Scenario 7: Primary to Replica Demotion Recovery

```gherkin
Given a 3-primary Ready cluster
When  operator and robin are scaled to 0
      A primary node's slots are deleted
      The node is forced to replicate another primary
      Robin is scaled to 1
      Operator is scaled to 1
Then  the cluster heals
      Original topology is restored (3 primaries)
      Status is Ready
```

---

## 5. Readiness & Assertion Strategy

### 5.1 Enhanced Readiness Gate

The chaos tests require a stronger `WaitForReady` than the current E2E helper. This directly matches the bash `wait_redis_ready` definition:

```go
// WaitForChaosReady waits for the Redis cluster to be fully healthy.
// It checks:
// 1. CR .status.status == "Ready"
// 2. All redis pods pass `redis-cli --cluster check`
// 3. No nodes show `fail` or `->` markers in `cluster nodes` output
func WaitForChaosReady(ctx context.Context, client client.Client, namespace, clusterName string, timeout time.Duration) error {
    return wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
        // 1. Check CR status
        cluster := &redkeyv1.Redkey{}
        if err := client.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: namespace}, cluster); err != nil {
            return false, nil
        }
        if cluster.Status.Status != "Ready" {
            return false, nil
        }

        // 2. List redis pods
        pods := &corev1.PodList{}
        if err := client.List(ctx, pods,
            client.InNamespace(namespace),
            client.MatchingLabels{"redis.redkey.operator/component": "redis"}); err != nil {
            return false, nil
        }

        // 3. For each pod, verify cluster health
        for _, pod := range pods.Items {
            if pod.Status.Phase != corev1.PodRunning {
                return false, nil
            }
            if !clusterCheckPasses(ctx, namespace, pod.Name) {
                return false, nil
            }
            if clusterNodesHasFailure(ctx, namespace, pod.Name) {
                return false, nil
            }
        }
        return true, nil
    })
}
```

### 5.2 k6 Success Criteria

**Minimum**: Job completes with exit code 0.

**Recommended** (future enhancement): Parse k6 summary for error-rate thresholds.

---

## 6. Usage Examples

| Command                                                                       | Purpose                     |
|-------------------------------------------------------------------------------|-----------------------------|
| `make test-chaos`                                                             | Run all chaos tests         |
| `make test-chaos GINKGO_EXTRA_OPTS='--focus="survives continuous scaling"'`   | Run single scenario         |
| `make test-chaos CHAOS_ITERATIONS=3`                                          | Fewer iterations per spec   |
| `make test-chaos CHAOS_SEED=12345`                                            | Reproducible random seed    |
| `make k6-build`                                                               | Build k6 image only         |

---

## 7. k6 Image Build Strategy

### 7.1 Dockerfile (`test/chaos/k6.Dockerfile`)

```dockerfile
FROM golang:1.24-alpine AS builder

RUN go install go.k6.io/xk6/cmd/xk6@latest
RUN xk6 build \
    --with github.com/grafana/xk6-redis \
    --output /k6

FROM alpine:3.21
COPY --from=builder /k6 /usr/bin/k6
COPY k6scripts/ /scripts/
ENTRYPOINT ["/usr/bin/k6"]
```

### 7.2 k6 Job Template

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: k6-load-{{ .Name }}
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: k6
        image: {{ .K6Image }}
        args:
        - run
        - /scripts/test-300k.js
        - --duration
        - {{ .Duration }}
        - --vus
        - {{ .VUs }}
        env:
        - name: REDIS_HOSTS
          value: {{ .RedisHosts }}
```

---

## 8. File Structure

```
test/chaos/
├── suite_test.go             # Ginkgo suite bootstrap (minimal)
├── chaos_suite_test.go       # Main chaos test scenarios (BDD style)
├── helpers.go                # Test-level helper functions
├── framework/
│   ├── cluster.go            # Redkey creation helpers
│   ├── k6.go                 # k6 Job creation and monitoring
│   ├── namespace.go          # Namespace creation/deletion helpers
│   ├── operator.go           # Operator scaling helpers (ScaleOperatorDown/Up)
│   ├── operator_setup.go     # Operator deployment setup (RBAC, ConfigMap, Deployment)
│   ├── readiness.go          # Enhanced WaitForChaosReady + remote command execution
│   └── redis_chaos.go        # Pod deletion, robin scaling, slot manipulation
├── k6.Dockerfile             # k6 image build with xk6-redis
└── k6scripts/
    └── test-300k.js          # Copied from OLD_BASH_TESTS/k6scripts
```

**Note**: Framework helpers are adapted from `test/e2e/framework/` with modifications for chaos-specific needs. Source files are annotated in comments.

---

## 9. Test Execution Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                    Ginkgo Test Lifecycle                        │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  BeforeSuite:                                                   │
│    1. Verify CRD installed                                      │
│    2. Verify k6 image available                                 │
│    3. Initialize random seed (from CHAOS_SEED or GinkgoSeed)    │
│                                                                 │
│  BeforeEach (per test):                                         │
│    1. Create isolated namespace                                 │
│    2. Deploy operator in namespace                              │
│    3. Create Redkey (N primaries + robin)                │
│    4. Wait for Ready status                                     │
│                                                                 │
│  Test Body (BDD style):                                         │
│    Given: Cluster is ready                                      │
│    When: [Optional] Start k6 load Job                           │
│          Chaos Action Loop:                                     │
│            1. PerformChaosAction() - random selection           │
│            2. WaitForChaosReady()                               │
│            3. Repeat until duration expires                     │
│    Then: Assertions                                             │
│          - Cluster status OK                                    │
│          - All 16384 slots assigned                             │
│          - No nodes in fail state                               │
│          - k6 Job succeeded (if started)                        │
│                                                                 │
│  AfterEach:                                                     │
│    1. Collect logs on failure (operator, k6, redis pods)        │
│    2. Delete k6 Job (if exists)                                 │
│    3. Delete Redkey (remove finalizers)                  │
│    4. Delete namespace                                          │
│                                                                 │
│  AfterSuite:                                                    │
│    1. Cleanup any orphaned resources                            │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

---

## 10. Helper Functions

### 10.1 Framework Helpers

```go
// Cluster Setup
func CreateAndWaitForReadyCluster(ctx, client, namespace, name string, primaries int) error
func EnsureOperatorRunning(ctx, client, namespace string) error

// Chaos Actions
func DeleteRandomRedisPods(ctx, client, namespace, clusterName string, count int) error
func DeleteOperatorPod(ctx, client, namespace string) error
func DeleteRobinPods(ctx, client, namespace, clusterName string, count int) error
func ScaleCluster(ctx, client, namespace, clusterName string, primaries int) error
func ScaleOperatorDown(ctx, client, namespace string) error
func ScaleOperatorUp(ctx, client, namespace string) error
func ScaleRobinDown(ctx, client, namespace, clusterName string) error
func ScaleRobinUp(ctx, client, namespace, clusterName string) error

// Slot Corruption (requires operator/robin down)
func CorruptSlotOwnership(ctx, namespace, clusterName string, slot int) error
func SetSlotMigrating(ctx, namespace, clusterName string, slot int) error
func ForcePrimaryToReplica(ctx, namespace, clusterName string, podName string) error

// k6 Load
func StartK6LoadJob(ctx, client, namespace, clusterName string, duration time.Duration, vus int) error
func WaitForK6JobCompletion(ctx, client, namespace, jobName string) error
func GetK6JobLogs(ctx, client, namespace, jobName string) (string, error)

// Assertions
func WaitForChaosReady(ctx, client, namespace, clusterName string, timeout time.Duration) error
func AssertAllSlotsAssigned(ctx, namespace, clusterName string) error
func AssertNoNodesInFailState(ctx, namespace, clusterName string) error
```

### 10.2 Test-Level Helpers

```go
// startK6OrFail starts a k6 load job and fails the test if it errors.
func startK6OrFail(c client.Client, namespace, clusterName string, duration time.Duration, vus int) string

// cleanupK6Job safely deletes a k6 job, ignoring errors.
func cleanupK6Job(c client.Client, namespace, jobName string)

// chaosLoop runs a chaos function repeatedly until the duration expires.
func chaosLoop(duration time.Duration, chaosFn func(iteration int))

// verifyClusterHealthy runs all cluster health checks.
func verifyClusterHealthy(c client.Client, namespace, clusterName string)

// verifyK6Completed waits for k6 job to complete successfully.
func verifyK6Completed(c client.Client, namespace, jobName string, timeout time.Duration)
```

---

## 11. Architectural Decisions

### 11.1 Design Rationale

| Decision                               | Rationale                                                                               |
|----------------------------------------|-----------------------------------------------------------------------------------------|
| **Sequential execution** (`--procs=1`) | Chaos tests modify shared cluster state; parallel execution would cause race conditions |
| **k6 as Job (not sidecar)**            | Jobs have clear success/failure semantics; easier to check exit code                    |
| **Operator in-namespace**              | Isolates tests completely; each test has its own operator instance                      |
| **Remote command via exec**            | Direct redis-cli execution for slot manipulation; no external dependencies              |
| **No DescribeTable for chaos**         | Each scenario has unique setup/teardown; tables obscure failure points                  |
| **Explicit phase assertions**          | Trace status transitions to catch improper healing behavior                             |
| **Scale down operator AND robin**      | For topology corruption tests, both must stop to prevent interference                   |

### 11.2 Anti-Patterns Avoided

| Anti-Pattern                       | Alternative Used                             | Benefit                                  |
|------------------------------------|----------------------------------------------|------------------------------------------|
| DescribeTable with complex entries | Separate `It()` blocks per scenario          | Clear test intent, easier debugging      |
| Anonymous function validators      | Named helper functions in framework          | Reusable, testable in isolation          |
| Magic timeouts                     | Configurable via environment/Makefile        | Adaptable to different environments      |
| Shelling out to kubectl            | controller-runtime client + client-go exec   | Type-safe, no path dependencies          |
| Hard-coded sleeps                  | `Eventually` / polling helpers with timeouts | Reliable, explicit timeout behavior      |
| Host-local k6                      | k6 runs in cluster via Job                   | Consistent environment, no network hacks |
| Unbounded randomness               | Actions seeded with `GinkgoRandomSeed()`     | Reproducible failures                    |

---

## 12. Environment Variables

### Makefile variables (set via `make` or exported in shell / `.envrc`)

| Variable                 | Default                                              | Purpose                                                                                                        |
|--------------------------|------------------------------------------------------|----------------------------------------------------------------------------------------------------------------|
| `TEST_PARALLEL_PROCESS`  | `8`                                                  | Number of parallel Ginkgo processes (`-procs`). Applies to both E2E and chaos tests.                           |
| `GOMAXPROCS`             | `8`                                                  | Go runtime parallelism. Should match `TEST_PARALLEL_PROCESS`.                                                  |
| `K6_IMG`                 | `localhost:5001/redkey-k6:dev`                       | k6 container image with xk6-redis extension.                                                                   |
| `CHAOS_ITERATIONS`       | `10`                                                 | Number of chaos loop iterations per test spec.                                                                 |
| `CHAOS_SEED`             | *(auto: Ginkgo random seed)*                         | Fixed random seed for reproducibility.                                                                         |
| `CHAOS_TIMEOUT`          | `100m`                                               | Maximum Ginkgo suite timeout (`--timeout`). Must accommodate iterations x recovery time / parallelism.         |
| `CHAOS_PACKAGES`         | `./test/chaos`                                       | Go test packages for the chaos suite.                                                                          |
| `IMG`                    | `localhost:5001/redkey-operator:$(VERSION)`           | Default operator image build tag. Used as fallback for `IMAGE_OPERATOR`.                                       |
| `IMG_ROBIN`              | `ghcr.io/inditextech/redkey-robin:$(ROBIN_VERSION)`  | Legacy Robin image variable. Prefer `IMAGE_ROBIN` for E2E runs.                                                |
| `REDIS_IMAGE`            | `redis:8.4.0`                                        | Redis image used for cluster pods.                                                                             |
| `GINKGO_EXTRA_OPTS`      | *(empty)*                                            | Extra Ginkgo flags (e.g. `--focus="..."`, `--label-filter=...`).                                               |

### Test-only environment variables (read by Go code, not in Makefile defaults)

| Variable                          | Default                   | Purpose                                                                                  |
|-----------------------------------|---------------------------|------------------------------------------------------------------------------------------|
| `CHAOS_KEEP_NAMESPACE_ON_FAILED`  | *(unset)*                 | When non-empty, preserves failed test namespaces for post-mortem inspection.              |
| `KUBECONFIG`                      | `~/.kube/config`          | Path to kubeconfig file.                                                                 |
| `IMAGE_OPERATOR`                  | `localhost:5001/redkey-operator:dev` | Operator image loaded into Kind and deployed by the E2E suite.                  |
| `IMAGE_ROBIN`                     | `localhost:5001/redkey-robin:dev`    | Robin image loaded into Kind and injected into the operator deployment.          |

### Recommended `.envrc` for local development

```shell
export TEST_PARALLEL_PROCESS=8
export GOMAXPROCS=8
export CHAOS_KEEP_NAMESPACE_ON_FAILED=true
export IMG_ROBIN=localhost:5001/redkey-robin:0.1.0
```

---

 ## 13. Migration Map

| Legacy Script                            | New Ginkgo Test                              | Notes                             |
|------------------------------------------|----------------------------------------------|-----------------------------------|
| ` test-chaos.sh`                         | `It("survives continuous chaos under load")` | Main chaos loop with k6           |
| `test-slot-busy.sh`                      | `It("heals slot ownership conflicts")`       | Operator/Robin scale down pattern |
| `test-migrating-in-the-air.sh`           | `It("recovers from mid-migration slots")`    | Operator/Robin scale down pattern |
| `test-with-a-master-becoming-a-slave.sh` | `It("recovers from forced role change")`     | Operator/Robin scale down pattern |
| `test-scaling-up-with-load.sh`           | `It("scales up under k6 load")`              | k6 Job integration                |
| `test-scaling-up-and-delete.sh`          | Covered by main chaos loop                   | Pod deletion after scaling        |
| `test-size-change.sh`                    | Covered by existing E2E tests                | No migration needed               |
| `test-template-change.sh`                | Covered by existing E2E tests                | No migration needed               |
