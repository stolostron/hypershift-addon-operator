# How to read the metrics dashboard

## No Data, Unknown or N/A
The cluster is no longer returning telemetry data.

## Clusters: HCP, Available & Total 
* HCP - Is the number of Hosted Control Planes that are available (Metrics provided for clusters and the fleet). API is accessible
* Available - Is Clusters with an Available Hosted Control Plane and Ready state Node Pool
* Total - Is the total number of Hosted Control Plane clusters, no matter their state (HostedCluster resources) 

### Reading the gauges
    * When clusters are provisioning, the HCP & Available gauge will be lower then the Total gauge
    * If HCP is less then Available or Available is less then Total, and no provisioning is occuring, there may be a problem with one of your hosted control planes or its node pool(s). Look at the `Fleet` graph for HyperShift Operator and see if one of the ManagedClusters that hosts control planes is degraded

## HyperShift Operator - Degraded
This is the HyperShift Operator controller that manages the hosted control planes. It can be found in the namespace `hypershift` with the name pod name `operator` (two pods). When the status shows degraded, check pod or deployment on the ManagedCluster. There is also a historical graph to see when it was degraded

## HyperShift Addon Status
The HyperShift addon agent, running on a ManagedCluster was successfully started
You can find the pod on the managed cluster in the namespace `open-cluster-management-agent-addon` and the pod name `hypershift-addon-agent-XXXXX-XXXXX`

## HyperShift Addon Installs
In the `Fleet` view, use the HyperShift Addon Installs graph to determine if one of the HyperShift addon installs is not proceeding correctly. The failure count resets to zero if there is a success.

## Placement score failures
When there is one or more placement score failures, that means the number of hosted clusters could not be collected and scored on a given ManagedCluster for use with Placement.

* This value is incremented by the `hypershift-addon-agent-XXXXX-XXXXX` pod running in the `open-cluster-management-agent-addon` namespace.
* Open the pod Logs, choose the `hypershift-addon-agent` stream, and search for each of the phrases
    ```
    failed to create or update the addOnPlacementScore
    
    failed to update the addOnPlacementScore
    ```
* This will show you one or more failed attempts to calculate Placement and why

## Kubeconfig Copy Failures
When there is one or more attempts to copy the Hosted Control Plane's kubeconfig for registration of the new hosted cluster into
ACM, this count will be incremented until it is successful.  Check the logs in the `hypershift-addon-agent-XXXXX-XXXXX` pod in the `open-cluster-management-agent-addon` namespace. You should see errors related to copying the kubeconfig. This is on the Management Cluster (Hosting Cluster)

## HyperShift Addon Available (SLO)
If the HyperShift Addon is detected by the ACM Hub as unavailable or unknown (5min heartbeat) it will be marked false or unknown.  This will affect the SLO values. This status is derived on the ACM hub, you can check addon health with
```
oc get ManagedClusterAddons -A | grep hypershift
```

## SLO (30day & Range)
* 30day - This is whether we met the service level objective in the last 30days
* Range - This is whether we met the service level objective over the grafana board's user defined time range