#!/bin/bash
# Install the OCM cluster-proxy addon for kind e2e.
#
# Docs: https://open-cluster-management.io/docs/getting-started/integration/cluster-proxy/
#
#   helm install \
#     -n open-cluster-management-addon --create-namespace \
#     cluster-proxy ocm/cluster-proxy
#
# enableServiceProxy / userServer are required so the HCP proxy can reach
# spoke kube-apiservers over HTTPS at cluster-proxy-addon-user:9092.

set -euo pipefail

KUBECTL=${KUBECTL:-kubectl}
HELM=${HELM:-helm}
NAMESPACE=${CLUSTER_PROXY_NAMESPACE:-open-cluster-management-addon}
RELEASE=${CLUSTER_PROXY_RELEASE:-cluster-proxy}
MANAGED_CLUSTER=${MANAGED_CLUSTER_NAME:-local-cluster}
TIMEOUT=${CLUSTER_PROXY_TIMEOUT:-300s}
INSTALL_NS=${CLUSTER_PROXY_AGENT_NAMESPACE:-open-cluster-management-agent-addon}

if ! command -v "${HELM}" >/dev/null 2>&1; then
  echo "ERROR: helm is required to install OCM cluster-proxy" >&2
  exit 1
fi

${HELM} repo add ocm https://open-cluster-management.io/helm-charts/ 2>/dev/null || true
${HELM} repo update ocm

# PortForward entrypoint is the kind-friendly default when entrypointAddress
# is unset (see ManagedProxyConfiguration chart template).
if ${HELM} status "${RELEASE}" -n "${NAMESPACE}" >/dev/null 2>&1; then
  echo "cluster-proxy release ${RELEASE} already installed in ${NAMESPACE}"
else
  ${HELM} install "${RELEASE}" ocm/cluster-proxy \
    -n "${NAMESPACE}" --create-namespace \
    --set enableServiceProxy=true \
    --set userServer.enabled=true \
    --wait --timeout "${TIMEOUT}"
fi

echo "Waiting for cluster-proxy-addon-user Service and Deployment..."
for _ in $(seq 1 150); do
  if ${KUBECTL} get svc -n "${NAMESPACE}" cluster-proxy-addon-user >/dev/null 2>&1 && \
     ${KUBECTL} get deploy -n "${NAMESPACE}" cluster-proxy-addon-user >/dev/null 2>&1; then
    break
  fi
  sleep 2
done
${KUBECTL} get svc -n "${NAMESPACE}" cluster-proxy-addon-user
${KUBECTL} rollout status -n "${NAMESPACE}" deployment/cluster-proxy-addon-user --timeout="${TIMEOUT}"

# Chart installStrategy is Placement-based; addon-manager creates the
# ManagedClusterAddOn asynchronously. kubectl wait fails immediately with
# NotFound if the object is missing, which races right after OCM join.
# Ensure the MCA exists (idempotent) before waiting for Available.
echo "Ensuring ManagedClusterAddOn cluster-proxy on ${MANAGED_CLUSTER}..."
${KUBECTL} apply -f - <<EOF
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: ManagedClusterAddOn
metadata:
  name: cluster-proxy
  namespace: ${MANAGED_CLUSTER}
spec:
  installNamespace: ${INSTALL_NS}
EOF

echo "Waiting for ManagedClusterAddOn cluster-proxy Available on ${MANAGED_CLUSTER}..."
${KUBECTL} wait --for=condition=Available=True \
  "managedclusteraddon/cluster-proxy" \
  -n "${MANAGED_CLUSTER}" \
  --timeout="${TIMEOUT}"

echo "cluster-proxy ready (namespace=${NAMESPACE}, cluster=${MANAGED_CLUSTER})"
${KUBECTL} get managedclusteraddon -n "${MANAGED_CLUSTER}" cluster-proxy
${KUBECTL} get svc -n "${NAMESPACE}" cluster-proxy-addon-user
