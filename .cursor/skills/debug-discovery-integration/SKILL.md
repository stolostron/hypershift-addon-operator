---
name: debug-discovery-integration
description: >-
  Troubleshoots the hosted cluster discovery flow spanning both hub and spoke
  sides. Covers the spoke DiscoveryAgent that creates DiscoveredCluster
  resources and the hub DiscoveryConfigController that manages
  AddOnDeploymentConfig, ClusterManagementAddOns, and KlusterletConfig for MCE
  import. Use when discovered clusters are missing, discovery config is not
  applied, or MCE import configuration fails.
---

# Debug Discovery Integration

## Architecture

Discovery has two independent sides:

### Spoke side: `DiscoveryAgent` (`pkg/agent/discovery_agent.go`)
- Watches `HostedCluster` resources
- Creates/updates/deletes `DiscoveredCluster` resources on the hub
- Skipped on local-cluster agents and when `DISABLE_HC_DISCOVERY=true`

### Hub side: `DiscoveryConfigController` (`pkg/manager/discovery_config_controller.go`)
- Watches `AddOnDeploymentConfig` named `hypershift-addon-deploy-config` in `multicluster-engine` namespace
- Gates on ACM being installed (skips if ACM not found)
- When `configureMceImport=true`: configures addon deployment configs, ClusterManagementAddOns, KlusterletConfig
- When `configureMceImport=false`: removes the created resources (if no ManagedClusters still using them)

## Diagnostic Checklist

### Spoke: DiscoveredCluster not created

```
- [ ] Step 1: Check if discovery is disabled
- [ ] Step 2: Verify this is NOT the local-cluster agent
- [ ] Step 3: Verify HC control plane is available
- [ ] Step 4: Check DiscoveredCluster on hub
- [ ] Step 5: Inspect discovery agent logs
- [ ] Step 6: Verify DISCOVERY_PREFIX env var
```

#### Step 1: Check if discovery is disabled

```bash
kubectl get deploy hypershift-addon-agent -n open-cluster-management-agent-addon \
  -o jsonpath='{.spec.template.spec.containers[0].env}' | jq '.[] | select(.name=="DISABLE_HC_DISCOVERY")'
```

If `DISABLE_HC_DISCOVERY=true`, the agent logs `"hosted cluster discovery is disabled, skip discovering"`.

#### Step 2: Verify this is NOT the local-cluster agent

Discovery is skipped when `clusterName == localClusterName`. The local-cluster is identified by label `local-cluster=true` on ManagedCluster.

```bash
# Check what cluster this agent manages
kubectl get deploy hypershift-addon-agent -n open-cluster-management-agent-addon \
  -o jsonpath='{.spec.template.spec.containers[0].args}' | tr ',' '\n' | grep cluster-name
```

If this is `local-cluster`, the DiscoveryAgent will not run. AutoImportController handles local-cluster instead.

#### Step 3: Verify HC control plane is available

The DiscoveryAgent waits for `HostedClusterAvailable` condition to be True:

```bash
kubectl get hostedcluster <HC_NAME> -n <HC_NAMESPACE> \
  -o jsonpath='{.status.conditions[?(@.type=="Available")]}' | jq .
```

If not available, the agent logs `"hosted control plane of (<name>) is unavailable"` and returns without error (no requeue).

#### Step 4: Check DiscoveredCluster on hub

```bash
# On the hub
kubectl get discoveredclusters -n <CLUSTER_NAME> \
  -l "hypershift.open-cluster-management.io/hc-name=<HC_NAME>,hypershift.open-cluster-management.io/hc-namespace=<HC_NAMESPACE>"
```

The DiscoveredCluster is named after `hc.Spec.ClusterID`, not the HC name.

#### Step 5: Inspect discovery agent logs

```bash
kubectl logs deploy/hypershift-addon-agent -n open-cluster-management-agent-addon | grep -i "discover"
```

Key log patterns:
- `"creating discovered cluster for hosted cluster"` — creation in progress
- `"updating discovered cluster for hosted cluster"` — API URL or version changed
- `"deleting the discovered cluster for hosted cluster"` — HC was deleted
- `"there are N discovered clusters for hosted cluster"` — duplicate DiscoveredClusters (error state)

#### Step 6: Verify DISCOVERY_PREFIX env var

The discovered cluster display name is determined by:
1. `DISCOVERY_PREFIX` env var set and non-empty: `<prefix>-<hcName>`
2. `DISCOVERY_PREFIX` env var set but empty: `<hcName>`
3. `DISCOVERY_PREFIX` not set: `<clusterName>-<hcName>`

This also affects the `external-managed-kubeconfig` namespace: `klusterlet-<displayName>`.

```bash
kubectl get deploy hypershift-addon-agent -n open-cluster-management-agent-addon \
  -o jsonpath='{.spec.template.spec.containers[0].env}' | jq '.[] | select(.name=="DISCOVERY_PREFIX")'
```

---

### Hub: DiscoveryConfigController not working

```
- [ ] Step 1: Verify ACM is installed
- [ ] Step 2: Check AddOnDeploymentConfig exists
- [ ] Step 3: Check configureMceImport value
- [ ] Step 4: Inspect created resources
- [ ] Step 5: Check controller logs
- [ ] Step 6: Verify disable flow (if disabling)
```

#### Step 1: Verify ACM is installed

The controller checks for `advanced-cluster-management` ClusterServiceVersion:

```bash
kubectl get csv -A | grep advanced-cluster-management
```

If ACM is not found, the controller logs `"Skipping reconciliation - ACM is not installed"` and returns.

On non-OpenShift clusters where CSV CRD doesn't exist, it assumes ACM is installed.

#### Step 2: Check AddOnDeploymentConfig exists

The controller only watches `hypershift-addon-deploy-config` in `multicluster-engine` namespace:

```bash
kubectl get addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine -o yaml
```

#### Step 3: Check configureMceImport value

The controller reads the `configureMceImport` customized variable:

```bash
kubectl get addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine \
  -o jsonpath='{.spec.customizedVariables}' | jq '.[] | select(.name=="configureMceImport")'
```

Valid values:
- `"true"` — enable MCE import: creates resources
- `"false"` — disable MCE import: removes resources (if safe)
- Any other value or missing — no action taken (ConfigMap still updated)

#### Step 4: Inspect created resources (when configureMceImport=true)

The controller creates/manages these resources:

**1. Customized variables on hypershift-addon-deploy-config:**

```bash
kubectl get addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine \
  -o jsonpath='{.spec.customizedVariables}' | jq .
```

Expected variables added: `disableMetrics=true`, `disableHOManagement=true`.
Expected `agentInstallNamespace`: `open-cluster-management-agent-addon-discovery`.

**2. Discovery AddOnDeploymentConfig:**

```bash
kubectl get addondeploymentconfig addon-ns-config -n multicluster-engine -o yaml
```

Expected: `agentInstallNamespace: open-cluster-management-agent-addon-discovery`.

**3. ClusterManagementAddOn references:**

The controller adds `addon-ns-config` reference to: `work-manager`, `managed-serviceaccount`, `cluster-proxy`.

```bash
for addon in work-manager managed-serviceaccount cluster-proxy; do
  echo "=== $addon ==="
  kubectl get clustermanagementaddon $addon -o jsonpath='{.spec.installStrategy.placements}' | jq '.[].configs'
done
```

For `application-manager`, it uses Manual strategy with `supportedConfigs`:

```bash
kubectl get clustermanagementaddon application-manager -o jsonpath='{.spec}' | jq '{strategy: .installStrategy.type, supportedConfigs: .supportedConfigs}'
```

**4. KlusterletConfig:**

```bash
kubectl get klusterletconfig mce-import-klusterlet-config -o yaml
```

Expected spec: `installMode.type: noOperator`, `installMode.noOperator.postfix: mce-import`.

**5. Info ConfigMap:**

```bash
kubectl get cm hypershift-addon-deploy-config-info -n multicluster-engine -o yaml
```

#### Step 5: Check controller logs

```bash
kubectl logs deploy/hypershift-addon-manager -n multicluster-engine | grep -i "discovery\|configureMceImport\|addon-ns-config\|KlusterletConfig\|ClusterManagementAddOn"
```

Key log patterns:
- `"Reconciling AddonDeploymentConfig for discovery config controller"` — reconcile triggered
- `"Skipping reconciliation - ACM is not installed"` — ACM check failed
- `"Processing addon deployment config - configureMceImport is enabled"` — enable flow
- `"Processing addon deployment config - configureMceImport is disabled"` — disable flow
- `"Cannot disable MCE import - there are ManagedClusters still using mce-import-klusterlet-config"` — safe guard

#### Step 6: Verify disable flow

When `configureMceImport=false`, the controller first checks if any ManagedClusters reference `mce-import-klusterlet-config`:

```bash
kubectl get managedclusters -o json | jq '.items[] | select(.metadata.annotations["agent.open-cluster-management.io/klusterlet-config"] == "mce-import-klusterlet-config") | .metadata.name'
```

If clusters are still using it, removal is blocked and logged.

## Event Filter

The hub controller uses predicates that only process `hypershift-addon-deploy-config` in `multicluster-engine` namespace. It reconciles on:
- **Create**: always (for the matching name/namespace)
- **Update**: `configureMceImport` value changed, generation changed, or metadata (labels/annotations) changed
- **Delete**: always (for the matching name/namespace)

## Spoke Agent MCE Import Config

The spoke agent (`pkg/agent/agent.go`) also has MCE import logic triggered by `CONFIGURE_MCE_IMPORT=true` env var. This is the **non-ACM** path:
- Creates `disable-sync-labels-to-clusterclaims` AddOnDeploymentConfig in `multicluster-engine` NS
- Updates `work-manager` ClusterManagementAddOn to reference it

This only runs when ACM is **not** installed. If ACM is installed, the hub DiscoveryConfigController handles configuration instead.

## Common Issues

1. **DiscoveredCluster duplicates**: Multiple DCs with same HC labels — the agent errors and refuses to act. Manual cleanup needed.
2. **Discovery skipped on local-cluster**: By design. AutoImportController handles local-cluster HCs.
3. **configureMceImport not taking effect**: Check that the AddOnDeploymentConfig name is exactly `hypershift-addon-deploy-config` in `multicluster-engine` namespace.
4. **Resources not cleaned up on disable**: ManagedClusters still annotated with `mce-import-klusterlet-config` — the controller won't remove resources until those clusters are detached.
5. **ACM check false negative**: If CSV CRD exists but no `advanced-cluster-management` CSV prefix is found, ACM is considered not installed.
