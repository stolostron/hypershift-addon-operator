#!/bin/bash

set -xv
set -o nounset
set -o pipefail

CLUSTERADM=${CLUSTERADM:-clusteradm}
KUBECTL=${KUBECTL:-kubectl}
_managed_cluster_name="local-cluster"

$CLUSTERADM init --output-join-command-file join.sh --wait
sh -c "$(cat join.sh) $_managed_cluster_name"
$CLUSTERADM accept --clusters $_managed_cluster_name --wait 60
$KUBECTL wait --for=condition=ManagedClusterConditionAvailable managedcluster/$_managed_cluster_name --timeout=60s
$KUBECTL get managedcluster