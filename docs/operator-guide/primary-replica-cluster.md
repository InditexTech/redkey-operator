<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Primary-Replica Clusters

Starting with the release 0.2.34, Redkey Operator supports primary-replica architecture of Redkey Cluster. This architecture increases resiliency of Redkey clusters as each primary has one or more replicas and in case of a primary goes down, its replica (or one of its replicas) can take over the role of its primary and continue to serve without a downtime and data loss in practice. Theoretically, there will be a small downtime and a possibility of a data loss if a data on a primary gets updated and primary go down before the data change propagate to its replicas but these are negligible.

Clusters with primary-replica architecture can be created by adding `replicasPerPrimary` specification and setting to an integer value inside the `spec` definition of a Redkey Cluster object. Redkey Operator will create replica nodes matching the count of `replicasPerPrimary` definition for each primary nodes.

The minimum configuration for the correct operation of this function is:

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: Redkey
...
spec:
  ...
  primaries: 3
  replicasPerPrimary: 1
```

The `primaries` definition is the total number of Redis primary nodes in the cluster. The `replicasPerPrimary` definition is the number of slaves for each primary node. The total number of Redis nodes in the cluster will be: `primaries` x `replicasPerPrimary`.

If this feature is enabled, this additional configuration should be added to `redis.conf` to improve efficiency.

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: Redkey
...
spec:
  ...
  config:
    ...
    rdbcompression yes
    rdbchecksum no
    replica-priority 10
    repl-timeout 3
    repl-disable-tcp-nodelay yes
    replica-lazy-flush yes
    cluster-slave-validity-factor 1
    repl-backlog-ttl 120
    repl-backlog-size 256mb
```

## Examples

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: Redkey
...
spec:
  ...
  primaries: 3
  replicasPerPrimary: 2

```

Redkey Operator will create 6 pods, 3 primaries and 3 replicas (one for each primary).

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: Redkey
...
spec:
  ...
  primaries: 5
  replicasPerPrimary: 2
```

Redkey Operator will create 15 pods, 5 primaries and 10 replicas (two for each primary).

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: Redkey
...
spec:
  ...
  primaries: 5
  replicasPerPrimary: 0
```

Redkey Operator will create 5 pods, 5 primaries and no replicas.

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: Redkey
...
spec:
  ...
  primaries: 5
```

Redkey Operator will create 5 pods, 5 primaries and no replicas.
