# Hybrid Pattern: Production-Ready Fleet Management (Recommended)

This pattern represents the recommended approach for managing HostedClusters in production environments. It balances ACM's automation capabilities with explicit GitOps control over cluster governance, organization, and policies.

## Overview

**Cluster Name:** `example-hcp-hybrid`
**Approach:** Auto-import ManagedCluster + Git-managed ACM resources
**ManagedCluster:** Auto-created by ACM (not in Git)
**Governance:** ManagedClusterSet, Placement, Policies in Git
**Best For:** Production fleet management, multi-cluster governance
**Time to Deploy:** ~40-45 minutes

## Key Characteristics

| Feature | Details |
|---------|---------|
| **GitOps Control** | HC, NP, ManagedClusterSet, Placement, Policies |
| **Auto-Import** | Enabled (**hypershift-addon**); hub **`hypershift-addon-deploy-config`** must **not** set **`autoImportDisabled: "true"`** |
| **ManagedCluster** | Auto-created by ACM |
| **Fleet Organization** | Via ManagedClusterSet (in Git) |
| **Policy Governance** | Via Placement + Policy (in Git) |
| **YAML Files** | 7+ (HC, NP, MCS, Placement, Policy, Argo app) |
| **ACM Complexity** | Medium-High (enterprise features) |

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Cluster Fleet Management              │
└─────────────────────────────────────────────────────────┘
                            │
                ┌───────────┴───────────┐
                │                       │
         ┌──────▼─────┐         ┌──────▼──────┐
         │ Git/Argo   │         │ ACM Control │
         │ (What We   │         │ (Automation)│
         │  Manage)   │         │             │
         └──────┬─────┘         └──────┬──────┘
                │                       │
         ┌──────▼────────┐      ┌──────▼────────┐
         │ HostedCluster │      │ ManagedCluster│
         │ NodePool      │      │ (auto-created)│
         │ ClusterSet    │      │               │
         │ Placement     │      │               │
         │ Policies      │      │               │
         └──────┬────────┘      └──────┬────────┘
                │                       │
                │    ┌──────────────────┘
                │    │
                └────▼─────────────────────────┐
                                               │
                                        ┌──────▼────────┐
                                        │ Hosted Cluster│
                                        │ + Policies    │
                                        │ Applied       │
                                        └───────────────┘
```

## What Gets Created

### In Git (Managed by You - Your Responsibility)

```
clusters namespace (git-managed)
├── HostedCluster: example-hcp-hybrid
├── NodePool: example-hcp-hybrid-workers
├── ManagedClusterSet: hostedclusters
├── Placement: hostedclusters
└── Policies/
    └── sample-policy.yaml (example policy to apply)
```

### Auto-Created (ACM Automation)

```
ManagedCluster: example-hcp-hybrid (discovered and created by ACM)
├── Labels from ClusterSet membership
├── Klusterlet
├── Klusterlet Addon Config
└── Policy evaluation results
```

## Files in This Directory

| File | Purpose |
|------|---------|
| `hostedcluster.yaml` | Defines hosted cluster (addon auto-import enabled) |
| `nodepool.yaml` | Defines worker nodes |
| `managedclusterset.yaml` | Groups clusters for management (fleet organization) |
| `placement.yaml` | Selects clusters from set for policy application |
| `policies/sample-policy.yaml` | Example policy + placement binding |
| `argo-application.yaml` | Argo CD application that manages all |

## Prerequisites

Before deploying this pattern:

- [ ] Secrets exist: `pull-secret`, `ssh-key` in `clusters` namespace
- [ ] Argo CD accessible and configured
- [ ] MCE with HyperShift addon running
- [ ] KubeVirt configured
- [ ] Compute resources available (32+ CPUs, 64+ GB RAM)
- [ ] Understanding of ACM concepts: ClusterSet, Placement, Policy

## Key Concepts

### ManagedClusterSet

A logical grouping of clusters that can be governed together.

```yaml
ManagedClusterSet: hostedclusters
├── example-hcp-hybrid
├── example-hcp-prod
└── example-hcp-staging
```

**Why:** Organize clusters by function, environment, or team.

**In Git:** YES - `managedclusterset.yaml`

### Placement

Selects which clusters from a ClusterSet should receive policies/configurations.

```yaml
Placement: hostedclusters
├── Selects from: ManagedClusterSet: hostedclusters
├── Predicates: Only Available clusters
└── Scope: All clusters in set (customizable)
```

**Why:** Flexible cluster selection based on labels and conditions.

**In Git:** YES - `placement.yaml`

### Policy

Configuration or compliance rule applied to selected clusters.

```yaml
Policy: require-openshift-namespace
├── Target: Clusters selected by Placement
├── Rule: Namespace must exist
└── Remediation: Inform (or auto-remediate)
```

**Why:** Enforce standards across your cluster fleet.

**In Git:** YES - `policies/sample-policy.yaml`

### Auto-Created ManagedCluster

When the **HostedCluster** is **Available**, **hypershift-addon** auto-import creates the **ManagedCluster** (unless **`autoImportDisabled`** is set on the hub addon config).

**Why:** Reduces manual steps, leverages ACM automation.

**In Git:** NO - Managed by ACM, but can be selected by Placement

## Deployment Steps

### Step 1: Understand the Pattern

Review the concept diagram above. This pattern:
1. Specifies cluster provisioning (HC, NP) - our responsibility
2. Lets **hypershift-addon** auto-import and create **ManagedCluster**
3. We organize clusters with ClusterSet
4. We select clusters with Placement
5. We apply policies to selected clusters

### Step 2: Review Manifests

**HostedCluster** (no hub **`autoImportDisabled`** for this pattern):
```bash
oc get addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine -o yaml | grep -A1 autoImportDisabled || true
```

Expected: no **`autoImportDisabled: "true"`** (or omit the variable entirely).

**ManagedClusterSet (organize your fleet):**
```bash
cat managedclusterset.yaml
```

This creates the "hostedclusters" logical group.

**Placement (select clusters for governance):**
```bash
cat placement.yaml
```

This selects all available clusters in the "hostedclusters" set.

**Sample Policy (example governance):**
```bash
cat policies/sample-policy.yaml
```

This policy ensures a compliance namespace exists on all selected clusters.

### Step 3: Customize for Your Environment

**Change base domain:**
```bash
sed -i 's/example.com/your.domain/g' hostedcluster.yaml
```

**Customize the policy for your needs:**
```bash
# Edit policies/sample-policy.yaml
# Change the policy name, requirements, remediation action, etc.
```

**Add your own policies:**
```bash
# Create additional policy files in policies/
# They'll be picked up by Argo on sync
```

**Change Argo repo:**
```bash
sed -i 's|https://github.com/YOUR_ORG/hypershift-addon-operator.git|https://github.com/your-org/your-repo.git|' argo-application.yaml
```

### Step 4: Dry-Run Validation

```bash
oc apply --dry-run=client -f hostedcluster.yaml
oc apply --dry-run=client -f nodepool.yaml
oc apply --dry-run=client -f managedclusterset.yaml
oc apply --dry-run=client -f placement.yaml
oc apply --dry-run=client -f policies/sample-policy.yaml
oc apply --dry-run=client -f argo-application.yaml
```

All should succeed with "(dry run)"

### Step 5: Apply Argo Application

```bash
oc apply -f argo-application.yaml
```

Verify:
```bash
oc get application -n openshift-gitops example-hcp-hybrid
```

### Step 6: Trigger Initial Sync

**Via Argo CLI:**
```bash
argocd app sync example-hcp-hybrid
```

**Via Argo Console:**
1. Open Argo CD console
2. Find `example-hcp-hybrid`
3. Click **SYNC**

### Step 7: Monitor Deployment

Watch key components:

**Terminal 1: HostedCluster**
```bash
oc get hostedcluster -n clusters example-hcp-hybrid -w
```

**Terminal 2: ManagedCluster (auto-creation)**
```bash
oc get managedcluster example-hcp-hybrid -w
```

**Terminal 3: Placement decisions**
```bash
oc get placement -n clusters hostedclusters -w
```

**Terminal 4: Policy compliance**
```bash
oc get clusterpolicies -n example-hcp-hybrid -w
```

Timeline:
- T+0m: Argo syncs resources
- T+2m: HostedCluster, NodePool, ClusterSet, Placement created
- T+10m: HostedCluster becomes Available
- T+10m: hypershift-addon auto-import creates ManagedCluster
- T+12m: ManagedCluster picked up by Placement
- T+12m: Policies begin evaluation on cluster
- T+15m: Worker nodes being created
- T+30m: Cluster fully available and policies applied

## Validation

### HostedCluster Ready

```bash
oc get hostedcluster -n clusters example-hcp-hybrid
```

Expected: AVAILABLE = True

### ManagedCluster Auto-Created

```bash
oc get managedcluster example-hcp-hybrid
```

Expected: ManagedCluster exists

### ManagedClusterSet Created

```bash
oc get managedclusterset hostedclusters
```

Expected: ClusterSet exists

### Placement Selecting Clusters

```bash
oc get placement -n clusters hostedclusters -o yaml
```

Check `status.decisions` shows our cluster selected.

### Policies Applied to Cluster

Check policy compliance on the cluster:
```bash
# Get kubeconfig first
export KUBECONFIG=/tmp/example-hcp-hybrid-kubeconfig
oc get ns openshift-custom-compliance
```

Expected: Compliance namespace exists (per policy)

### Full Status Check

```bash
# HostedCluster
echo "=== HostedCluster ==="
oc get hostedcluster -n clusters example-hcp-hybrid

# ManagedCluster
echo "=== ManagedCluster ==="
oc get managedcluster example-hcp-hybrid

# ClusterSet
echo "=== ManagedClusterSet ==="
oc get managedclusterset hostedclusters

# Placement decisions
echo "=== Placement Decisions ==="
oc get placement -n clusters hostedclusters -o jsonpath='{.status.decisions}'

# NodePool
echo "=== NodePool ==="
oc get nodepool -n clusters example-hcp-hybrid-workers
```

## GitOps Workflow for Fleet Management

### Scenario 1: Add a New Policy

1. Create new policy file in `policies/`
2. Create PlacementBinding in same file
3. Commit and push
4. Argo syncs new policy
5. Policy evaluated against all clusters in Placement

```bash
# Example: policies/require-pod-security.yaml
apiVersion: policy.open-cluster-management.io/v1
kind: Policy
metadata:
  name: require-pod-security
  namespace: clusters
spec:
  # ... policy definition ...

---
apiVersion: policy.open-cluster-management.io/v1beta1
kind: PlacementBinding
metadata:
  name: require-pod-security-placement
  namespace: clusters
spec:
  placementRef:
    name: hostedclusters
    kind: Placement
    apiGroup: cluster.open-cluster-management.io
  subjects:
  - name: require-pod-security
    kind: Policy
    apiGroup: policy.open-cluster-management.io
```

### Scenario 2: Add a New Cluster to the Fleet

1. Deploy new HostedCluster in Scenario 1 or Scenario 2 directory
2. **HostedCluster** becomes **Available**; **hypershift-addon** auto-import runs
3. **ManagedCluster** is auto-created
4. Placement automatically selects it (matches labels)
5. All policies automatically apply

The hybrid pattern scales gracefully!

### Scenario 3: Scale Cluster Fleet to Different Environment

1. Create new Placement for production:
```yaml
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Placement
metadata:
  name: production-clusters
  namespace: clusters
spec:
  clusterSets:
  - hostedclusters
  predicates:
  - requiredClusterConditionType: ManagedClusterConditionAvailable
```

2. Create production-specific policy:
```yaml
apiVersion: policy.open-cluster-management.io/v1
kind: Policy
metadata:
  name: production-security-policy
  namespace: clusters
spec:
  # ... stricter production requirements ...
```

3. Bind policy to production Placement
4. Commit and sync
5. Production-specific policy applies only to production clusters

## Hybrid Advantages

### Flexibility

- Auto-import for speed (Full-Auto benefit)
- Explicit governance via Git (Disabled benefit)
- Best of both worlds

### Scalability

- Add clusters → automatically picked up by Placement
- Add policies → automatically applied to all clusters
- Cluster fleet grows without manual coordination

### Production-Ready

- ManagedCluster auto-created (less operational burden)
- Governance in Git (audit trail, versioning)
- Fleet-level controls (scaling, policies, grouping)
- Enterprise features (Placement, Policies)

### Easier Migration

- Start with Full-Auto
- Gradually add Placement and Policies
- Eventually add explicit ManagedCluster if needed

## Pros and Cons

### Pros
✓ **Automation** - ManagedCluster auto-created
✓ **GitOps** - Fleet governance in Git
✓ **Scalable** - Handles large multi-cluster deployments
✓ **Fleet-Ready** - Built for managing many clusters
✓ **Policy Governance** - Enforce standards across fleet
✓ **Production-Worthy** - Enterprise-grade
✓ **Balanced** - Automation + Control

### Cons
✗ **More Complex** - More concepts to learn
✗ **More YAML** - More resources to manage
✗ **Requires Planning** - Cluster organization upfront
✗ **Learning Curve** - Placement and Policies take time
✗ **Setup Time** - ~40 minutes initial deployment

## Best Use Cases

This pattern is ideal for:

- **Production Environments** - Recommended approach
- **Fleet Management** - Multiple clusters
- **Governance at Scale** - Policy enforcement
- **Compliance Requirements** - Audit trails and standards
- **Multi-Team Deployments** - Organize by team/environment
- **GitOps-First Orgs** - Everything version-controlled
- **Large Enterprises** - Feature-rich governance

## Not Recommended For

- **Single Cluster** - Overkill
- **Quick Demos** - Too much setup
- **Simple Dev Environments** - Use Full-Auto
- **No Governance Needs** - Use simpler patterns

## Troubleshooting

### Placement Not Selecting Cluster

**Check ManagedCluster exists:**
```bash
oc get managedcluster example-hcp-hybrid
```

**Check ClusterSet exists:**
```bash
oc get managedclusterset hostedclusters
```

**Check Placement predicates:**
```bash
oc get placement -n clusters hostedclusters -o yaml | grep -A 5 "predicates"
```

**Check Placement decisions:**
```bash
oc get placement -n clusters hostedclusters -o jsonpath='{.status.decisions}'
```

If empty, cluster may not be "Available" yet.

### Policy Not Applying

**Check policy exists:**
```bash
oc get policy -n clusters
```

**Check placement binding:**
```bash
oc get placementbinding -n clusters
```

**Check policy status:**
```bash
oc get policy -n clusters require-openshift-namespace -o yaml | grep -A 5 "status"
```

**Check on cluster:**
```bash
export KUBECONFIG=/tmp/example-hcp-hybrid-kubeconfig
oc get ns openshift-custom-compliance
```

### Argo Out of Sync

```bash
argocd app get example-hcp-hybrid
argocd app sync example-hcp-hybrid --force
```

## Upgrading from Other Patterns

### From Full-Auto

1. Add ClusterSet YAML
2. Add Placement YAML
3. Add Policy YAML
4. Commit and let Argo sync
5. Governance resources created
6. Cluster automatically selected by Placement

### From Disabled

1. On the hub, remove **`autoImportDisabled`** from **`hypershift-addon-deploy-config`** (or set to **`"false"`**) and wait for **hypershift-addon** to roll out.
2. Remove Git-managed **ManagedCluster** and **KlusterletAddonConfig** YAML if you will rely on auto-import for **new** clusters (plan adoption for any existing **ManagedCluster**).
3. Add **ManagedClusterSet**, **Placement**, and **Policy** YAML.
4. Commit and sync; new **HostedClusters** get an auto-created **ManagedCluster** again.
5. **Placement** selects the cluster; policies apply.

## Cleanup

```bash
# Option 1: Delete via Argo (cleanest)
argocd app delete example-hcp-hybrid --cascade

# Option 2: Delete resources directly
oc delete hostedcluster -n clusters example-hcp-hybrid
oc delete nodepool -n clusters example-hcp-hybrid-workers
oc delete managedclusterset hostedclusters
oc delete placement -n clusters hostedclusters
oc delete policy -n clusters -l app=required
```

This triggers cleanup of:
- HostedCluster and control plane
- NodePool and worker VMs
- ManagedCluster (auto-deleted when HC deleted)
- Policies and Placements

## See Also

- [Full-Auto Pattern](../full-auto/) - Simpler automated approach
- [Disabled Pattern](../disabled/) - Maximum explicit control
- [ACM with GitOps](../../../../docs/gitops/00-acm-hosted-clusters-when-using-gitops.md)
- [ACM Fleet Management Docs](https://access.redhat.com/documentation/en-us/red_hat_advanced_cluster_management_for_kubernetes/)
- [Scenario 3: Scaling](../../03-scaling/) - Manage multiple clusters at scale
