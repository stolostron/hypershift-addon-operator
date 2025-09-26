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

## Overriding resource utilization baseline measures

Based on [Hosted control plane sizing guidance](https://access.redhat.com/documentation/en-us/red_hat_advanced_cluster_management_for_kubernetes/2.9/html/clusters/cluster_mce_overview#hosted-sizing-guidance), the following baseline measurements are used to calculate the above metrics.

- vCPUs required per HCP: 5
- vCPUs usage per idle HCP: 2.9
- Incremental vCPU usage per 1000 increase in API request rate (QPS) by HCP: 9.0
- Memory required per HCP: 18GB
- Memory usage per idle HCP: 11.1 GB
- Incremental memory usage per 1000 increase in API request rate (QPS) by HCP: 2.5 GB
- Minimum API request rate (QPS), this is th assumed average QPS of all HCPs: 50
- Medium API request rate (QPS), this is th assumed average QPS of all HCPs: 1000
- High API request rate (QPS), this is th assumed average QPS of all HCPs: 2000

This set of baseline measurements are taken from a specific lab environment and can be different in a different cluster. You can override these values.

1. Create a configmap named `hcp-sizing-baseline` in `local-cluster` namespace. You can specify only the ones you want to override.

```yaml
kind: ConfigMap
apiVersion: v1
metadata:
  name: hcp-sizing-baseline
  namespace: local-cluster
data:
  cpuRequestPerHCP: "5"
  idleCPUUsage: "2.9"
  incrementalCPUUsagePer1KQPS: "9.0"
  memoryRequestPerHCP: "18"
  idleMemoryUsage: "11.1"
  incrementalMemUsagePer1KQPS: "2.5"
  minimumQPSPerHCP: "50.0"
  mediumQPSPerHCP: "1000.0"
  highQPSPerHCP: "2000.0"
```

2. Restart the `hypershift-addon-agent` deployment in `open-cluster-management-agent-addon` namespace.

3. Look at the `hypershift-addon-agent` container log of the `hypershift-addon-agent` deployment pod in `open-cluster-management-agent-addon` namespace to verify that the overriden values are picked up for the HCP sizing calculations.

```
2024-01-05T19:41:05.392Z	INFO	agent.agent-reconciler	agent/agent.go:793	setting cpuRequestPerHCP to 5
2024-01-05T19:41:05.392Z	INFO	agent.agent-reconciler	agent/agent.go:802	setting memoryRequestPerHCP to 18
2024-01-05T19:41:05.392Z	INFO	agent.agent-reconciler	agent/agent.go:820	setting incrementalCPUUsagePer1KQPS to 9.0
2024-01-05T19:41:05.392Z	INFO	agent.agent-reconciler	agent/agent.go:829	setting incrementalMemUsagePer1KQPS to 2.5
2024-01-05T19:41:05.392Z	INFO	agent.agent-reconciler	agent/agent.go:838	setting idleCPUUsage to 2.9
2024-01-05T19:41:05.392Z	INFO	agent.agent-reconciler	agent/agent.go:847	setting idleMemoryUsage to 11.1
...

2024-01-05T19:53:54.070Z	INFO	agent.agent-reconciler	agent/hcp_capacity_calculation.go:141	The worker nodes have 12.000000 vCPUs
2024-01-05T19:53:54.070Z	INFO	agent.agent-reconciler	agent/hcp_capacity_calculation.go:142	The worker nodes have 49.173369 GB memory
2024-01-05T19:53:54.070Z	INFO	agent.agent-reconciler	agent/hcp_capacity_calculation.go:143	The maximum number of pods the worker nodes can have is 750.000000
2024-01-05T19:53:54.070Z	INFO	agent.agent-reconciler	agent/hcp_capacity_calculation.go:162	The maximum number of HCPs based on resource requests per HCP is 2
2024-01-05T19:53:54.070Z	INFO	agent.agent-reconciler	agent/hcp_capacity_calculation.go:163	The maximum number of HCPs based on low QPS load per HCP is 2
2024-01-05T19:53:54.070Z	INFO	agent.agent-reconciler	agent/hcp_capacity_calculation.go:164	The maximum number of HCPs based on medium QPS load per HCP is 1
2024-01-05T19:53:54.070Z	INFO	agent.agent-reconciler	agent/hcp_capacity_calculation.go:165	The maximum number of HCPs based on high QPS load per HCP is 0
2024-01-05T19:53:54.070Z	INFO	agent.agent-reconciler	agent/hcp_capacity_calculation.go:166	The maximum number of HCPs based on average QPS of all existing HCPs is 2
```

If there is not overriding configmap `hcp-sizing-baseline`, you should see the following log message.

```
2024-01-05T19:53:54.052Z	ERROR	agent.agent-reconciler	agent/agent.go:788	failed to get configmap from the hub. Setting the HCP sizing baseline with default values.	{"error": "configmaps \"hcp-sizing-baseline\" not found"}
```

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

