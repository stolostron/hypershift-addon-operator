# How to read the metrics dashboard

## Placement score failures
When there is one or more placement score failures, that means the number of hosted clusters could not be collected and scored on a given ManagedCluster for use with Placement.

* This value is incremented by the `hypershift-addon-agent-XXXXX-XXXXX` pod running in the `open-cluster-management-agent-addon` namespace.
* Open the pod Logs, choose the `hypershift-addon-agent` stream, and search for each of the phrases
    ```
    failed to create or update the addOnPlacementScore
    
    failed to update the addOnPlacementScore
    ```
* This will show you one or more failed attempts to calculate Placement and why

## No Data, Unknown or N/A
The cluster is no longer returning telemetry data.

## HyperShift Addon Status
The HyperShift addon agent, running on a ManagedCluster was successfully started
You can find the pod on the managed cluster in the namespace `open-cluster-management-agent-addon` and the pod name `hypershift-addon-agent-XXXXX-XXXXX`

## HyperShift Operator - Degraded
This is the HyperShift controller that manages the hosted control planes. It can be found in the namespace `hypershift` with the name `operator`. When the status shows degraded, check pod or deployment on the ManagedCluster.

## Available vs Total hosted control planes
* Available implies the Hosted Control Plane is accessible
* When clusters are provisioning, the Available Hosted Control Planes guage will be lower then the Total Hosted Control Planes gauge
* If Available is less then total, and no provisioning is occuring, there may be a problem with one of your hosted control plane clusters. Look at the `Fleet` graph for HyperShift Operator and see if one of the ManagedClusters that hosts control plains is degraded

## HyperShift Addon Installs
In the `Fleet` view, use the HyperShift Addon Installs graph to determine if one of the HyperShift addon installs is not proceeding correctly. The failure count resets to zero if there is a success.

