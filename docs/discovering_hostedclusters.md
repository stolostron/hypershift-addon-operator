# Discovering Hosted Clusters from MCE Clusters

You have one or more MCE clusters hosting many hosted clusters. How do you bring these hosted clusters into an ACM hub cluster to manage them using ACM's management tools like application management and security policies? This document explains how you can import existing MCE clusters into an ACM hub cluster to have those hosted clusters automatically discovered and imported as managed clusters.

## MCE as a hosting cluster managed by ACM

Clusters in this topology:

- ACM cluster as a hub cluster
- One or more MCE hosting clusters as managed clusters (Having ACM on these clusters it not supported)

<img width="532" alt="image" src="https://github.com/rokej/hypershift-addon-operator/assets/41969005/55b83d78-7172-4434-bef5-9deec75c23f5">

In this topology, the managed clusters are MCE clusters. One of the reasons why you want managed clusters to be MCE clusters instead of vanilla OCP is that MCE installs other operators like hive and BareMetal infrastructure operators that you can take advantage of.

### Scaling option

Since the hosted control planes run on the managed MCE clusters' nodes, the number of hosted control planes the cluster can host is determined by the resource availability of managed MCE clusters' nodes as well as the number of managed MCE clusters. You can add more nodes or managed clusters to host more hosted control planes.

### Importing an MCE cluster into ACM

#### Configurations before import

MCE has a self-managed cluster called `local-cluster` and the default addons are enabled for this managed cluster.

```
% oc get managedclusteraddon -n local-cluster
NAME                     AVAILABLE   DEGRADED   PROGRESSING
cluster-proxy            True                   False
hypershift-addon         True        False      False
managed-serviceaccount   True                   False
work-manager             True                   False
```

```
% oc get deployment -n open-cluster-management-agent-addon
NAME                                 READY   UP-TO-DATE   AVAILABLE   AGE
cluster-proxy-proxy-agent            1/1     1            1           25h
hypershift-addon-agent               1/1     1            1           25h
klusterlet-addon-workmgr             1/1     1            1           25h
managed-serviceaccount-addon-agent   1/1     1            1           25h
```

When this MCE is imported into ACM, ACM enables the same set of addons to manage the MCE. We want the ACM's addons to be installed in a different namespace in MCE so that MCE can still self-manage with the `local-cluster` addons while MCE can be managed by ACM at the same time.

Log into ACM.

Create this `AddOnDeploymentConfig` resource to specify a different addon installation namespace.

```
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: AddOnDeploymentConfig
metadata:
  name: addon-ns-config
  namespace: multicluster-engine
spec:
  agentInstallNamespace: open-cluster-management-agent-addon-discovery
```

Update the existing `ClusterManagementAddOn` resources for these addons so that the addons pick up the new installation namespace from the `AddOnDeploymentConfig` resource we created.

Before the update, `ClusterManagementAddOn` for `work-manager` addon should look like this.

```
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: ClusterManagementAddOn
metadata:
  name: work-manager
spec:
  addOnMeta:
    displayName: work-manager
  installStrategy:
    placements:
    - name: global
      namespace: open-cluster-management-global-set
      rolloutStrategy:
        type: All
    type: Placements
```


Update `ClusterManagementAddOn` for `work-manager` addon to add a reference to the `AddOnDeploymentConfig` resource created in the previous step.

```
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: ClusterManagementAddOn
metadata:
  name: work-manager
spec:
  addOnMeta:
    displayName: work-manager
  installStrategy:
    placements:
    - name: global
      namespace: open-cluster-management-global-set
      rolloutStrategy:
        type: All
      configs:
      - group: addon.open-cluster-management.io
        name: addon-ns-config
        namespace: multicluster-engine
        resource: addondeploymentconfigs
    type: Placements
```

Do the same update for `managed-serviceaccount` addon.

```
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: ClusterManagementAddOn
metadata:
  name: managed-serviceaccount
spec:
  addOnMeta:
    displayName: managed-serviceaccount
  installStrategy:
    placements:
    - name: global
      namespace: open-cluster-management-global-set
      rolloutStrategy:
        type: All
      configs:
      - group: addon.open-cluster-management.io
        name: addon-ns-config
        namespace: multicluster-engine
        resource: addondeploymentconfigs
    type: Placements
```

Once you make these changes in ACM, you will notice that these addons for ACM's `local-cluster` and all other managed clusters are re-installed into the specified namespace.

```
% oc get deployment -n open-cluster-management-agent-addon-discovery
NAME                                 READY   UP-TO-DATE   AVAILABLE   AGE
klusterlet-addon-workmgr             1/1     1            1           24h
managed-serviceaccount-addon-agent   1/1     1            1           24h
```

We are going to create a `KlusterletConfig` resource that is going to be used by `ManagedCluster` resources to import MCEs. When a `ManagedCluster` references this `KlusterletConfig` resource, the managed cluster klusterlet gets installed in the namepspace that is specified in the `KlusterletConfig`. This allows the importing ACM's klusterlet to be installed in a different namespace than the MCE's klusterlet for its self-managed local-cluster managed cluster in the MCE cluster.

```
kind: KlusterletConfig
apiVersion: config.open-cluster-management.io/v1alpha1
metadata:
  name: mce-import-klusterlet-config
spec:
  installMode:
    type: noOperator
    noOperator:
       postfix: mce-import
```

#### Importing MCE 

In ACM cluster, create a `ManagedCluster` resource manually to start importing an MCE cluster. For example, create the following resource to import an MCE and name the managed cluster `mce-a`.

```
apiVersion: cluster.open-cluster-management.io/v1
kind: ManagedCluster
metadata:
  annotations:
    agent.open-cluster-management.io/klusterlet-config: mce-import-klusterlet-config
  name: mce-a
spec:
  hubAcceptsClient: true
  leaseDurationSeconds: 60
```

Note that it has the annotation `agent.open-cluster-management.io/klusterlet-config: mce-import-klusterlet-config`. This annotation references the `KlusterletConfig` resource that was created in the previous step to install the ACM's klusterlet into a different namespace in MCE.

Now the managed cluster and its namespace should be created in the ACM cluster. 

Follow https://access.redhat.com/documentation/en-us/red_hat_advanced_cluster_management_for_kubernetes/2.10/html-single/clusters/index#importing-clusters-auto-import-secret to create the auto-import secret to complete the MCE auto-import process. Once the auto import secret is created in the MCE managed cluster namespace in the ACM cluster, the managed cluster gets registered and you should see the managed cluster status like this.


```
% oc get managedcluster
NAME            HUB ACCEPTED   MANAGED CLUSTER URLS                                         JOINED   AVAILABLE   AGE
local-cluster   true           https://api.acm-hub-hs-aws.dev09.red-chesterfield.com:6443   True     True        44h
mce-a           true           https://api.clc-hs-mce-a.dev09.red-chesterfield.com:6443     True     True        27s
```

Important: DO NOT enable any other ACM addons for the imported MCE.

## Enabling the hypershift addon for MCE

After all MCEs are imported into ACM, you need to enable the hypershift addon for those managed MCE clusters. Run the following commands in the ACM hub cluster to enable it. Similar to how the default addon are intalled into a different namespace in the previous section, these commands are for installing the hypershift addon into a different namespace in MCE as well so that the hypershift addon agent for MCE's local-cluster and the agent for ACM can co-exist in MCE. This requires `oc` and `clusteradm` CLIs.

```
% oc patch addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine --type=merge -p '{"spec":{"agentInstallNamespace":"open-cluster-management-agent-addon-discovery"}}'

% oc patch addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine --type=merge -p '{"spec":{"customizedVariables":[{"name":"disableMetrics","value": "true"}]}}'

% clusteradm addon enable --names hypershift-addon --clusters <MCE managed cluster names>
```

Replace <MCE managed cluster names> with the actual managed cluster names for MCE, comma separated. You can get the MCE managed cluster names by running the following command in ACM.

```
% oc get managedcluster
```

Log into MCE clusters and verify that the hypershift addon is installed in the specified namespace.

```
% oc get deployment -n open-cluster-management-agent-addon-discovery
NAME                                 READY   UP-TO-DATE   AVAILABLE   AGE
klusterlet-addon-workmgr             1/1     1            1           24h
hypershift-addon-agent               1/1     1            1           24h
managed-serviceaccount-addon-agent   1/1     1            1           24h
```

This hypershift addon deployed by ACM acts as a discovery agent that discovers hosted clusters from MCE and create corresponding `DiscoveredCluster` CR in the MCE's managed cluster namespace in the ACM hub cluster when the hosted cluster's kube API server becomes available. Log into ACM hub console, navigate to All Clusters -> Infrastructure -> Clusters and `Discovered clusters` tab to view all discovered hosted clusters from MCE with type `MultiClusterEngineHCP`. 


<img width="1641" alt="image" src="https://github.com/rokej/hypershift-addon-operator/assets/41969005/b2739520-413a-4cd8-8a59-29d2e6837967">



## Auto-importing the discovered hosted clusters

A `DiscoveredCluster` CR that is created by ACM's hypershift addon agent looks like this.

```
apiVersion: discovery.open-cluster-management.io/v1
kind: DiscoveredCluster
metadata:
  creationTimestamp: "2024-05-30T23:05:39Z"
  generation: 1
  labels:
    hypershift.open-cluster-management.io/hc-name: hosted-cluster-1
    hypershift.open-cluster-management.io/hc-namespace: clusters
  name: hosted-cluster-1
  namespace: mce-1
  resourceVersion: "1740725"
  uid: b4c36dca-a0c4-49f9-9673-f561e601d837
spec:
  apiUrl: https://a43e6fe6dcef244f8b72c30426fb6ae3-ea3fec7b113c88da.elb.us-west-1.amazonaws.com:6443
  cloudProvider: aws
  creationTimestamp: "2024-05-30T23:02:45Z"
  credential: {}
  displayName: mce-1-hosted-cluster-1
  importAsManagedCluster: false
  isManagedCluster: false
  name: hosted-cluster-1
  openshiftVersion: 0.0.0
  status: Active
  type: MultiClusterEngineHCP
```

Setting the `spec.importAsManagedCluster` to `true` triggers ACM's discovery operator to start the auto-importing process and soon, you will see a managed cluster that is named the same as `spec.displayName` in the `DiscoveredCluster`. 

<img width="1645" alt="image" src="https://github.com/rokej/hypershift-addon-operator/assets/41969005/68fe947a-702c-4e24-a57c-2719b71eea5a">

Setting `spec.importAsManagedCluster` to `true` can be automated by applying the following policy to ACM. This policy ensures that a DiscoveredCluster with type `MultiClusterEngineHCP` is set for auto-importing.
 
```
apiVersion: policy.open-cluster-management.io/v1
kind: Policy
metadata:
  name: policy-mce-hcp-autoimport
  namespace: open-cluster-management-global-set
  annotations:
    policy.open-cluster-management.io/standards: NIST SP 800-53
    policy.open-cluster-management.io/categories: CM Configuration Management
    policy.open-cluster-management.io/controls: CM-2 Baseline Configuration
    policy.open-cluster-management.io/description: Discovered clusters that are of
      type MultiClusterEngineHCP can be automatically imported into ACM as managed clusters.
      This policy configure those discovered clusters so they are automatically imported. 
      Fine tuning MultiClusterEngineHCP clusters to be automatically imported
      can be done by configure filters at the configMap or add annotation to the discoverd cluster.
spec:
  # Remove the default remediation below to enforce the policies.
  # remediationAction: inform
  disabled: false
  policy-templates:
    - objectDefinition:
        apiVersion: policy.open-cluster-management.io/v1
        kind: ConfigurationPolicy
        metadata:
          name: mce-hcp-autoimport-config
        spec:
          object-templates:
            - complianceType: musthave
              objectDefinition:
                apiVersion: v1
                kind: ConfigMap
                metadata:
                  name: discovery-config
                  namespace: open-cluster-management-global-set
                data:
                  rosa-filter: ""
          remediationAction: enforce
          severity: low
    - objectDefinition:
        apiVersion: policy.open-cluster-management.io/v1
        kind: ConfigurationPolicy
        metadata:
          name: policy-mce-hcp-autoimport
        spec:
          remediationAction: enforce
          severity: low
          object-templates-raw: |
            {{- /* find the MultiClusterEngineHCP DiscoveredClusters */ -}}
            {{- range $dc := (lookup "discovery.open-cluster-management.io/v1" "DiscoveredCluster" "" "").items }}
              {{- /* Check for the flag that indicates the import should be skipped */ -}}
              {{- $skip := "false" -}}
              {{- range $key, $value := $dc.metadata.annotations }}
                {{- if and (eq $key "discovery.open-cluster-management.io/previously-auto-imported")
                           (eq $value "true") }}
                  {{- $skip = "true" }}
                {{- end }}
              {{- end }}
              {{- /* if the type is MultiClusterEngineHCP and the status is Active */ -}}
              {{- if and (eq $dc.spec.status "Active") 
                         (contains (fromConfigMap "open-cluster-management-global-set" "discovery-config" "mce-hcp-filter") $dc.spec.displayName)
                         (eq $dc.spec.type "MultiClusterEngineHCP")
                         (eq $skip "false") }}
            - complianceType: musthave
              objectDefinition:
                apiVersion: discovery.open-cluster-management.io/v1
                kind: DiscoveredCluster
                metadata:
                  name: {{ $dc.metadata.name }}
                  namespace: {{ $dc.metadata.namespace }}
                spec:
                  importAsManagedCluster: true
              {{- end }}
            {{- end }}
---
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Placement
metadata:
  name: policy-mce-hcp-autoimport-placement
  namespace: open-cluster-management-global-set
spec:
  tolerations:
    - key: cluster.open-cluster-management.io/unreachable
      operator: Exists
    - key: cluster.open-cluster-management.io/unavailable
      operator: Exists
  clusterSets:
    - global
  predicates:
    - requiredClusterSelector:
        labelSelector:
          matchExpressions:
            - key: local-cluster
              operator: In
              values:
                - "true"
---
apiVersion: policy.open-cluster-management.io/v1
kind: PlacementBinding
metadata:
  name: policy-mce-hcp-autoimport-placement-binding
  namespace: open-cluster-management-global-set
placementRef:
  name: policy-mce-hcp-autoimport-placement
  apiGroup: cluster.open-cluster-management.io
  kind: Placement
subjects:
  - name: policy-mce-hcp-autoimport
    apiGroup: policy.open-cluster-management.io
    kind: Policy
```

When a discovered hosted cluster is auto-imported into ACM, all ACM addons are enabled as well so you can start managing the hosted clusters using the available management tools.

## Hosted cluster life-cycle management

The hosted cluster is also auto-imported into MCE. Through the MCE console, you can manage the hosted cluster's life-cycle. You cannot manage the hosted cluster life-cycle from the ACM console.

This is the MCE console.

<img width="1638" alt="image" src="https://github.com/rokej/hypershift-addon-operator/assets/41969005/c8eab313-ffa5-4996-bca8-ece5b3838f29">

<img width="1375" alt="image" src="https://github.com/rokej/hypershift-addon-operator/assets/41969005/41cee271-6d83-4701-b47e-221d21bc2b59">

<img width="1389" alt="image" src="https://github.com/rokej/hypershift-addon-operator/assets/41969005/29ffd0f7-3b69-4ea6-917c-6f7838a6c070">


## Detaching hosted clusters from ACM

An imported hosted cluster can be detached from ACM using the detach option in the ACM console or by removing the corresponsing `ManagedCluster` CR from the command line. It is recommended to detach the managed hosted cluster before destroying the hosted cluster.

When a discovered cluster is detached, the following annotation is added to the DiscoveredCluster resource to prevent the policy to import the discovered cluster again.

```
  annotations:
    discovery.open-cluster-management.io/previously-auto-imported: "true"
```

If you want the detached discovered cluster to be re-imported, this annotation needs to be remove

## Limitations

- The discovered cluster name link on the discovered cluster list UI does not open the console for discovered cluster with `MultiClusterEngineHCP` type.

<img width="1098" alt="image" src="https://github.com/rokej/hypershift-addon-operator/assets/41969005/ae9efc82-f4b2-462c-862f-0da62e8f1b87">

- The "Import cluster" discovered cluster action menu option should not be used to import `MultiClusterEngineHCP` type discovered clusters. The only way to import them is through the auto-import policy.

<img width="1138" alt="image" src="https://github.com/rokej/hypershift-addon-operator/assets/41969005/a86a0f73-04e0-4a89-a355-43c15565ef66">

- The "Last active" column for `MultiClusterEngineHCP` type discovered clusters is always "N/A".
