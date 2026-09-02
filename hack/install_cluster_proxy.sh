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

# Parse CLUSTER_PROXY_TIMEOUT into seconds for polling loops (supports s, m, h, ms).
parse_timeout_seconds() {
  local raw="$1"
  if [[ "${raw}" =~ ^([0-9]+)(ms|s|m|h)$ ]]; then
    local n="${BASH_REMATCH[1]}"
    local unit="${BASH_REMATCH[2]}"
    case "${unit}" in
      ms) echo $(( (n + 999) / 1000 )); return 0 ;;
      s) echo "${n}"; return 0 ;;
      m) echo $(( n * 60 )); return 0 ;;
      h) echo $(( n * 3600 )); return 0 ;;
    esac
    return 1
  fi
  local stripped="${raw%s}"
  if [[ "${stripped}" =~ ^[0-9]+$ ]]; then
    echo "${stripped}"
    return 0
  fi
  return 1
}

if ! timeout_seconds="$(parse_timeout_seconds "${TIMEOUT}")"; then
  echo "ERROR: invalid CLUSTER_PROXY_TIMEOUT '${TIMEOUT}' (use e.g. 300s, 1m, 5m)" >&2
  exit 1
fi
poll_interval=2
KUBECTL_GET="${KUBECTL} --request-timeout=${TIMEOUT}"
poll_iterations=$(( timeout_seconds / poll_interval ))
if [[ poll_iterations -lt 1 ]]; then
  poll_iterations=1
fi

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
for _ in $(seq 1 "${poll_iterations}"); do
  if ${KUBECTL_GET} get svc -n "${NAMESPACE}" cluster-proxy-addon-user >/dev/null 2>&1 && \
     ${KUBECTL_GET} get deploy -n "${NAMESPACE}" cluster-proxy-addon-user >/dev/null 2>&1; then
    break
  fi
  sleep "${poll_interval}"
done
${KUBECTL} get svc -n "${NAMESPACE}" cluster-proxy-addon-user
${KUBECTL} rollout status -n "${NAMESPACE}" deployment/cluster-proxy-addon-user --timeout="${TIMEOUT}"

# The chart installs ClusterManagementAddOn with installStrategy Placements
# (cluster-proxy-placement, clusterSets: [global]). On kind that Placement
# often stays NoManagedClusterMatched (taints on a just-joined local-cluster,
# or global set membership lag), so addon-manager never creates the MCA and
# deletes any we apply. Switch to Manual and create the MCA ourselves.
echo "Switching cluster-proxy ClusterManagementAddOn to Manual installStrategy..."
${KUBECTL} patch clustermanagementaddon cluster-proxy --type merge \
  -p '{"spec":{"installStrategy":{"type":"Manual"}}}'

echo "Ensuring install namespace ${INSTALL_NS}..."
${KUBECTL} create namespace "${INSTALL_NS}" --dry-run=client -o yaml | ${KUBECTL} apply -f -

echo "Ensuring ManagedClusterAddOn cluster-proxy on ${MANAGED_CLUSTER}..."
apply_cluster_proxy_mca() {
  ${KUBECTL} apply -f - <<EOF
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: ManagedClusterAddOn
metadata:
  name: cluster-proxy
  namespace: ${MANAGED_CLUSTER}
spec:
  installNamespace: ${INSTALL_NS}
EOF
}
apply_cluster_proxy_mca

echo "Waiting for ManagedClusterAddOn cluster-proxy Available on ${MANAGED_CLUSTER}..."
available=""
for _ in $(seq 1 "${poll_iterations}"); do
  available=$(${KUBECTL_GET} get managedclusteraddon cluster-proxy -n "${MANAGED_CLUSTER}" \
    -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' 2>/dev/null || true)
  if [[ "${available}" == "True" ]]; then
    break
  fi
  # Recreate if addon-manager still deletes it before the Manual patch settles.
  apply_cluster_proxy_mca >/dev/null
  sleep "${poll_interval}"
done
if [[ "${available}" != "True" ]]; then
  echo "ERROR: cluster-proxy ManagedClusterAddOn not Available on ${MANAGED_CLUSTER}" >&2
  ${KUBECTL_GET} get clustermanagementaddon cluster-proxy -o yaml || true
  ${KUBECTL_GET} get managedclusteraddon -A || true
  ${KUBECTL_GET} get placement,placementdecision -n "${NAMESPACE}" || true
  ${KUBECTL_GET} get managedcluster "${MANAGED_CLUSTER}" -o yaml || true
  exit 1
fi

echo "cluster-proxy ready (namespace=${NAMESPACE}, cluster=${MANAGED_CLUSTER})"
${KUBECTL} get managedclusteraddon -n "${MANAGED_CLUSTER}" cluster-proxy
${KUBECTL} get svc -n "${NAMESPACE}" cluster-proxy-addon-user
