#!/bin/bash
# Install OCM hub + register the kind cluster as local-cluster.
#
# Avoids flaky clusteradm --wait ("unexpected watch event received") by:
#   1. Enabling ManagedClusterAutoApproval (no slow accept --wait)
#   2. Joining with --force-internal-endpoint-lookup (kind pods can't reach 127.0.0.1 host port)
#   3. Polling readiness with kubectl wait instead of clusteradm watches

set -xv
set -o nounset
set -o pipefail

CLUSTERADM=${CLUSTERADM:-clusteradm}
KUBECTL=${KUBECTL:-kubectl}
_managed_cluster_name="local-cluster"
JOIN_TIMEOUT=${JOIN_TIMEOUT:-180s}

# Auto-approve CSRs so we can skip `clusteradm accept --wait`.
# Tolerate the known flaky "unexpected watch event received" from clusteradm --wait.
$CLUSTERADM init \
  --feature-gates=ManagedClusterAutoApproval=true \
  --output-join-command-file join.sh \
  --wait || true

# Confirm hub pieces exist even if clusteradm's wait flaked.
$KUBECTL wait --for=condition=Available deployment/cluster-manager \
  -n open-cluster-management --timeout=120s

# Parse join credentials from join.sh (do not reuse its flaky --wait).
if [[ ! -f join.sh ]]; then
  echo "ERROR: join.sh was not created by clusteradm init" >&2
  exit 1
fi
hub_token=$(grep -oE -- '--hub-token[[:space:]]+[^[:space:]]+' join.sh | awk '{print $2}' | head -1)
hub_apiserver=$(grep -oE -- '--hub-apiserver[[:space:]]+[^[:space:]]+' join.sh | awk '{print $2}' | head -1)
if [[ -z "${hub_token}" || -z "${hub_apiserver}" ]]; then
  echo "ERROR: failed to parse hub token/apiserver from join.sh" >&2
  cat join.sh >&2
  exit 1
fi

# Join without --wait (flaky watch); force in-cluster hub endpoint for kind.
$CLUSTERADM join \
  --hub-token "${hub_token}" \
  --hub-apiserver "${hub_apiserver}" \
  --cluster-name "${_managed_cluster_name}" \
  --force-internal-endpoint-lookup

# Fallback if auto-approval did not create/accept the ManagedCluster yet.
if ! $KUBECTL get managedcluster "${_managed_cluster_name}" >/dev/null 2>&1; then
  for _ in $(seq 1 30); do
    if $KUBECTL get csr -o name 2>/dev/null | grep -q "${_managed_cluster_name}"; then
      break
    fi
    sleep 2
  done
  $CLUSTERADM accept --clusters "${_managed_cluster_name}" || true
fi

$KUBECTL wait --for=condition=HubAcceptedManagedCluster \
  "managedcluster/${_managed_cluster_name}" --timeout="${JOIN_TIMEOUT}"
$KUBECTL wait --for=condition=ManagedClusterConditionAvailable \
  "managedcluster/${_managed_cluster_name}" --timeout="${JOIN_TIMEOUT}"
$KUBECTL get managedcluster
