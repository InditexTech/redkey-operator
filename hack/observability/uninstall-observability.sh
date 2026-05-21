#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)
# SPDX-License-Identifier: Apache-2.0

# uninstall-observability.sh — Removes the observability stack from the cluster.
# Usage: ./hack/observability/uninstall-observability.sh [NAMESPACE]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NAMESPACE="${1:-monitoring}"
RELEASE_NAME="prometheus"

echo "==> Uninstalling observability stack from namespace '${NAMESPACE}'..."

# 1. Delete Grafana dashboards ConfigMap
echo "  Deleting Grafana dashboards ConfigMap..."
kubectl delete configmap redkey-grafana-dashboards -n "${NAMESPACE}" --ignore-not-found

# 2. Delete ServiceMonitors and PodMonitor
echo "  Deleting ServiceMonitors and PodMonitor..."
kubectl delete -f "${SCRIPT_DIR}/servicemonitors.yaml" --ignore-not-found

# 3. Uninstall Helm release
echo "  Uninstalling Helm release '${RELEASE_NAME}'..."
helm uninstall "${RELEASE_NAME}" --namespace "${NAMESPACE}" --wait 2>/dev/null || true

# 4. Clean up CRDs installed by kube-prometheus-stack (optional but thorough)
echo "  Cleaning up Prometheus Operator CRDs..."
kubectl delete crd alertmanagerconfigs.monitoring.coreos.com --ignore-not-found
kubectl delete crd alertmanagers.monitoring.coreos.com --ignore-not-found
kubectl delete crd podmonitors.monitoring.coreos.com --ignore-not-found
kubectl delete crd probes.monitoring.coreos.com --ignore-not-found
kubectl delete crd prometheusagents.monitoring.coreos.com --ignore-not-found
kubectl delete crd prometheuses.monitoring.coreos.com --ignore-not-found
kubectl delete crd prometheusrules.monitoring.coreos.com --ignore-not-found
kubectl delete crd scrapeconfigs.monitoring.coreos.com --ignore-not-found
kubectl delete crd servicemonitors.monitoring.coreos.com --ignore-not-found
kubectl delete crd thanosrulers.monitoring.coreos.com --ignore-not-found

# 5. Delete the namespace
echo "  Deleting namespace '${NAMESPACE}'..."
kubectl delete namespace "${NAMESPACE}" --ignore-not-found

echo ""
echo "==> Observability stack uninstalled successfully. Cluster is clean."
