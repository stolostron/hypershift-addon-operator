# Cluster Capacity for Hosting Hosted Control Planes

Many factors, including hosted cluster workload and worker node count, affect how many hosted clusters can fit within a certain number of control-plane nodes. Refer to [Hosted control plane sizing guidance](https://access.redhat.com/documentation/en-us/red_hat_advanced_cluster_management_for_kubernetes/2.9/html/clusters/cluster_mce_overview#hosted-sizing-guidance)

When you enable the `hypershift-addon` managed cluster addon, the metric service monitoring is configured by default so that OCP monitoring can scrape metrics data from the hypershift addon agent. This document describes the instrumented hosted control plane capacity metrics based on the [Hosted control plane sizing guidance](https://access.redhat.com/documentation/en-us/red_hat_advanced_cluster_management_for_kubernetes/2.9/html/clusters/cluster_mce_overview#hosted-sizing-guidance)
 and how to disable the metric service monitoring.

## Metrics

| **Name** | **Description** | 
| --- | --- |
| *mce_hs_addon_request_based_hcp_capacity_gauge* | Estimated maximum number of hosted control planes the cluster can host based on a highly available HCP resource request. |
| *mce_hs_addon_low_qps_based_hcp_capacity_gauge* | Estimated maximum number of hosted control planes the cluster can host if all hosted control planes make around 50 QPS (low load) to the clusters Kube API server. |
| *mce_hs_addon_medium_qps_based_hcp_capacity_gauge* | Estimated maximum number of hosted control planes the cluster can host if all hosted control planes make around 1000 QPS (medium load) to the clusters Kube API server. |
| *mce_hs_addon_high_qps_based_hcp_capacity_gauge* | Estimated maximum number of hosted control planes the cluster can host if all hosted control planes make around 2000 QPS (high load) to the clusters Kube API server. |
| *mce_hs_addon_average_qps_based_hcp_capacity_gauge* | Estimated maximum number of hosted control planes the cluster can host based on the existing hosted control planes' average QPS. If there is no existing active hosted control plane, low QPS is assumed. |

## Disabling metric service monitoring configuration

1. Log into the hub cluster.

2. Edit `hypershift-addon-deploy-config` AddOnDeploymentConfig.

```bash
$ oc edit addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine
```

3. Add `disableMetrics=true` customized variable while leaving other customized variables intact and save. This will automatically disable the metric service monitoring configuration from existing hypershift-addon managed cluster addons and newly enabled hypershift-addon managed cluster addons.

```yaml
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: AddOnDeploymentConfig
metadata:
  name: hypershift-addon-deploy-config
  namespace: multicluster-engine
spec:
  customizedVariables:
  - name: hcMaxNumber
    value: "80"
  - name: hcThresholdNumber
    value: "60"
  - name: disableMetrics
    value: "true"
```

