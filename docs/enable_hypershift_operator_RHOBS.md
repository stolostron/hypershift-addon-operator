# RHOBS Monitoring for Hypershift Operator

When RHOBS monitoring is enabled for HyperShift, any ServiceMonitor or PodMonitor resources that are created for the HyperShift operator and inside control plane namespaces created by HyperShift will be created using the `monitoring.rhobs/v1` groupVersion instead of the default `monitoring.coreos.com/v1` groupVersion. This allows the Observability operator to ignore ServiceMonitors/PodMonitors that are not meant to be scraped by it. It also allows the operator to move to newer versions of the API without a dependency on OCP of the management cluster.

When the hypershift-addon managed cluster addon is enabled, it installs the hypershift operator with the RHOBS monitoring disabled by default. 

## Enabling RHOBS Monitoring

RHOBS monitoring for HyperShift operator can be enabled before or after you enable the hypershift-addon managed cluster addon.


1. Apply the `AddOnDeploymentConfig` in the hub cluster.

```yaml
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: AddOnDeploymentConfig
metadata:
  name: enable-rhobs-hypershift
  namespace: multicluster-engine
spec:
  customizedVariables:
  - name: enableRHOBSMonitoring
    value: "true"
```

2. Add the reference to the `AddOnDeploymentConfig` in the hypershift-addon `ManagedClusterAddon` of the hosting cluster in the hub cluster. For example, 

```yaml
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: ManagedClusterAddOn
metadata:
  name: hypershift-addon
  namespace: hosting-cluster-1
spec:
  installNamespace: open-cluster-management-agent-addon
  configs:
  - group: addon.open-cluster-management.io
    resource: addondeploymentconfigs
    name: enable-rhobs-hypershift
    namespace: multicluster-engine
```

## Verification

In the hub cluster, the status section of the hypershift-addon `ManagedClusterAddon` should contain a condition like the following.

```yaml
  configReferences:
  - group: addon.open-cluster-management.io
    lastObservedGeneration: 3
    name: enable-rhobs-hypershift
    namespace: multicluster-engine
    resource: addondeploymentconfigs
```

In the hosting cluster, check the `hypershift-addon-agent` deployment in `open-cluster-management-agent-addon` namespace to ensure that the environment variable `RHOBS_MONITORING` is set to true.

```
        env:
        - name: RHOBS_MONITORING
          value: "true"
```