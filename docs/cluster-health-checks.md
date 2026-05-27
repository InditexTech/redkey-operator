<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Cluster Health Checks and Remediation

When the Redkey Cluster reaches the **Ready** status, Redkey Robin continuously monitors the cluster health and automatically remediates detected issues. This document describes the health check pipeline and the remediation actions taken.

## Health Check Report

Each reconciliation cycle produces a health report with the following checks:

| Check | Description |
|-------|-------------|
| **MembershipOK** | All expected pod IPs are known to the cluster, and no stale nodes remain |
| **SlotsCoveredOK** | All 16384 hash slots are assigned to primaries |
| **ReplicaSpreadOK** | Correct number of primaries and replicas; each primary has the expected replica count; no orphan or chained replicas |
| **BalancedOK** | Slot distribution across primaries is balanced (no node deviates >2% from ideal) |
| **ClusterCheckOK** | `redis-cli --cluster check` reports no errors |

The cluster is considered **Healthy** only when all five checks pass.

## Remediation Pipeline

When the cluster is not healthy, Robin executes remediation steps in order, stopping at the first failure:

### 1. Membership Remediation

- **CLUSTER FORGET**: Removes stale node IDs not associated with any current pod IP from all known nodes.
- **CLUSTER MEET**: Introduces pod IPs that are missing from the cluster view.
- After meeting new nodes, waits for gossip propagation (`ClusterMeetWaitSeconds`, default 5s).
- **After a successful membership fix, Robin defers all further remediation to the next cycle.** The cluster topology is in flux after a MEET/FORGET and the health report captured before the change is stale — running slot or rebalance operations on outdated topology data could cause data loss.

### 2. Slot Coverage Remediation

- Identifies unassigned slots (those not covered by any primary).
- Distributes missing slots evenly across existing primaries using `CLUSTER ADDSLOTS`.

### 3. Replica Spread Remediation

- Detects replicas assigned to the wrong primary or orphaned replicas.
- Reassigns replicas using `CLUSTER REPLICATE` to achieve the desired topology (N replicas per primary, evenly distributed).
- **Failover recovery**: When a primary pod is deleted and its replica is promoted by Redis Cluster auto-failover, the replacement pod joins as an empty primary. Robin detects the excess primary (0 slots, no importing/migrating state) and demotes it to a replica of the promoted primary, preserving topology without triggering a rebalance.

### 4. Cluster Fix (`redis-cli --cluster fix`)

- Runs `redis-cli --cluster fix` to resolve inconsistencies such as migrating slots or open slots.
- Subject to configurable timeout (`clusterCommandTimeoutSeconds`, default 24s).

### 5. Rebalance (`redis-cli --cluster rebalance`)

- Runs `redis-cli --cluster rebalance --cluster-use-empty-masters` to redistribute slots evenly.
- Subject to configurable timeout (`rebalanceTimeoutSeconds`, default 120s).

## Configuration

The following fields in `RedkeyClusterConfig.spec.robin.cluster` control health check behavior:

| Field | Default | Description |
|-------|---------|-------------|
| `clusterCommandTimeoutSeconds` | 24 | Timeout for `redis-cli --cluster fix` commands |
| `rebalanceTimeoutSeconds` | 120 | Timeout for `redis-cli --cluster rebalance` commands |
| `clusterMeetWaitSeconds` | 5 | Time to wait for gossip propagation after `CLUSTER MEET` |

## Behavior Notes

- Health checks and remediation run **only** when the cluster is in **Ready** status.
- No status transition occurs during health checks — the cluster remains in Ready.
- If remediation fails, the error is logged and retried on the next reconciliation cycle.
- All remediation actions are idempotent and safe to retry.
