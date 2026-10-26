<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Backup Configuration

## Backup procedure

Redkey does not enable any backup on its own. On persistent clusters (`ephemeral: false`), RDB
snapshots and the AOF are configured through `spec.config` with the `save` and `appendonly`
directives; when `spec.config` sets none, the Redis defaults apply (RDB snapshots with the default
save points, AOF off). See
[Cluster Configuration Defaults → Persistence Parameters](cluster-configuration.md#persistence-parameters).
Ephemeral clusters have persistence disabled by Robin. RDB backups should be uploaded to S3 with a
Cronjob process for each node.

The RDB file is recorded under `/data/dump.rdb`, and the AOF under `/data/appendonlydir/`.

![Redkey backup](../images/redisbackup.png)

## Restore procedure

Currently, there is no automated restore procedure. It's advised to manually fetch the required RDB or AOF file and run a restore procedure.

## Nodes layout

![Redkey PVC Nodes Layout](../images/redispvcnodeslayout.png)
