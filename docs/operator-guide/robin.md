<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Redkey Cluster Robin

The Redkey Cluster CRD provides the field `spec.robin` to deploy the Redkey Cluster Robin, a faithful partner who assists the operator in the dangerous Gotham.
Robin is designed to help the Operator (Batman) in its duties, in particular:

- Provide Redkey Cluster prometheus metrics
- FUTURE WORK

The Operator deploys a Deployment and a ConfigMap for Robin given the configuration provided in `spec.robin` for each Redkey Cluster, if configured. The operator is responsible of reconcile any addition, update or delete in the `spec.robin` of a RedkeyCluster.

## How to deploy Robin

Robin deployment can be configured in `spec.robin.template`. This field is an object representing a [PodSpecTemplate](https://github.com/kubernetes/kubernetes/blob/v1.32.2/staging/src/k8s.io/api/core/v1/types.go#L5050). The template is then used by the Redkey Operator to create, update or delete a Deployment with Robin, whose name is `<RedkeyClusterName>-robin`.

Robin connects to all the nodes of the Redkey Cluster using port 6379 and the K8s Redis Pod domain name (e.g.: redkeycluster-sample-0.redis-cluster-sample). Therefore, a DNS resolving that name to the Pod IP is needed for Robin to work.

### Example

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: RedkeyCluster
...
spec:
  ...
  robin:
    template:
      ...
      spec:
        containers:
          - image: 'redkey-robin:0.0.1'
            name: robin
            imagePullPolicy: Always
            ports:
              - containerPort: 8080
                name: prometheus
                protocol: TCP
            volumeMounts:
              - mountPath: /opt/conf/configmap
                name: redkeycluster-sample-robin-config
        volumes:
          - configMap:
              defaultMode: 420
              name: redkeycluster-sample-robin
            name: redkeycluster-sample-robin-config
```

## How to configure Robin

Robin configuration can be included in `spec.robin.config`. This field is an string whose content is included in the key `application-configmap.yml` of the ConfigMap `<RedkeyClusterName>-robin`.
The content is expected to be a valid YAML with several fields which can be seen in [Configuration fields](#configuration-fields) section

The Redkey Operator applies the MD5 algorithm to the `spec.robin.config` content and adds the result in the `checksum/config` annotation of the Robin Deployment template. This way, any change in the configuration content will trigger a Robin POD recreation, which will have always the latest content applied to the RedkeyCluster object.

### Configuration fields

The expected fields of the `spec.robin.config` YAML are:

- `metadata`: object with the labels that will be added to the Prometheus metrics
- `redis`: object with the cluster configuration:
  - `operator`:
    - `collection_interval_seconds` (int): sleep time in seconds between two consecutive metrics polling iterations.
  - `cluster`:
    - `replicas` (int): number of nodes of the Redkey Cluster. Used to infer the Redis node domain name.
    - `name` (string): Redkey Cluster name.
    - `namespace` (string): K8s namespace of the Redkey Cluster.
    - `health_probe_interval_seconds` (int):
    - `healing_time_seconds` (int):
    - `max_retries` (int): maximum retries to connect to a Redis node.
    - `back_off` (time.Duration): sleep time between two consecutive attempts to connect to a Redis node.
  - `metrics`:
    - `version`: Redis metrics version.
    - `redis_info_keys`: Redis info keys that are asked to each Redis node and are exported in the Prometheus metrics.

### Example

```yaml
apiVersion: redkey.inditex.dev/v1beta1
kind: RedkeyCluster
...
spec:
  ...
  robin:
    ...
    config: |
      metadata:
        application: showpaas
        version: "7.2.4"
        environment: des
        tenant: global
        domain: swdelivery
        slot: sample
        layer: middleware-redis
        namespace: redkey-operator
        platformid: "meccanoarteixo2"
        service: "showpaas"
      redis:
        operator:
          collection_interval_seconds: 60
        cluster:
          replicas: 1
          name: "redkeycluster-sample"
          namespace: redkey-operator
          health_probe_interval_seconds: 60
          healing_time_seconds: 60
          max_retries: 2
          back_off: 10s
        metrics:
          version: 0.10.2.0
          redis_info_keys:
            - keyspace_hits
            - evicted_keys
            - connected_clients
            - total_commands_processed
            - keyspace_misses
            - expired_keys
            - redis_version
            - used_memory_rss
            - maxmemory
            - used_cpu_sys
            - used_cpu_sys_children
            - used_cpu_user
            - used_cpu_user_children
            - total_net_input_bytes
            - total_net_output_bytes
            - aof_base_size
            - aof_current_size
            - mem_aof_buffer
```

## How to develop Robin

Please refer to [Redkey Robin](https://github.com/InditexTech/redkeyrobin/docs/developer-guide.md) section of the Operador Development Guide to know how to develop, build and deploy Robin for development and debugging purposes.

## Authentication

Robin authenticates to Redis using a password stored in a Kubernetes Secret. The secret name is configured declaratively in `spec.auth.secret` of the `RedkeyCluster` — no CLI flags or environment variables are required.

For full details on creating the Secret, configuring auth, password rotation, and troubleshooting, see the [Redis Authentication](authentication.md) guide.

## Dynamic Configuration

All Robin operational settings (`reconciler.intervalSeconds`, `reconciler.intervalOnErrorSeconds`, `reconciler.intervalOnWaitSeconds`, `metrics.collectionIntervalSeconds`, `metrics.redisInfoKeys`, `metrics.metricsLabels`, connection retries, and the auth secret reference) are applied **at runtime without a Pod restart**. Robin continuously polls `RedkeyClusterConfig` resources and updates its internal state when new configurations are detected. If a reconciler interval is omitted from the CR, Robin keeps its startup value; for `intervalOnWaitSeconds` that startup default is 10 seconds unless overridden via CLI. Changing `metricsLabels` values is applied on the next metrics cycle; adding or removing label keys resets only Robin's RedKey metrics registry so Prometheus can ingest the new label schema.

For a complete explanation of the hot-reload mechanism, propagation timing, and examples, see the [Dynamic Configuration](dynamic-configuration.md) guide.
