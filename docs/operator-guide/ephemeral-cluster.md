<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Ephemeral Mode / Zero Persistent Volume Claims

Ephemeral mode, also known as Zero Persistent Volume Claims (PVCs), disables persistent volume claims for storing the `redis.conf` configuration file. This mode frees up storage, removes the need for managing persistent volume claims, and decreases pod start-up time. When using Redis as a cache, it is recommended to enable ephemeral mode.

## How To Enable Ephemeral Mode

For a new cluster configuration, set the property `ephemeral: true` and apply the configuration. See the following snippet:

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: Redkey
metadata:
  name: redis-cluster
  ...
spec:
  ...
  ephemeral: true
```

## Data Directory (`/tmp` vs `/data`)

Because ephemeral clusters mount **no data volume**, Redis' working directory (`dir`) is set to
`/tmp` instead of `/data`, and `cluster-config-file` is written to `/tmp/nodes.conf`. `/tmp` is
world-writable in the container image, so it is always writable by the container's runtime UID —
including the random, non-root UID assigned by OpenShift's `restricted-v2` SCC. Persistent clusters
keep `dir /data` (the PVC mount point). See
[Cluster Configuration Defaults → Persistence Parameters](cluster-configuration.md#persistence-parameters)
for details.

> If you inspect an ephemeral Redis pod, expect `nodes.conf` under `/tmp`, not `/data`.

## Limitation: Ephemeral mode only works on new Redkey clusters

Currently, it is not possible to change a Redkey cluster from persistent to ephemeral. The reason why is that the existing statefulset of a persistent cluster has `VolumeClaimTemplates` configured. These templates cannot be removed at runtime via a patch command. See [this Kubernetes issue](https://github.com/kubernetes/kubernetes/issues/65870).
