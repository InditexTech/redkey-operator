<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Redkey Authentication

This guide explains how to configure Redis password authentication for a Redkey Cluster.

## Overview

Redkey supports Redis `requirepass` + `masterauth` authentication. The password is stored in a Kubernetes Secret and referenced by name in the `Redkey` spec. The flow is:

1. The user creates a Secret containing the Redis password.
2. The user references the Secret name in `spec.auth.secret` of the `Redkey`.
3. The Operator propagates the reference to `RedkeyConfig.spec.auth.secret`.
4. Robin detects the auth change, applies it to all running Redis nodes via `CONFIG SET requirepass` + `CONFIG SET masterauth`, and updates the ConfigMap for future pod restarts.

Auth changes are applied **in-place via hot-reload** — no pods are recycled, no rolling upgrade is triggered, and the cluster remains fully operational throughout.

```ascii
┌────────────┐       ┌──────────────────────┐       ┌─────────────────┐
│   Secret   │◄──────│  RedkeyConfig │◄──────│ Redkey   │
│ (password) │ read  │   spec.auth.secret   │ copy  │ spec.auth.secret│
└────────────┘       └──────────────────────┘       └────────────────┘
       ▲
       │ get (K8s API)
┌──────┴──────────────────────────────────────────────────────┐
│    Robin                                                    │
│                                                             │
│  1. CONFIG SET requirepass <password>  →  all running pods  │
│  2. CONFIG SET masterauth  <password>  →  all running pods  │
│  3. Update ConfigMap (for future pod restarts)              │
└─────────────────────────────────────────────────────────────┘
```

## Creating the Auth Secret

Create a Kubernetes Secret in the same namespace as the `Redkey`. The Secret must contain a key named `password` with the Redis password value:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-redis-secret
  namespace: my-namespace
type: Opaque
data:
  password: <base64-encoded-password>
```

Or using `kubectl`:

```bash
kubectl create secret generic my-redis-secret \
  --namespace=my-namespace \
  --from-literal=password='my-strong-password'
```

> **Important**: The Secret **must** be in the same namespace as the `Redkey` resource.

## Configuring the Redkey

Reference the Secret name in the `spec.auth.secret` field:

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: Redkey
metadata:
  name: my-cluster
  namespace: my-namespace
spec:
  primaries: 3
  replicasPerPrimary: 1
  auth:
    secret: my-redis-secret
  # ... other fields
```

When the Operator reconciles this resource it copies the `auth` reference into each `RedkeyConfig` it creates.

## How Robin Applies Auth Changes

Auth changes are applied via **hot-reload** — no pod recycling, no rolling upgrade, no downtime.

When Robin detects an auth change (`HasAuthChanges = true` in the change report), it calls `applyAuthToAllNodes()` which:

1. Reads the new password from the target config's auth Secret.
2. Connects to every running Redis pod using the **previous** config's credentials.
3. Issues `CONFIG SET requirepass <new-password>` on each pod.
4. Issues `CONFIG SET masterauth <new-password>` on each pod (critical for replication).
5. Updates the ConfigMap so freshly created pods inherit the new auth.
6. Updates the internal runtime config's auth secret reference.

Auth is applied **before** any cluster operation (upgrade, scaling). This ensures:
- New pods created during scaling/upgrade start with the correct `requirepass`/`masterauth`.
- All nodes share the same `masterauth` during the upgrade (replica reconnections work).
- Auth changes are never blocked by an ongoing cluster operation.

Because auth changes are separated from `HasRedisConfigChanges` in `DetectChanges()`, an **auth-only config change is immediately marked as Applied** — the cluster status remains `Ready`.

## Disabling Authentication

To run Redis without authentication, simply omit the `spec.auth` field:

```yaml
spec:
  primaries: 3
  replicasPerPrimary: 1
  # no auth field — Robin connects without a password
```

If a previously authenticated cluster should be switched to no-auth, remove the `spec.auth.secret` field from the `Redkey`. Robin will detect the empty secret name and stop sending a password.

## Rotating the Password

Robin manages `requirepass` and `masterauth` automatically. There are two ways to rotate:

### In-place Secret update (same Secret, new password)

1. Update the `password` key in the existing Secret with the new value.
2. Robin detects the change and applies `CONFIG SET requirepass <new>` + `CONFIG SET masterauth <new>` to all running nodes.
3. No pods are recycled, no downtime occurs.
4. The old password stops working after Robin completes the CONFIG SET on all nodes.

### New Secret (different name)

1. Create the new Secret with the new password.
2. Update `spec.auth.secret` in the `Redkey` to point to the new Secret name.
3. The Operator creates a new `RedkeyConfig` with the updated auth reference.
4. Robin detects the config change and applies the new password via CONFIG SET to all nodes.
5. Since auth is the only change, the config is marked as Applied immediately (no cluster operation).

> **Note**: For combined changes (e.g., auth + image upgrade together), auth is applied via CONFIG SET **before** the upgrade begins. This ensures all nodes share the same `masterauth` throughout the rolling upgrade, preventing replica reconnection failures.

## RBAC

The Operator automatically creates a Role and RoleBinding for Robin's ServiceAccount with `get` access to Secrets:

```yaml
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get"]
```

No manual RBAC configuration is needed.

## Troubleshooting

| Symptom | Cause | Resolution |
| ------- | ----- | ---------- |
| Robin logs `reading auth secret <ns>/<name>: not found` | Secret does not exist | Create the Secret in the correct namespace |
| Robin logs `auth secret <ns>/<name> missing key "password"` | Secret exists but lacks the `password` key | Add the `password` key to the Secret's `data` |
| Redis returns `NOAUTH Authentication required` | Secret password doesn't match Redis config | Ensure the Secret value matches the `requirepass` set on Redis |
| Robin connects successfully but `spec.auth.secret` is empty | Running without auth | Expected if Redis has no `requirepass` |
