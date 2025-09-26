# Running MCE and ACM add-ons in hosted mode

When a cluster is imported to be managed by the hub cluster, the default add-on agents are deployed on worker nodes of the imported cluster. If you import a hypershift hosted cluster to be managed by the hub cluster and you want to deploy the MCE and ACM add-on agents to run on the hosting cluster (hosted mode) along with the hosted cluster's control plane rather than on the worker nodes of the hosted cluster, you can follow the instructions below.

This is the list of add-ons that support hosted mode

- work-manager
- config-policy-controller
- cert-policy-controller

## Configuring the hub cluster

1. Create a global addon deployment configuration for add-ons to be enabled hosted mode.

```
$ oc apply -f - <<EOF
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: AddOnDeploymentConfig
metadata:
  name: addon-hosted-config
  namespace: multicluster-engine
spec:
  customizedVariables:
    - name: managedKubeConfigSecret
      value: external-managed-kubeconfig
EOF
```

2. Update work-manager ClusterManagementAddOns to use the hosted mode configuration.

```
$ oc patch clustermanagementaddon work-manager --type merge -p '{"spec":{"supportedConfigs":[{"defaultConfig":{"name":"addon-hosted-config","namespace":"multicluster-engine"},"group":"addon.open-cluster-management.io","resource":"addondeploymentconfigs"}]}}'
```

3. Update config-policy-controller ClusterManagementAddOns to use the hosted mode configuration.

```
$ oc patch clustermanagementaddon config-policy-controller --type merge -p '{"spec":{"supportedConfigs":[{"defaultConfig":{"name":"addon-hosted-config","namespace":"multicluster-engine"},"group":"addon.open-cluster-management.io","resource":"addondeploymentconfigs"}]}}'
```

4. Update cert-policy-controller ClusterManagementAddOns to use the hosted mode configuration.

```
$ oc patch clustermanagementaddon cert-policy-controller --type merge -p '{"spec":{"supportedConfigs":[{"defaultConfig":{"name":"addon-hosted-config","namespace":"multicluster-engine"},"group":"addon.open-cluster-management.io","resource":"addondeploymentconfigs"}]}}'
```

## Importing a hosted cluster as a managed cluster

Create the following `ManagedCluster` custom resource on the hub cluster to import a hosted cluster and enable the add-ons in hosted mode.

```yaml
apiVersion: cluster.open-cluster-management.io/v1
kind: ManagedCluster
metadata:
  name: <hosted_cluster_name>
  annotations:
    import.open-cluster-management.io/klusterlet-deploy-mode: Hosted
    import.open-cluster-management.io/hosting-cluster-name: <hosting_cluster_name>
    addon.open-cluster-management.io/enable-hosted-mode-addons: "true"
    open-cluster-management/created-via: other
  labels:
    cloud: auto-detect
    cluster.open-cluster-management.io/clusterset: default
    name: my-hosted-cluster-12345
    vendor: auto-detect
spec:
  hubAcceptsClient: true
  leaseDurationSeconds: 60
```

**Note:** The `addon.open-cluster-management.io/enable-hosted-mode-addons: "true"` annotation enables the add-ons in hosted mode for the imported cluster. The add-on deployment will fail if this annotation is used without the previous hub cluster configuration steps.

## Verification

On the hosting cluster, you can see that the add-on agents run in the same klusterlet namespace `klusterlet-<hosted_cluster_name>`.

```
klusterlet-rj-1206a                                config-policy-controller-5c974d89fd-nkmzp                            2/2     Running            0               117m
klusterlet-rj-1206a                                governance-policy-framework-5b6fcf679d-22wdj                         1/1     Running            0               126m
klusterlet-rj-1206a                                klusterlet-addon-workmgr-c78dcf786-hsrsr                             1/1     Running            0               119m
klusterlet-rj-1206a                                klusterlet-rj-1206a-registration-agent-6477fcc7c4-lmj86              1/1     Running            0               126m
klusterlet-rj-1206a                                klusterlet-rj-1206a-work-agent-677598f894-9smjj                      1/1     Running            0               125m

klusterlet-rj-1206b                                config-policy-controller-6b5f64c6d6-9cb8d                            2/2     Running            0               77m
klusterlet-rj-1206b                                governance-policy-framework-c7fc4cd9d-mz5lh                          1/1     Running            0               83m
klusterlet-rj-1206b                                klusterlet-addon-workmgr-74d5d449db-ch2zr                            1/1     Running            0               79m
klusterlet-rj-1206b                                klusterlet-rj-1206b-registration-agent-7b884bc9c-gfl4j               1/1     Running            0               83m
klusterlet-rj-1206b                                klusterlet-rj-1206b-work-agent-f95974b4-rhfn6                        1/1     Running            0               83m
```