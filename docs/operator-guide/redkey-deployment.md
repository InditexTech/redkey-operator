<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# How to deploy Redkey Cluster

## CRD

Redkey operator CRD defines a new resource type `Redkey`.

Below you'll find an example of manifest conforming to the resource definition that will deploy a Redkey cluster:

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: Redkey
metadata:
  name: redkey-cluster-sample
  namespace: default
  labels:
    app: redis
    team: team-a
spec:
  labels:
    redis-name: redkey-cluster-sample
    app: myapp
  annotations:
    prometheus.io/scrape: "true"
    custom-annotation: custom-value
  primaries: 3
  storage: 3Gi
  image: redislabs/redisgraph:2.8.9
  resources:
    limits:
      cpu: 1
      memory: 2Gi
  config: |
    save ""
    appendonly no
    maxmemory 1400mb
  robin:
    template:
      spec:
        containers:
        - name: robin
          image: redis-metrics:1.0.0
          ports:
          - containerPort: 8080
            name: prometheus
            protocol: TCP
          volumeMounts:
          - mountPath: /opt/conf/configmap
            name: redkey-cluster-sample-robin-config
        volumes:
        - configMap:
            defaultMode: 420
            name: redkey-cluster-sample-robin
          name: redkey-cluster-sample-robin-config
```

### Redis configuration

The `config` item contains the Redis specific configuration attributes that are usually set in the `redis.conf` Redis configuration file (you'll find a complete self-documented [redis.conf](https://redis.io/docs/management/config-file/) file in Redis documentation).

## Cluster vs Standalone

Currently, Redkey operator **only deploys Rdis in cluster mode**.

However, it's possible to set `primaries: 1` to deploy a single instance Redkey cluster. With this configuration, all the slots will be by force allocated to that instance.

## Live reloading

The configuration from `config` item will be placed under a ConfigMap created and managed by the Operator. This ConfigMap shares its name with the Redkey.

The configuration contained in this ConfigMap is the `source of truth` that Redkey operator will use to create and configure the Redkey cluster nodes.

Config reloading is supported by Redkey operator. When ConfigMap contents are updated Redkey operator's reconciliation loop will detect it and update the underlying mapping upgrading the Redkey cluster and restarting the Redis nodes to get the new configuration.

## Resources layout

![Resources layout](../images/redisk8slayout.png)
