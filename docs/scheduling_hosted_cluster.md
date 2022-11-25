# Placing a hosted cluster

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

This placement considers managed hosting clusters the belong to `default` cluster set and selects only one cluster.

With the predicates settings, this placement excludes managed clusters with clusterClaim `hostedclustercount.full.hypershift.openshift.io=true` and without label `purpose: production`. The hypershift addon agent sets the cluster claim to `"true"` when the number of hosted clusters on the hosting cluster has reached 80. With the label selector, you can easily take one or more hosting clusters out of placement consideration by removing the specified label from the managed clusters. Then with the prioritizerPolicy settings, this placement selects a hosting cluster with the least `hostedClustersCount` score which is contained in `AddOnPlacementScore` resource named `hosted-clusters-score` in the hosting cluster's namespace in the hub cluster. The hypershift addon agent constantly updates this `AddOnPlacementScore`. The score is multiplied by the weight and the cluster with the highest score gets selected. 

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

