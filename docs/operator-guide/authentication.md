<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Redkey Authentication

This guide explains how to configure Redis password authentication for a Redkey Cluster.

## Overview

Redkey supports Redis `requirepass` authentication. The password is stored in a Kubernetes Secret and referenced by name in the `RedkeyCluster` spec. The flow is:

1. The user creates a Secret containing the Redis password.
2. The user references the Secret name in `spec.auth.secret` of the `RedkeyCluster`.
3. The Operator propagates the reference to `RedkeyClusterConfig.spec.auth.secret`.
4. Robin reads the Secret at runtime and uses the password to connect to Redis nodes.

```ascii
┌────────────┐       ┌──────────────────────┐       ┌─────────────────┐
│   Secret   │◄──────│  RedkeyClusterConfig │◄──────│ RedkeyCluster   │
│ (password) │ read  │   spec.auth.secret   │ copy  │ spec.auth.secret│
└────────────┘       └──────────────────────┘       └────────────────┘
       ▲
       │ get
┌──────┴──────┐
│    Robin    │──── connects to Redis with password
└─────────────┘
```

## Creating the Auth Secret

Create a Kubernetes Secret in the same namespace as the `RedkeyCluster`. The Secret must contain a key named `password` with the Redis password value:

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

> **Important**: The Secret **must** be in the same namespace as the `RedkeyCluster` resource.

## Configuring the RedkeyCluster

Reference the Secret name in the `spec.auth.secret` field:

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: RedkeyCluster
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

When the Operator reconciles this resource it copies the `auth` reference into each `RedkeyClusterConfig` it creates.

## How Robin Uses the Secret

Robin does **not** receive the password via CLI flags or environment variables. Instead:

1. The reconciler reads `RedkeyClusterConfig.spec.auth.secret` and stores the secret name in its internal runtime configuration.
2. The metrics collector retrieves the password from the Secret using the Kubernetes API (`GET /api/v1/namespaces/{ns}/secrets/{name}`).
3. The password is cached in-memory. If the secret name changes (e.g. a rotation to a new Secret), Robin automatically invalidates the cache and fetches the new password on the next collection cycle.

This design means:

- **No restart required** when switching to a different Secret.
- **No sensitive data** in Pod environment variables or command-line arguments.
- **RBAC-enforced**: Robin's ServiceAccount only has `get` permission on Secrets in its namespace.

## Disabling Authentication

To run Redis without authentication, simply omit the `spec.auth` field:

```yaml
spec:
  primaries: 3
  replicasPerPrimary: 1
  # no auth field — Robin connects without a password
```

If a previously authenticated cluster should be switched to no-auth, remove the `spec.auth.secret` field from the `RedkeyCluster`. Robin will detect the empty secret name and stop sending a password.

## Rotating the Password

To rotate the Redis password:

1. Update the content of the existing Secret (the `password` key) with the new password.
2. Robin caches the password per collection cycle, so it will pick up the new value after the current cache expires (typically on the next metrics collection tick).

To rotate to a completely new Secret:

1. Create the new Secret with the new password.
2. Update `spec.auth.secret` in the `RedkeyCluster` to point to the new Secret name.
3. Robin detects the name change and fetches from the new Secret automatically.

> **Note**: Ensure Redis itself is reconfigured with the new password (via `CONFIG SET requirepass` or a restart) before or in coordination with the Secret update. Redkey does not currently manage the Redis `requirepass` configuration automatically — the password in the Secret must match what Redis expects.

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
