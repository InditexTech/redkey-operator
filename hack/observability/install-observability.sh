#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)
# SPDX-License-Identifier: Apache-2.0

# install-observability.sh — Deploys Prometheus + Grafana + dashboards into the Kind dev cluster.
# Usage: ./hack/observability/install-observability.sh [NAMESPACE]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NAMESPACE="${1:-monitoring}"
RELEASE_NAME="prometheus"
CHART_REPO="https://prometheus-community.github.io/helm-charts"
CHART_NAME="prometheus-community/kube-prometheus-stack"

echo "==> Installing observability stack into namespace '${NAMESPACE}'..."

# 1. Add Helm repo
echo "  Adding prometheus-community Helm repo..."
helm repo add prometheus-community "${CHART_REPO}" --force-update 2>/dev/null
helm repo update prometheus-community

# 2. Create namespace if needed
kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

# 3. Install/upgrade kube-prometheus-stack
echo "  Installing kube-prometheus-stack..."
helm upgrade --install "${RELEASE_NAME}" "${CHART_NAME}" \
  --namespace "${NAMESPACE}" \
  --values "${SCRIPT_DIR}/values-prometheus-stack.yaml" \
  --wait --timeout 5m

# 4. Apply ServiceMonitors and PodMonitor
echo "  Applying ServiceMonitors and PodMonitor..."
kubectl apply -f "${SCRIPT_DIR}/servicemonitors.yaml"

# 5. Generate and apply Grafana dashboards ConfigMap
echo "  Creating Grafana dashboards ConfigMap..."
OPERATOR_DASHBOARD=$(cat "${SCRIPT_DIR}/dashboards/redkey-operator.json")
ROBIN_DASHBOARD=$(cat "${SCRIPT_DIR}/dashboards/redkey-robin.json")

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: redkey-grafana-dashboards
  namespace: ${NAMESPACE}
  labels:
    grafana_dashboard: "1"
  annotations:
    grafana_folder: "Redkey"
data:
  redkey-operator.json: |
$(echo "${OPERATOR_DASHBOARD}" | sed 's/^/    /')
  redkey-robin.json: |
$(echo "${ROBIN_DASHBOARD}" | sed 's/^/    /')
EOF

# 6. Star the Redkey dashboards in Grafana via API
echo "  Starring Redkey dashboards in Grafana..."
GRAFANA_USER=$(kubectl get secret -n "${NAMESPACE}" "${RELEASE_NAME}-grafana" \
  -o jsonpath='{.data.admin-user}' | base64 -d)
GRAFANA_PASS=$(kubectl get secret -n "${NAMESPACE}" "${RELEASE_NAME}-grafana" \
  -o jsonpath='{.data.admin-password}' | base64 -d)

# Start a temporary port-forward in background
kubectl -n "${NAMESPACE}" port-forward "svc/${RELEASE_NAME}-grafana" 3999:80 &>/dev/null &
PF_PID=$!
trap "kill ${PF_PID} 2>/dev/null || true" EXIT

# Wait for Grafana to respond and for the sidecar to provision the dashboards
GRAFANA_URL="http://localhost:3999"
for i in $(seq 1 30); do
  if curl -sf -u "${GRAFANA_USER}:${GRAFANA_PASS}" "${GRAFANA_URL}/api/health" &>/dev/null; then
    break
  fi
  sleep 2
done

# Wait up to 60 s for the sidecar to write and register the dashboards
for uid in redkey-operator redkey-robin; do
  for i in $(seq 1 30); do
    STATUS=$(curl -sf -u "${GRAFANA_USER}:${GRAFANA_PASS}" \
      "${GRAFANA_URL}/api/dashboards/uid/${uid}" \
      -o /dev/null -w "%{http_code}" 2>/dev/null || echo "000")
    if [ "${STATUS}" = "200" ]; then
      break
    fi
    sleep 2
  done
done

# Star the dashboards for the admin user (Grafana 10+ supports uid-based endpoint)
for uid in redkey-operator redkey-robin; do
  STATUS=$(curl -sf -X POST -u "${GRAFANA_USER}:${GRAFANA_PASS}" \
    "${GRAFANA_URL}/api/user/stars/dashboard/uid/${uid}" \
    -o /dev/null -w "%{http_code}" 2>/dev/null || echo "000")
  if [ "${STATUS}" = "200" ]; then
    echo "    ★ Starred dashboard: ${uid}"
  else
    echo "    ⚠ Could not star dashboard: ${uid} (HTTP ${STATUS})"
  fi
done

kill "${PF_PID}" 2>/dev/null || true
trap - EXIT

echo ""
echo "==> Observability stack installed successfully!"
echo ""
echo "  Grafana is available via NodePort 30300 on any cluster node."
echo "  For Kind, access it at: http://localhost:30300"
echo "  Credentials: admin / redkey"
echo ""
echo "  Prometheus is available in-cluster at:"
echo "    http://${RELEASE_NAME}-kube-prometheus-prometheus.${NAMESPACE}:9090"
echo ""
echo "  To port-forward Grafana:"
echo "    kubectl -n ${NAMESPACE} port-forward svc/${RELEASE_NAME}-grafana 3000:80"
echo "    Then open http://localhost:3000"
echo ""
echo "  To port-forward Prometheus:"
echo "    kubectl -n ${NAMESPACE} port-forward svc/${RELEASE_NAME}-kube-prometheus-prometheus 9090:9090"
echo "    Then open http://localhost:9090"
