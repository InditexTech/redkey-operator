<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# AGENTS.md

Reference guide for AI agents and automated tools working on the Redkey Operator codebase.

---

## Project Summary

**Redkey Operator** is a Kubernetes operator that deploys and manages Redkey clusters — key/value clusters built from [Redis](https://hub.docker.com/_/redis) or [Valkey](https://hub.docker.com/r/valkey/valkey/) official images.

It implements the [operator pattern](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/) and extends the Kubernetes API with two custom resources:

- **`RedkeyCluster`** — declares the desired state of a Redis/Valkey cluster (topology, storage, scaling, authentication, etc.).
- **`RedkeyClusterConfig`** — tracks individual configuration revisions applied to a cluster.

The operator reconciles the declared state, manages the lifecycle of Kubernetes objects (StatefulSets, Services, PodDisruptionBudgets, PVCs), and coordinates Redis-side operations through **Redkey Robin**.

### Technology Stack

| Layer | Technology |
| ----- | ---------- |
| Language | Go 1.26.4 |
| Framework | [kubebuilder](https://github.com/kubernetes-sigs/kubebuilder) + [operator-sdk](https://github.com/operator-framework/operator-sdk) |
| Kubernetes client | [controller-runtime](https://sigs.k8s.io/controller-runtime) v0.21.0 |
| Target platform | Kubernetes v1.33 (also OpenShift v4.11+) |
| Packaging | Helm charts (`charts/redkey-operator`, `charts/redkey-cluster`) + OLM bundle |
| Testing | Go `testing`, [Ginkgo v2](https://github.com/onsi/ginkgo) + [Gomega](https://github.com/onsi/gomega), [envtest](https://sigs.k8s.io/controller-runtime/tools/setup-envtest) |
| Linting | [golangci-lint](https://github.com/golangci/golangci-lint) v2.1.0 |

---

## Repository Layout

Key paths to understand before changing code:

- `api/v1beta1/`: CRD types and API schema.
- `cmd/main.go`: operator entrypoint and controller startup wiring.
- `internal/controller/`: reconcile logic, config lifecycle, and Robin integration.
- `config/`: generated CRDs, RBAC, manager manifests, samples, and kustomize bases.
- `charts/`: Helm packaging for operator and sample cluster installation.
- `test/integration/`: envtest-based integration suite.
- `test/e2e/`: Kind-based end-to-end suite.
- `docs/`: operator, architecture, OLM, and developer documentation.

Operational assumptions:

- The operator is stateless; durable state lives in Kubernetes resources.
- `RedkeyClusterConfig` is the revision-tracking resource used to coordinate applied and pending cluster changes.
- Redkey Robin is part of the runtime architecture; operator changes that affect rollout orchestration may require checking Robin integration paths too.
- Generated artifacts matter in this repository: API or manifest changes are not complete until `make generate` and `make manifests` have been run.

---

## Build Commands

All common operations are driven by `make`. Tools that are not yet present are downloaded automatically into `bin/`.

This repository does not use Maven. There is no `pom.xml` or `mvnw` in Redkey Operator, so agents must not suggest `mvn` commands here. If an external workflow or template expects Maven goals/phases, use the following equivalents.

### Maven goal equivalents

| Maven goal or phase | Operator command | Notes |
| ------------------- | ---------------- | ----- |
| `mvn validate` | `make manifests && make generate && make fmt && make vet && make lint` | Closest pre-test validation sequence for this repo. |
| `mvn test` | `make test` | Unit tests only. Generates `cover.out`. |
| `mvn failsafe:integration-test` | `make test-integration` | Runs envtest-based integration tests. |
| `mvn failsafe:verify` | `make test-integration` | Same integration suite; no separate Maven-style verify step exists. |
| `mvn verify` | `make verify` | Canonical full local gate for the operator. |
| `mvn package` | `make build` | Produces `bin/manager`. |
| `mvn install` | not applicable | No Maven-style local artifact install phase exists. |
| profile-driven system/e2e tests | `make setup-test-e2e && make test-e2e` | Use `make cleanup-test-e2e` afterwards. |

### Prerequisites (installed manually)

- Go 1.26+
- Make
- Docker or Podman
- `kubectl`

### Code generation (run after changing API types)

```shell
make generate    # regenerates DeepCopy methods
make manifests   # regenerates CRDs, RBAC manifests, and webhooks
```

### Format and static analysis

```shell
make fmt         # go fmt ./...
make vet         # go vet ./...
make lint        # golangci-lint run
make lint-fix    # golangci-lint run --fix
```

### Binary build

```shell
make build       # runs manifests, generate, fmt, vet, test-all, then: go build -o bin/manager cmd/main.go
```

### Container image

```shell
make docker-build              # builds image tagged as localhost:5005/redkey-operator:<VERSION>
make docker-push               # pushes the image
make docker-buildx             # cross-platform build (linux/amd64 + linux/arm64) and push
```

Override the image tag with `IMG=<registry>/<name>:<tag>`.

### Installer manifest

```shell
make build-installer           # produces dist/install.yaml (CRDs + Deployment, via kustomize)
```

### OLM and release packaging

```shell
make bundle                    # generates and validates the OLM bundle
make bundle-build              # builds bundle image
make bundle-push               # pushes bundle image
make catalog-build             # builds OLM catalog image from bundle images
make catalog-push              # pushes catalog image
make bundle-deploy             # installs the operator from the bundle image
make bundle-undeploy           # removes bundle-based installation
```

### Canonical verification

```shell
make verify      # fmt + vet + lint + build + test-all
```

`make verify` is the preferred full local gate. It is broader than `make lint && make test-all` because it also exercises the build path.

---

## Testing Instructions

### Mandatory validation for every change

For code changes in this repository, before considering the task complete, run:

```shell
make lint
make test-all
```

Use `make verify` when you want the widest local gate before handing off or opening a PR.

If a change touches API types, generated manifests, deployment packaging, or install flows, also run the matching generation or packaging target you changed, such as `make generate`, `make manifests`, `make build-installer`, or `make bundle`.

If a change only touches documentation or agent instructions, executable validation may be skipped when there is nothing meaningful to compile or run; in that case, keep the edit limited and aligned with the Makefile and repository layout.

### Recommended validation flows

```shell
make test                               # fast unit-focused loop while iterating
make test-integration                   # envtest coverage for controller behavior
make verify                             # preferred full local gate
make setup-test-e2e && make test-e2e    # cluster-level behavior checks when relevant
make cleanup-test-e2e                   # tear down the isolated e2e cluster afterwards
```

### Unit tests

```shell
make test
```

Runs all packages except `e2e` and `test/integration`. Generates a coverage profile at `cover.out`.

```shell
make coverage    # generates coverage.html from cover.out
```

### Integration tests (envtest)

Requires the envtest binaries to be present. The target installs them automatically.

```shell
make test-integration
```

Uses `KUBEBUILDER_ASSETS` to point the test suite to fake Kubernetes API binaries; no real cluster required.

### All tests (unit + integration)

```shell
make test-all
```

### End-to-end tests

Requires a Kind cluster and a running local registry.

```shell
make setup-test-e2e   # creates Kind cluster + loads manager image
make test-e2e         # runs ./test/e2e/ with Ginkgo
make cleanup-test-e2e # tears down the e2e Kind cluster
```

Override the cluster name with `KIND_CLUSTER_E2E=<name>`.

These tests are not part of `make test-all`. Run them when the change affects deployment flows, reconciliation across real cluster objects, networking, or install/upgrade behavior.

### Local development cluster

```shell
make setup-kind       # creates Kind cluster + local registry on port 5005
make install          # installs CRDs into the current kubeconfig cluster
make run              # runs the controller locally against the cluster
make deploy-samples   # deploys the sample RedkeyCluster resource
make cleanup-kind     # tears down the Kind cluster
```

---

## Style Guidelines

### Code conventions

- Follow standard Go idioms and the [Effective Go](https://go.dev/doc/effective_go) guidelines.
- For every change, run `make lint` and `make test-all` before finishing the task.
- All code must pass `go fmt`, `go vet`, and `golangci-lint` before being merged.
- API types live in `api/v1beta1/`. After changing them, always run `make generate && make manifests`.
- Controller logic lives in `internal/controller/`. Files are split by concern:
  - `redkeycluster_controller.go` — main reconcile loop
  - `redkeycluster_config.go` — `RedkeyClusterConfig` lifecycle
  - `redkeycluster_robin.go` — Redkey Robin integration
- Tests mirror their subject file with a `_test.go` suffix in the same package.
- Integration tests live under `test/integration/`; e2e tests live under `test/e2e/`.
- Use `go.uber.org/zap` through the controller-runtime logger; do not use `fmt.Print*` for operational output.

### Architecture conventions

- The operator is stateless: all state is persisted in Kubernetes resources.
- Reconcile functions must be idempotent.
- `RedkeyClusterConfig` objects carry the annotation `redkey.inditex.dev/cluster-generation` to correlate config revisions with the parent cluster generation.
- Cleanup logic retains the last `ConfigPhaseApplied` config and any newer configs; do not assume older configs survive cleanup.
- REUSE compliance is required: every source file must have an `SPDX-FileCopyrightText` and `SPDX-License-Identifier` header. See `REUSE.toml` and `hack/boilerplate.go.txt`.

---

## Upgrade Architecture — Cross-Repo Knowledge

The upgrade lifecycle is split between the operator and Robin. Understanding the boundary is critical.

### Operator Responsibilities (this repo)

1. **Trigger**: When any spec field that affects the Redis pod template changes — `image`, `version`, `redisConfig`, `resources`, `labels`, `annotations`, `override`, or `pdb` — the operator creates a new `RedkeyClusterConfig` with the updated spec. Robin then recycles the pods (it compares `controller-revision-hash` against the StatefulSet `UpdateRevision`, so it is not limited to image changes). Topology fields (`primaries`, `replicasPerPrimary`) drive scaling instead, and `robin.*` changes are handled as a Robin hot-reload.
2. **Config Checksum**: A SHA-256 checksum annotation (`redkey.inditex.dev/config-checksum`, 16 hex chars) is set on the pod template to ensure StatefulSet detects changes even when only `redisConfig` differs.
3. **Robin Deployment**: The operator ensures a Robin Deployment exists per cluster that watches for config changes. The Robin image is specified via Helm values or the operator container environment.

### Robin Responsibilities (in `../redkeyrobin`)

Robin owns the entire upgrade execution:
- Strategy selection: Fast vs Rolling N+1
- StatefulSet manipulation (scale, template updates with OnDelete strategy, manual pod deletion)
- Redis cluster commands (`CLUSTER MEET`, `CLUSTER REPLICATE`, `CLUSTER FORGET`, `--cluster reshard`, `--cluster fix`)
- Status/substatus updates on the `RedkeyClusterConfig`
- **HA preservation**: Only drained primaries (0 slots) and their specific replicas are recycled. Replicas of active primaries are never touched.

### Key CRD Constants for Upgrade (defined in `api/v1beta1/`)

```go
// Strategy selection criteria:
// fastUpgradeEligible = Ephemeral && ReplicasPerPrimary == 0 && PurgeKeysOnRebalance == true
// Everything else → Rolling N+1

// Substatus flow — Rolling N+1:
SubstatusUpgradeScalingUp     = "AddingExtraNode"
SubstatusUpgradeResharding    = "DrainingNode"
SubstatusUpgradeRollingUpdate = "RollingUpdate"
SubstatusUpgradeEnding        = "MovingLastSlots"
SubstatusUpgradeScalingDown   = "RemovingExtraNode"

// Substatus flow — Fast Upgrade:
SubstatusFastUpgrading     = "FastUpgrading"
SubstatusEndingFastUpgrade = "FormingCluster"
```

> **Note:** `SubstatusEndingFastUpgrade` deliberately reuses the literal string
> `"FormingCluster"` (the same value as `SubstatusFormingCluster` used during initial
> cluster formation and fast scaling). The final step of a fast upgrade is functionally
> a cluster re-formation, so the user-facing substatus is intentionally identical. When
> matching on substatus, distinguish these by the surrounding status/context, not by the
> string alone.

### Important: Clusters with Replicas ALWAYS Use Rolling N+1

Even if a cluster is ephemeral with `purgeKeysOnRebalance=true`, if `replicasPerPrimary > 0` it uses Rolling N+1. This is intentional — destroying replicas causes election races and potential data loss during cluster reformation.

### StatefulSet Layout (reference for pod ordinal calculations)

For `primaries=P`, `replicasPerPrimary=R`:
- Pods `0 .. P-1` → primaries
- Pods `P .. P+P*R-1` → replicas
- During upgrade: pods `P+P*R` → extra primary, pods `P+P*R+1 .. P+P*R+R` → extra replicas

Formula: `totalMembers = P + P*R`. This is the base size before the extra node is added.

### E2E Testing (runs from this repo)

```shell
# Full upgrade suite:
make test-e2e LABEL=upgrade

# With replicas only (critical path — most bugs appear here):
go test ./test/e2e/ -v -ginkgo.v -ginkgo.label-filter="upgrade" -ginkgo.focus="with replicas" -timeout 20m

# Without replicas:
go test ./test/e2e/ -v -ginkgo.v -ginkgo.label-filter="upgrade" -ginkgo.focus="without replicas" -timeout 15m
```

E2E tests require:
- Kind cluster `redkey-operator-test-e2e` (or override with `KIND_CLUSTER_E2E`)
- Local registry at `localhost:5005`
- Both images pushed: `localhost:5005/redkey-operator:<VERSION>` and `localhost:5005/redkey-robin:<VERSION>`

### Documentation

Upgrade docs live in `docs/operator-guide/upgrade.md` and `docs/redkey-cluster-status.md`. These document the Rolling N+1 pivot pattern, substatus flow, and topology support matrix. Keep them in sync when modifying the upgrade flow.

### Auth Hot-Reload Across Repos

Auth changes are applied via CONFIG SET (hot-reload) by Robin, not via pod recycling:

| Repo | File | Role |
| ---- | ---- | ---- |
| Robin | `internal/reconciler/config_changes.go` | `HasAuthChanges` field, `detectRedisConfigChanges()` no longer checks Auth |
| Robin | `internal/reconciler/cluster_reconciler.go` | `applyAuthToAllNodes()`, modified `handleConfigChange()` |
| Operator | `test/e2e/framework/redisclient.go` | `CheckAuthRequired()`/`CheckAuthDisabled()` helpers (no false positive from `-a` flag) |
| Operator | `test/e2e/auth_test.go` | Fixed false positives by using `CheckAuthRequired` for unauthenticated checks |
| Operator | `test/e2e/auth_and_upgrade_test.go` | Combined auth + upgrade scenarios |
| Robin | `test/integration/config_changes_test.go` | Auth-only config marked Applied without upgrade transition |

**Key e2e pattern**: Use `CheckAuthRequired(clusterNs, pod)` (no password) instead of `PingRedis(clusterNs, pod, password)` to verify auth enforcement without false positives from `redis-cli -a` behavior.

**Key robin integration pattern**: Auth-only changes must result in `ConfigPhaseApplied` + `ClusterStatusReady` (no `Upgrading`/`InProgress`).

### Dependency management

- Use `go mod tidy` after adding or removing dependencies.
- Do not vendor dependencies; the project relies on the Go module cache.

---

## Commit and PR Management

### Commit format

Follow the [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) specification:

```text
<type>(<optional scope>): <short description>

[optional body]

[optional footers]
Signed-off-by: Name <email>
```

Common types: `feat`, `fix`, `docs`, `chore`, `refactor`, `test`, `ci`.

### Required commit properties

Every commit in a PR **must**:

1. Include a `Signed-off-by` trailer (`git commit -s`). This certifies agreement with the [CLA](./CLA.md).
2. Be GPG-signed with a verified key (`git commit -S`, or set `git config --local commit.gpgsign true`).
3. Use a verified email address associated with the GitHub account.

### Pull Request guidelines

- Open an issue before starting significant work and reference it in the PR (`Closes #<issue>`).
- Check existing issues and PRs to avoid duplicate work.
- Keep PRs focused; split unrelated changes into separate PRs.
- For every change, ensure `make lint` and `make test-all` pass locally before opening or updating a PR.
- Ensure `make verify` passes locally before opening a PR.
- If the change affects install manifests, charts, or OLM packaging, run the relevant packaging target locally before opening the PR.
- Document new features or behaviour changes in `docs/`.
- Add or update tests for every code change.
- An automated check will validate commit signatures and CLA compliance on every PR.
