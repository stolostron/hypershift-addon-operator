# Placing a hosted cluster

## Selecting one hosting cluster with the least number of hosted clusters

As documented [here](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/provision_hypershift_clusters_by_manifestwork.md), you can use a `manifestwork` to create a hosted cluster on a remote managed hosting cluster. If you have multiple remote managed hosting clusters and you want to find a hosting cluster with the least number of hosted clusters in order to evenly distribute hosted clusters among hosting clusters, you can use the following `placement`.

This example assumes that all hosting clusters belong to managed cluster set called `default` and the `placement` is created in `default` namespace.

1. Create a `ManagedClusterSetBinding` to give namespace `default` access to managed cluster set `default`.

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta2
kind: ManagedClusterSetBinding
metadata:
  name: default
  namespace: default
spec:
  clusterSet: default
```

2. Create a `Placement` in `default` namespace. 

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Placement
metadata:
  name: placement1
  namespace: default
spec:
  numberOfClusters: 1 
  clusterSets:
    - default
  predicates:
    - requiredClusterSelector:
        labelSelector:
          matchLabels:
            purpose: production
        claimSelector:
          matchExpressions:
            - key: hostedclustercount.full.hypershift.openshift.io
              operator: In
              values:
                - "false"
  prioritizerPolicy:
    mode: Exact
    configurations:
      - scoreCoordinate:
          type: AddOn
          addOn:
            resourceName: hosted-clusters-score 
            scoreName: hostedClustersCount
        weight: -1
```

This placement considers managed hosting clusters that belong to `default` cluster set and selects only one cluster.

With the predicates settings, this placement excludes managed clusters with clusterClaim `hostedclustercount.full.hypershift.openshift.io=true` and without label `purpose: production`. The hypershift addon agent sets the cluster claim to `"true"` when the number of hosted clusters on the hosting cluster has reached 80. With the label selector, you can easily take one or more hosting clusters out of placement consideration by removing the specified label from the managed clusters. Then with the prioritizerPolicy settings, this placement selects a hosting cluster with the least `hostedClustersCount` score which is contained in `AddOnPlacementScore` resource named `hosted-clusters-score` in the hosting cluster's namespace in the hub cluster. The hypershift addon agent constantly updates this `AddOnPlacementScore`. The score is multiplied by the weight and the cluster with the highest score gets selected. 

This is a sample `AddOnPlacementScore` resource named `hosted-clusters-score` in the hosting cluster's namespace in the hub cluster.

```yaml
apiVersion: cluster.open-cluster-management.io/v1alpha1
kind: AddOnPlacementScore
metadata:
  creationTimestamp: "2022-11-28T19:35:56Z"
  generation: 1
  name: hosted-clusters-score
  namespace: local-cluster
  resourceVersion: "562117"
  uid: cf1a7e80-6e16-4c39-beab-1d6beb0a4475
status:
  conditions:
  - lastTransitionTime: "2022-11-28T19:36:00Z"
    message: Hosted cluster count was updated successfully
    reason: HostedClusterCountUpdated
    status: "True"
    type: HostedClusterCountUpdated
  scores:
  - name: hostedClustersCount
    value: 2
```

This is a sample cluster claim that gets updated in the hosting cluster's `ManagedCluster` resource. The default maximum number of hosted clusters is 80.

```yaml
  - name: hostedclustercount.full.hypershift.openshift.io
    value: "false"
```

3. There should be a `PlacementDecision` in `default` namespace when the placement controller makes a successful decision based on the placement and it should look like this. In this example, the placement selected hosting cluster `cluster1`.

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: PlacementDecision
metadata:
  creationTimestamp: "2022-11-23T22:49:04Z"
  generation: 1
  labels:
    cluster.open-cluster-management.io/placement: placement1
  name: placement1-decision-1
  namespace: default
  ownerReferences:
  - apiVersion: cluster.open-cluster-management.io/v1beta1
    blockOwnerDeletion: true
    controller: true
    kind: Placement
    name: placement1
    uid: 00f74077-6f35-47b9-b5bc-22cd1785618f
  resourceVersion: "13302465"
  uid: 7d0616bc-4037-43a1-b1e5-0ff6b86bea45
status:
  decisions:
  - clusterName: cluster1
    reason: ""
```

## Getting a list of hosting clusters with zero hosted cluster

This is a sample cluster claim that gets updated in the hosting cluster's `ManagedCluster` resource on the hub cluster. The value becomes `"true"` when there is no hosted cluster on the hosting managed cluster. You can use this cluster claim in a `Placement` to get the list of hosting clusters with no hosted cluster.

```yaml
  - name: hostedclustercount.zero.hypershift.openshift.io
    value: "true"
```

This sample placement YAML selects all hosting clusters from the `default` cluster set that has label `purpose=production` and do not have any hosted cluster.

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Placement
metadata:
  name: clusters-with-no-hosted-cluster
  namespace: default
spec:
  clusterSets:
    - default
  predicates:
    - requiredClusterSelector:
        labelSelector:
          matchLabels:
            purpose: production
        claimSelector:
          matchExpressions:
            - key: hostedclustercount.zero.hypershift.openshift.io
              operator: In
              values:
                - "true"
```

## Getting a list of hosting clusters that have not reached the threshold number of hosted clusters

This is a sample cluster claim that gets updated in the hosting cluster's `ManagedCluster` resource on the hub cluster. The value becomes `"true"` when the number of hosted clusters on the hosting managed cluster exceeds (>=) the threshold number. The default threshold is 60 hosted clusters. You can use this cluster claim in a `Placement` to get the list of hosting clusters that either have or have not exceeded the threshold.

```yaml
  - name: hostedclustercount.above.threshold.hypershift.openshift.io
    value: "true"
```

This sample placement YAML selects all hosting clusters from the `default` cluster set that has label `purpose=production` and have exceeded the threshold. You can use operartor `NotIn` or values `"false"` to get different results.

```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Placement
metadata:
  name: clusters-with-no-hosted-cluster
  namespace: default
spec:
  clusterSets:
    - default
  predicates:
    - requiredClusterSelector:
        labelSelector:
          matchLabels:
            purpose: production
        claimSelector:
          matchExpressions:
            - key: hostedclustercount.above.threshold.hypershift.openshift.io
              operator: In
              values:
                - "false"
```

## Overriding the maximum and threshold number of hosted clusters

The default maximum number of hosted clusters is 80 and threshold number is 60. If you want to override these values for all hosting clusters, update the `AddOnDeploymentConfig` named `hypershift-addon-deploy-config` in `multicluster-engine` namespace on the hub cluster.

```bash
$ oc edit addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine
```

Edit the values and save.

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
```

The hypershift addon agent on all hosting clusters will automatically restart with the new settings. If the values are invalid such as invalid numbers or hcMaxNumber < hcThresholdNumber, the change will not be effective and the default values will be enforced.

If you want to have different different settings per hosting cluster, create a different `AddOnDeploymentConfig` in the hosting cluster's namespace on the hub cluster.

```yaml
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: AddOnDeploymentConfig
metadata:
  name: hosting-cluster-1-hypershift-addon-deploy-config
  namespace: hosting-cluster-1
spec:
  customizedVariables:
  - name: hcMaxNumber
    value: "100"
  - name: hcThresholdNumber
    value: "80"
```

And reference it in the hypershift-addon `ManagedClusterAddon`.

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
    name: hosting-cluster-1-hypershift-addon-deploy-config
    namespace: hosting-cluster-1
```

The hypershift addon agent on the hosting cluster will automatically restart with the new settings. If the values are invalid such as invalid numbers or hcMaxNumber < hcThresholdNumber, the change will not be effective and the default values will be enforced.