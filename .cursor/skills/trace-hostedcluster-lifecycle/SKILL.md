---
name: trace-hostedcluster-lifecycle
description: >-
  Traces a HostedCluster through the hypershift-addon agent's reconciliation
  pipeline, from creation through secret mirroring, external-managed-kubeconfig
  generation, placement scores, cluster claims, auto-import, discovery, and
  cleanup on deletion. Use when debugging why a hosted cluster is not appearing
  as a ManagedCluster, secrets are not synced, placement scores are wrong, or
  cleanup is stuck.
---

# Trace HostedCluster Lifecycle

## Overview

When a HostedCluster is created or updated on a spoke cluster, multiple controllers in the addon agent react. This skill traces the full lifecycle to help diagnose issues.

## Controllers Involved

| Controller | Trigger | Action |
|---|---|---|
| `agentController` | HC create/update/delete | Mirror secrets to hub, generate ext-managed-kubeconfig, update placement scores, cluster claims, delete ManagedCluster on HC deletion |
| `AutoImportController` | HC create | Create ManagedCluster + KlusterletAddonConfig on local-cluster |
| `DiscoveryAgent` | HC create/update/delete | Create/update/delete DiscoveredCluster on hub (non-local clusters) |
| `ExternalSecretController` | Klusterlet create | Annotate HC with `create-external-hub-kubeconfig` timestamp |
| `HcpKubeconfigChangeWatcher` | Secret change | React to HCP kubeconfig changes |
| `AddonStatusController` | Deployment change | Report addon health status |

## Phase 1: HostedCluster Creation

### Event filter (what triggers reconciliation)

The `hostedClusterEventFilters()` predicate allows:
- **Create**: always
- **Update**: only when HostedClusterAvailable condition transitions to True, annotations change, KubeConfig/KubeadminPassword status changes, or version history changes
- **Delete**: always
- **Generic**: never

### agentController.Reconcile flow

```
HC Created →
  ├─ GenerateHCPMetrics (count HCPs, set status gauge)
  ├─ SyncAddOnPlacementScore (update HC count score on hub)
  │   ├─ Create/update AddOnPlacementScore in <clusterName> NS on hub
  │   ├─ Count available HCPs, completed HCs, deleting HCs
  │   ├─ Set metrics: TotalHostedClusterGauge, HostedControlPlaneAvailableGauge, etc.
  │   └─ Update cluster claims: full, threshold, zero
  ├─ calculateCapacitiesToHostHCPs (update capacity metrics)
  ├─ Wait for HC control plane available
  │   └─ isHostedControlPlaneAvailable: condition HostedClusterAvailable=True, Reason=AsExpected
  ├─ Mirror secrets to hub
  │   ├─ <managedClusterName>-admin-kubeconfig → hub/<clusterName> NS
  │   └─ <managedClusterName>-kubeadmin-password → hub/<clusterName> NS (if exists)
  ├─ Generate external-managed-kubeconfig
  │   ├─ Get admin kubeconfig from HC secret
  │   ├─ Replace server URL with internal: kube-apiserver.<ns>-<name>.svc.cluster.local:<port>
  │   └─ Create in klusterlet-<managedClusterName> namespace
  ├─ Replace certificate-authority-data (if serving cert configured)
  └─ Create hosted cluster claim (if version history has CompletedUpdate)
```

### AutoImportController flow (local-cluster only)

```
HC Created →
  ├─ Skip if DISABLE_AUTO_IMPORT=true
  ├─ Skip if NOT local-cluster agent
  ├─ Wait for HC control plane available (requeue if not)
  ├─ Skip if HC being deleted
  ├─ Create ManagedCluster on spoke
  │   ├─ Labels: name, vendor=auto-detect, cloud=auto-detect, clusterset=default
  │   ├─ Sync HostedCluster labels to ManagedCluster
  │   └─ Annotations: klusterlet-deploy-mode=Hosted, hosting-cluster-name, created-via=hypershift
  ├─ Skip KlusterletAddonConfig if ACM not installed
  └─ Wait for MC namespace, create KlusterletAddonConfig
```

### DiscoveryAgent flow (non-local-cluster only)

```
HC Created →
  ├─ Skip if DISABLE_HC_DISCOVERY=true
  ├─ Skip if local-cluster agent
  ├─ Wait for HC control plane available
  └─ Create DiscoveredCluster on hub in <clusterName> namespace
      ├─ Name: hc.Spec.ClusterID
      ├─ DisplayName: <prefix>-<hcName> or <clusterName>-<hcName>
      ├─ Labels: hc-name, hc-namespace
      ├─ Type: MultiClusterEngineHCP
      └─ CloudProvider: from HC platform type
```

## Phase 2: HostedCluster Deletion

### agentController on delete

```
HC Deleted (not found) →
  ├─ Delete ManagedCluster on hub
  │   ├─ Skip if created-via=hive or klusterlet-deploy-mode != Hosted
  │   └─ Remove klusterlet-hosted-cleanup finalizer from Klusterlet on spoke
  └─ Delete mirror secrets on hub (admin-kubeconfig + kubeadmin-password)
      └─ Uses label selector: cluster-name=<hcName>, hosting-namespace=<hcNamespace>

HC has deletionTimestamp (still exists) →
  ├─ Delete ManagedCluster (same logic as above)
  └─ Skip secret reconciliation
```

### DiscoveryAgent on delete

```
HC Deleted →
  ├─ Find DiscoveredCluster by labels: hc-name, hc-namespace
  └─ Delete DiscoveredCluster from hub
```

## Diagnostic Checklist

### HC not appearing as ManagedCluster

```
- [ ] Is this the local-cluster agent? (AutoImport only runs on local-cluster)
- [ ] Is DISABLE_AUTO_IMPORT=true?
- [ ] Is HC control plane available? Check: HostedClusterAvailable condition = True
- [ ] Check auto-import controller logs for errors
- [ ] Check if ManagedCluster already exists with different name
```

### Secrets not mirrored to hub

```
- [ ] Is HC control plane available?
- [ ] Check agentController logs for "failed to get hosted cluster secret"
- [ ] Verify spoke secrets exist: <hcName>-admin-kubeconfig, <hcName>-kubeadmin-password
- [ ] Check hub permissions: agent needs create/update secrets in <clusterName> NS
- [ ] Check for label mismatch on hub secrets
```

### external-managed-kubeconfig not created

```
- [ ] Does klusterlet-<managedClusterName> namespace exist?
- [ ] Check for "failed to find the klusterlet namespace" in logs (normal until import completes)
- [ ] Verify kube-apiserver service exists: kube-apiserver in <hcNS>-<hcName> namespace
- [ ] Check kubeconfig secret has valid "kubeconfig" key with "cluster" entry
- [ ] If discovery enabled: check discoveredClusterName matches expected format
```

### Placement scores incorrect

```
- [ ] Check AddOnPlacementScore on hub: kubectl get addonplacementscores -n <clusterName>
- [ ] Verify HC list can be fetched: "failed to get HostedCluster list" in logs means CRD issue
- [ ] Check cluster claims: hostedclustercapacity.hypershift.openshift.io
- [ ] Verify max/threshold values from env or hcp-sizing-baseline ConfigMap
```

### Cleanup stuck after HC deletion

```
- [ ] Check if ManagedCluster has created-via=hive annotation (won't be deleted)
- [ ] Check if klusterlet-deploy-mode != Hosted (won't be deleted)
- [ ] Look for finalizer issues on Klusterlet: operator.open-cluster-management.io/klusterlet-hosted-cleanup
- [ ] Check agent logs for "failed to delete the managedCluster"
- [ ] Verify hub connectivity from agent
```

## Key Resources

| Resource | Location | Purpose |
|---|---|---|
| `AddOnPlacementScore/hosted-clusters-score` | Hub, `<clusterName>` NS | HC count score for placement |
| Mirror secrets | Hub, `<clusterName>` NS | admin-kubeconfig + kubeadmin-password copies |
| `external-managed-kubeconfig` | Spoke, `klusterlet-<mc>` NS | Registration agent kubeconfig |
| `DiscoveredCluster` | Hub, `<clusterName>` NS | HC discovery record |
| `ManagedCluster` | Hub (cluster-scoped) | Auto-imported HC as managed cluster |
| `ClusterClaim` | Spoke (cluster-scoped) | HC count claims (zero, threshold, full) |

## Useful Log Queries

```bash
# All reconcile events for a specific HC
kubectl logs deploy/hypershift-addon-agent -n open-cluster-management-agent-addon | grep "<HC_NAME>"

# Secret mirroring
kubectl logs deploy/hypershift-addon-agent -n open-cluster-management-agent-addon | grep -E "(createOrUpdate.*secret|mirror|external-managed-kubeconfig)"

# Placement score updates
kubectl logs deploy/hypershift-addon-agent -n open-cluster-management-agent-addon | grep -i "placementscore"

# Discovery events
kubectl logs deploy/hypershift-addon-agent -n open-cluster-management-agent-addon | grep -i "discover"

# Auto-import events
kubectl logs deploy/hypershift-addon-agent -n open-cluster-management-agent-addon | grep -i "auto-import\|managed cluster"
```
