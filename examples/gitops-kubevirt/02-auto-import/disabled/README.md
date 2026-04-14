# Disabled Pattern: Full GitOps Control

This pattern demonstrates GitOps-first cluster management where the **ManagedCluster** (and related ACM objects) are explicitly defined and versioned in Git. **Auto-import is turned off on the hypershift-addon agent** via **`autoImportDisabled`** on the hub **`AddOnDeploymentConfig`**—not via a **HostedCluster** annotation. You then apply a Git-managed **ManagedCluster** (and **KlusterletAddonConfig**) so registration matches your desired state.

## Overview

**Cluster Name:** `example-hcp-disabled`
**Approach:** Explicit Git-managed ManagedCluster
**ManagedCluster:** Defined in Git, applied via Argo
**Best For:** Strict GitOps, production, compliance
**Time to Deploy:** ~35-45 minutes

## Key Characteristics

| Feature | Details |
|---------|---------|
| **GitOps Control** | HostedCluster, NodePool, ManagedCluster, Addons |
| **Auto-Import** | Disabled on addon: **`autoImportDisabled: "true"`** on **`AddOnDeploymentConfig`** `hypershift-addon-deploy-config` (`multicluster-engine`) → **`DISABLE_AUTO_IMPORT`** on hypershift-addon |
| **ManagedCluster** | Explicit YAML in Git, full version control |
| **Pre-Configuration** | Can set labels and configure KlusterletAddonConfig |
| **YAML Files** | 5+ (HC, NP, MC, KAC, Argo app) |
| **ACM Complexity** | Medium (explicit control) |

## What Gets Created

### In Git (Managed by You)
```
clusters namespace
├── HostedCluster: example-hcp-disabled
├── NodePool: example-hcp-disabled-workers
├── ManagedCluster: example-hcp-disabled (explicit)
└── KlusterletAddonConfig: example-hcp-disabled
```

### Controlled by You via Git
```
All ACM resources are versioned in Git:
- ManagedCluster creation/deletion is tracked
- Addon configuration is reproducible
- Changes require Pull Requests
- Full audit trail in Git history
```

## Files in This Directory

| File | Purpose |
|------|---------|
| `hostedcluster.yaml` | Defines hosted cluster (no special annotation for import; auto-import is off at the addon) |
| `nodepool.yaml` | Defines worker nodes (2 replicas, 4 cores, 16GiB each) |
| `managedcluster.yaml` | Explicitly defines the ManagedCluster resource |
| `klusterlet-addon-config.yaml` | Configures ACM addons for this cluster |
| `argo-application.yaml` | Argo CD application that manages all resources |

## Prerequisites

Before deploying this pattern:

- [ ] **Hub:** `autoImportDisabled: "true"` is set on **`AddOnDeploymentConfig`** **`hypershift-addon-deploy-config`** in namespace **`multicluster-engine`** (see **Step 0**). Until this rolls out to hypershift-addon, the auto-import controller can still create a **ManagedCluster** when the **HostedCluster** becomes **Available**—only **new** hosted clusters after the change are skipped.
- [ ] Secrets exist: `pull-secret`, `ssh-key` in `clusters` namespace
- [ ] Argo CD accessible and configured
- [ ] MCE with HyperShift addon running
- [ ] KubeVirt configured
- [ ] Compute resources available (32+ CPUs, 64+ GB RAM)

## Key concept: where auto-import is disabled

**Do not** rely on a **HostedCluster** annotation to disable auto-import. The hypershift-addon **auto-import controller** (`pkg/agent/auto_import_controller.go`) only skips when the deployment has **`DISABLE_AUTO_IMPORT=true`**, which comes from **`autoImportDisabled: "true"`** on the addon’s **`AddOnDeploymentConfig`** (default **`hypershift-addon-deploy-config`**, **`multicluster-engine`**).

The annotation **`cluster.open-cluster-management.io/managedcluster-name`** is used elsewhere (for example mirroring secrets and kubeconfig paths) to pick a **ManagedCluster** name when set to a **non-empty** value; it does **not** turn off auto-import.

**This pattern:** hub has auto-import disabled at the addon → you declare **ManagedCluster** and **KlusterletAddonConfig** in Git so import and addons match your repo.

More detail: [Disabling automatic import](../../../../docs/planning/provision_hosted_cluster_on_mce_local_cluster.md#disabling-automatic-import) and [Pattern 2 in the GitOps integration guide](../../../../docs/gitops/01-acm-integration-overview.md#pattern-2-disabled-import).

## Deployment steps

### Step 0: Disable auto-import on the hub (required)

On the **hub** cluster (where **`hypershift-addon-deploy-config`** lives), merge this variable into the existing **`customizedVariables`** (do not drop other variables your environment needs):

```bash
oc patch addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine --type=json -p='[{"op":"add","path":"/spec/customizedVariables/-","value":{"name":"autoImportDisabled","value":"true"}}]'
```

Verify:

```bash
oc get addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine -o yaml | grep -A1 autoImportDisabled
```

Wait for the **hypershift-addon** deployment on the hosting cluster to roll out so **`DISABLE_AUTO_IMPORT`** is in effect **before** you create a new **HostedCluster** for this walkthrough.

### Step 1: Review manifests

Review **HostedCluster** (no import-related annotations required for this pattern):

```bash
cat hostedcluster.yaml
```

Review the **ManagedCluster** you will apply from Git:

```bash
cat managedcluster.yaml
```

Note:
- ManagedCluster is cluster-scoped (no namespace)
- Has `hubAcceptsClient: true` to be joinable
- Can have labels for organization

Review the addon configuration:

```bash
cat klusterlet-addon-config.yaml
```

This configures:
- Application Manager (for app deployments)
- Policy Controller (for compliance policies)
- Search Collector (for resource search)
- Certificate Controller (for cert compliance)

### Step 2: Customize for your environment

**Change base domain:**
```bash
sed -i 's/example.com/your.domain/g' hostedcluster.yaml
```

**Add custom labels to ManagedCluster:**
```bash
# Edit managedcluster.yaml to add labels:
# labels:
#   environment: production
#   team: platform
#   region: us-west
```

**Change Argo repo:**
```bash
sed -i 's|https://github.com/YOUR_ORG/hypershift-addon-operator.git|https://github.com/your-org/your-repo.git|' argo-application.yaml
```

### Step 3: Dry-Run Validation

Validate YAML before applying:

```bash
oc apply --dry-run=client -f hostedcluster.yaml
oc apply --dry-run=client -f nodepool.yaml
oc apply --dry-run=client -f managedcluster.yaml
oc apply --dry-run=client -f klusterlet-addon-config.yaml
oc apply --dry-run=client -f argo-application.yaml
```

All should succeed with "(dry run)"

### Step 4: Apply Argo Application

```bash
oc apply -f argo-application.yaml
```

Verify:
```bash
oc get application -n openshift-gitops example-hcp-disabled
```

### Step 5: Trigger Initial Sync

**Via Argo CLI:**
```bash
argocd app sync example-hcp-disabled
```

**Via Argo Console:**
1. Open Argo CD console
2. Find `example-hcp-disabled` application
3. Click **SYNC** button

### Step 6: Monitor Deployment

Watch the resources in order:

**Terminal 1: HostedCluster**
```bash
oc get hostedcluster -n clusters example-hcp-disabled -w
```

**Terminal 2: ManagedCluster**
```bash
# First check: was it created?
oc get managedcluster example-hcp-disabled

# Then watch status
oc get managedcluster example-hcp-disabled -w
```

**Terminal 3: NodePool**
```bash
oc get nodepool -n clusters example-hcp-disabled-workers -w
```

Timeline:
- T+0m: Argo syncs resources
- T+1m: HostedCluster, NodePool, ManagedCluster all created
- T+10m: HostedCluster becomes Available
- T+12m: Klusterlet starts deploying to hosted cluster
- T+15m: First worker nodes joining
- T+30m: All nodes Ready and cluster fully available

## Validation

### Auto-import disabled on the addon (hub)

```bash
oc get addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine -o jsonpath='{range .spec.customizedVariables[?(@.name=="autoImportDisabled")]}{.name}={.value}{"\n"}{end}'
```

Expected: `autoImportDisabled=true`

### HostedCluster Ready

```bash
oc get hostedcluster -n clusters example-hcp-disabled
```

Expected: AVAILABLE = True

### ManagedCluster Created from Git

Verify ManagedCluster exists:
```bash
oc get managedcluster example-hcp-disabled
```

Expected: ManagedCluster resource exists

Check labels we defined in Git:
```bash
oc get managedcluster example-hcp-disabled -L environment,import-pattern
```

Expected: Shows the labels from managedcluster.yaml

Check it's joinable:
```bash
oc get managedcluster example-hcp-disabled -o jsonpath='{.spec.hubAcceptsClient}'
```

Expected: `true`

### KlusterletAddonConfig Applied

```bash
oc get klusterletaddonconfig -n example-hcp-disabled
```

Expected: Shows the addon config

Check what addons are enabled:
```bash
oc get klusterletaddonconfig -n example-hcp-disabled -o yaml | grep "enabled: true"
```

### Klusterlet Deployed

```bash
oc get pods -n example-hcp-disabled
```

Expected: klusterlet pods running in hosted cluster

### Workers Ready

```bash
oc get nodepool -n clusters example-hcp-disabled-workers
```

Expected: READY = 2/2

### Access Hosted Cluster

```bash
# Get kubeconfig
oc get secret -n clusters example-hcp-disabled-admin-kubeconfig \
  -o jsonpath='{.data.kubeconfig}' | base64 -d > /tmp/example-hcp-disabled-kubeconfig

# Test access
export KUBECONFIG=/tmp/example-hcp-disabled-kubeconfig
oc get nodes
oc get clusteroperators
```

Expected:
- 2 worker nodes in Ready state
- All cluster operators available

## GitOps Workflow

### Making Changes

This pattern enables full GitOps workflows:

**Example 1: Add a label to the ManagedCluster**

1. Edit `managedcluster.yaml`
2. Commit and push
3. Argo detects change and syncs
4. Label appears on cluster immediately

**Example 2: Enable/disable an addon**

1. Edit `klusterlet-addon-config.yaml`
2. Change `enabled: false` to `enabled: true` (or vice versa)
3. Commit and push
4. Argo syncs and updates addon configuration
5. Addon deployed/removed from cluster

**Example 3: Change node count**

1. Edit `nodepool.yaml`
2. Change `spec.replicas: 2` to `spec.replicas: 3`
3. Commit and push
4. Argo syncs the change
5. New worker node automatically created

## Coordination Between Resources

This pattern requires coordinating multiple resources:

### Resource Dependencies

```
AddOnDeploymentConfig (autoImportDisabled on hub)
    ↓
HostedCluster + NodePool (Git / Argo)
    ↓
ManagedCluster (explicit registration from Git; addon does not create it)
    ↓
KlusterletAddonConfig (addon setup from Git)
    ↓
Klusterlet / join flows
```

### Critical ordering

1. **AddOnDeploymentConfig** must have **`autoImportDisabled`** before the **HostedCluster** becomes **Available** if you want to avoid an auto-created **ManagedCluster** (only clusters created after the rollout are skipped for auto-import).
2. **HostedCluster** is created from Git; **NodePool** brings workers.
3. **ManagedCluster** comes from Git because auto-import is off at the addon.
4. **KlusterletAddonConfig** must match the **ManagedCluster** name and namespace conventions your hub expects; **Klusterlet** deployment then proceeds per ACM once **ManagedCluster** exists and joins.

This pattern handles dependencies correctly when **Step 0** is done before the hosted control plane becomes **Available**, then Argo applies the Git manifests.

## Pros and Cons

### Pros
✓ **Full Git Control** - Everything versioned and tracked
✓ **Reproducible** - Exact same configuration every time
✓ **Audit Trail** - Complete Git history of all changes
✓ **Pre-Configuration** - Set labels and addons before import
✓ **Compliance** - Meets strict GitOps requirements
✓ **Production Ready** - Suitable for production use
✓ **Pull Request Workflow** - Changes require review

### Cons
✗ **More YAML** - Additional managedcluster.yaml
✗ **More Coordination** - Must manage multiple resources
✗ **Slower to Update** - Changes need Git → PR → Merge → Sync
✗ **Learning Curve** - Must understand ManagedCluster concepts
✗ **Longer Setup** - Takes ~35-45 minutes
✗ **Not for Demos** - More ceremony for quick prototypes

## Best Use Cases

This pattern is ideal for:

- **Production Environments** - Full control and audit
- **Compliance Teams** - Git audit trails required
- **GitOps-First** - Everything in version control
- **Enterprise Deployments** - Change management required
- **Single Cluster** - Full focus on one cluster
- **Policy Enforcement** - Configure policies from day 1
- **Cost Centers** - Track cluster configurations per team

## Not Recommended For

- **Quick Demos** - Too much setup ceremony
- **Learning Environments** - More complex than needed
- **Ephemeral Clusters** - Overkill for temporary resources
- **Fully Automated** - Manual coordination needed
- **Fleet Management** - Too much per-cluster configuration

## Troubleshooting

### ManagedCluster Not Joining

**Confirm auto-import is disabled on the addon (hub):**
```bash
oc get addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine -o yaml | grep -A1 autoImportDisabled
```

If **`autoImportDisabled`** is missing or not **`"true"`**, the addon may still auto-create a **ManagedCluster**—fix **Step 0**, then see [disabling automatic import](../../../../docs/planning/provision_hosted_cluster_on_mce_local_cluster.md#disabling-automatic-import) (only **new** hosted clusters after the change).

**Check ManagedCluster exists:**
```bash
oc get managedcluster example-hcp-disabled
oc describe managedcluster example-hcp-disabled
```

**Check klusterlet deployment:**
```bash
oc get manifestwork -n example-hcp-disabled
oc logs -n example-hcp-disabled -l app=klusterlet
```

### Addon Config Not Applied

**Check namespace exists:**
```bash
oc get namespace example-hcp-disabled
```

If not, create it first:
```bash
oc create namespace example-hcp-disabled
```

**Check config was created:**
```bash
oc get klusterletaddonconfig -n example-hcp-disabled
```

**Check Argo synced:**
```bash
argocd app get example-hcp-disabled
```

### Out of Sync in Argo

Manually re-sync:
```bash
argocd app sync example-hcp-disabled --force
```

Or fix via Argo console:
1. Open Argo console
2. Click on `example-hcp-disabled`
3. Check for errors in "Result" section
4. Click **SYNC** to force reconciliation

## Upgrading from Full-Auto

If coming from Full-Auto pattern:

1. **On the hub**, set **`autoImportDisabled: "true"`** on **`hypershift-addon-deploy-config`** (see **Step 0**) and wait for hypershift-addon to roll out.
2. **Create explicit ManagedCluster / KlusterletAddonConfig YAML** in Git (you can copy labels from an auto-created **ManagedCluster** if you are migrating a name).
3. For a cluster that was **already** auto-imported, toggling **`autoImportDisabled`** alone does not remove existing **ManagedCluster** objects—plan migration (delete/recreate or adopt) per your operational rules.
4. **Let Argo apply** your Git-defined **ManagedCluster** and addons so state matches the repo.

## Cleanup

To remove this cluster:

```bash
# Option 1: Delete via Argo (cleanest)
argocd app delete example-hcp-disabled --cascade

# Option 2: Delete resources directly
oc delete hostedcluster -n clusters example-hcp-disabled
oc delete nodepool -n clusters example-hcp-disabled-workers
oc delete managedcluster example-hcp-disabled
```

This will trigger cleanup of:
- HostedCluster and control plane
- NodePool and worker VMs
- ManagedCluster and klusterlet
- KlusterletAddonConfig

Wait 5-10 minutes for full cleanup.

## See Also

- [Full-Auto Pattern](../full-auto/) - Simpler, fully automated
- [Hybrid Pattern](../hybrid/) - Production fleet management
- [ACM with GitOps](../../../../docs/gitops/00-acm-hosted-clusters-when-using-gitops.md)
- [ACM Documentation](https://access.redhat.com/documentation/en-us/red_hat_advanced_cluster_management_for_kubernetes/)
