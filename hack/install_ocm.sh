#!/bin/bash

set -xv
set -o nounset
set -o pipefail

CLUSTERADM=${CLUSTERADM:-clusteradm}
KUBECTL=${KUBECTL:-kubectl}
_managed_cluster_name="local-cluster"

rm -rf clusteradm
echo "############  Cloning clusteradm"
git clone https://github.com/open-cluster-management-io/clusteradm.git

cd clusteradm || {
  printf "cd failed, clusteradm does not exist"
  return 1
}

make install
if [ $? -ne 0 ]; then
 echo "############  Failed to install clusteradm"
 exit 1
fi

$CLUSTERADM init --output-join-command-file join.sh --wait
sh -c "$(cat join.sh) $_managed_cluster_name"
$CLUSTERADM accept --clusters $_managed_cluster_name --wait 60
$KUBECTL wait --for=condition=ManagedClusterConditionAvailable managedcluster/$_managed_cluster_name --timeout=60s
$KUBECTL get managedcluster