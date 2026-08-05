<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Helm Charts

![Redkey Operator icon](../docs/images/redkey-icon-color-128.png)

This directory contains Helm charts for deploying the various components of the project. Each subdirectory corresponds to a specific component and includes the necessary templates and configuration files for deployment.

Directory structure:

```ascii
charts/
├── redkey-operator/
│   ├── Chart.yaml
│   ├── values.yaml
│   └── templates/
└── redkey/
    ├── Chart.yaml
    ├── values.yaml
    └── templates/
```

## redkey-operator Chart

Use this chart to deploy the Redkey Operator, which manages Redkey clusters in your Kubernetes environment.

You can install the Redkey Operator by running the following command:

```bash
helm install my-redkey-operator ./charts/redkey-operator
```

## redkey Chart

Use this chart to deploy a Redkey cluster.

You can create a Readkey cluster by executindg the following command:

```bash
helm install my-redkey ./charts/redkey
```

This chart includes a **Helm post-install hook** that checks if the cluster is healthy after installation, waiting for `Condition "Ready"` to be true in the Redkey Custom Resource.

To use the chart with the post-install hook enabled, override the `waitReady.enabled` value to `true` in your `values.yaml` or via the command line:

```bash
# If you want to rollback on failure.
helm install my-redkey ./charts/redkey --set waitReady.enabled=true --rollback-on-failure

# If you want to skip rollback on failure.
helm install my-redkey ./charts/redkey --set waitReady.enabled=true
```
