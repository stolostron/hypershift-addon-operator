# Full-Auto Pattern: Let ACM Handle Everything

This pattern demonstrates the simplest approach to GitOps HostedCluster management with ACM. We create the HostedCluster and NodePool in Git, and let ACM automatically discover and register it as a ManagedCluster.

## Overview

**Cluster Name:** `example-hcp-auto`
**Approach:** Fully automatic cluster discovery and import
**ManagedCluster:** Auto-created by ACM, NOT in Git
**Best For:** Demos, dev environments, learning
**Time to Deploy:** ~30-40 minutes

## Key Characteristics

| Feature | Details |
|---------|---------|
| **GitOps Control** | HostedCluster + NodePool only |
| **Auto-Import** | Enabled (**hypershift-addon**); hub **`hypershift-addon-deploy-config`** must **not** set **`autoImportDisabled: "true"`** |
| **ManagedCluster** | Auto-created and managed by ACM |
| **Pre-Configuration** | Cannot pre-configure ACM settings |
| **YAML Files** | 3 (hostedcluster.yaml, nodepool.yaml, argo-application.yaml) |
| **ACM Complexity** | Minimal |

## What Gets Created

### In Git (Managed by You)
```
clusters namespace
├── HostedCluster: example-hcp-auto
└── NodePool: example-hcp-auto-workers
```

### Auto-Created (Managed by ACM)
```
ManagedCluster: example-hcp-auto
├── Klusterlet
├── Klusterlet Addon Config
└── ManifestWorks
```

## Files in This Directory

| File | Purpose |
|------|---------|
| `hostedcluster.yaml` | Defines the hosted cluster (no **`autoImportDisabled`** on the addon) |
| `nodepool.yaml` | Defines worker nodes (2 replicas, 4 cores, 16GiB each) |
| `argo-application.yaml` | Argo CD application that manages HC + NP |

## Prerequisites

Before deploying this pattern:

- [ ] Secrets exist: `pull-secret`, `ssh-key` in `clusters` namespace
- [ ] Argo CD accessible and configured
- [ ] MCE with HyperShift addon running
- [ ] KubeVirt configured
- [ ] Compute resources available (32+ CPUs, 64+ GB RAM)

## Deployment Steps

### Step 1: Review Manifests

Before applying, review the HostedCluster manifest:

```bash
cat hostedcluster.yaml
```

**Key points:**
- **Hub:** leave **`autoImportDisabled`** off **`hypershift-addon-deploy-config`** so auto-import runs (see [Disabled](../disabled/) to turn it off).
- Release image: `quay.io/openshift-release-dev/ocp-release:4.21.0-multi`
- Platform: `KubeVirt`
- Base domain: `example.com` (customize as needed)

Review the Argo Application:

```bash
cat argo-application.yaml
```

**Key points:**
- Set `spec.source.repoURL` to your fork
- `ignoreDifferences` configured for auto-created ManagedCluster

### Step 2: Customize for Your Environment (Optional)

**Change base domain:**
```bash
sed -i 's/example.com/your.domain/g' hostedcluster.yaml
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
oc apply --dry-run=client -f argo-application.yaml
```

Expected output: "created (dry run)" for each file

### Step 4: Apply Argo Application

```bash
oc apply -f argo-application.yaml
```

Verify it was created:

```bash
oc get application -n openshift-gitops example-hcp-auto
```

### Step 5: Trigger Initial Sync

**Option A: Via Argo CLI**
```bash
argocd app sync example-hcp-auto
```

**Option B: Via Argo Console**
1. Open the Argo CD console (for example `oc get route -n openshift-gitops`)
2. Login with admin credentials
3. Find `example-hcp-auto` application
4. Click **SYNC** button
5. Click **SYNCHRONIZE** in dialog

### Step 6: Monitor Deployment

Watch progress with `oc` (use separate terminals for each watch):

```bash
# Watch HostedCluster status
oc get hostedcluster -n clusters example-hcp-auto -w

# Watch ManagedCluster auto-creation (key event for this pattern)
oc get managedcluster example-hcp-auto -w

# Watch NodePool status
oc get nodepool -n clusters example-hcp-auto-workers -w
```

## Validation

### HostedCluster Ready
```bash
oc get hostedcluster -n clusters example-hcp-auto
```

Expected: AVAILABLE = True, VERSION matches your release image (e.g. 4.21.0)

### ManagedCluster Auto-Created
```bash
oc get managedcluster example-hcp-auto
```

Expected: ManagedCluster exists and is joinable

### Control Plane Running
```bash
oc get pods -n clusters-example-hcp-auto | grep -E "etcd|kube-apiserver|kube-controller"
```

Expected: All pods in Running state

### Workers Ready
```bash
oc get nodepool -n clusters example-hcp-auto-workers
```

Expected: READY = 2/2

### Access Hosted Cluster
```bash
# Get kubeconfig
oc get secret -n clusters example-hcp-auto-admin-kubeconfig \
  -o jsonpath='{.data.kubeconfig}' | base64 -d > /tmp/example-hcp-auto-kubeconfig

# Test access
export KUBECONFIG=/tmp/example-hcp-auto-kubeconfig
oc get nodes
oc get clusteroperators
```

Expected:
- 2 worker nodes in Ready state
- All cluster operators available

## Key Events During Deployment

Here's what happens in the background:

### Timeline

| Time | Event |
|------|-------|
| T+0m | Argo syncs manifests, HostedCluster created |
| T+2m | HyperShift operator detects HC and creates control plane namespace |
| T+5m | Control plane pods starting (etcd, API server, etc.) |
| T+10m | HostedCluster becomes Available |
| T+10m | **hypershift-addon auto-import creates ManagedCluster** once **HostedCluster** is **Available** |
| T+12m | Klusterlet deployed to hosted cluster |
| T+15m | First worker node VMs being created |
| T+25m | Both worker nodes join and become Ready |
| T+30m | Full deployment complete |

**Key moment:** Watch for ManagedCluster auto-creation around T+10m!

## Auto-Import Process Explained

### What Triggers Auto-Import?

1. **HostedCluster** created with name `example-hcp-auto` on the hosting cluster.
2. **hypershift-addon** is running with **`DISABLE_AUTO_IMPORT`** **not** set to **`true`** (i.e. **`autoImportDisabled`** is absent or not **`"true"`** on **`AddOnDeploymentConfig`** **`hypershift-addon-deploy-config`** in **`multicluster-engine`** on the hub).
3. The **HostedCluster** control plane becomes **Available**; the hypershift-addon **auto-import controller** reconciles and creates the **ManagedCluster** (same name) and (when ACM is installed) **KlusterletAddonConfig**.

### Why No extra HostedCluster fields?

Auto-import is **not** controlled by adding or omitting **`cluster.open-cluster-management.io/managedcluster-name: ""`** on the **HostedCluster**. That annotation is for other flows (for example a **non-empty** name for mirroring); disabling import is **`autoImportDisabled`** on the addon **`AddOnDeploymentConfig`**—see the [Disabled](../disabled/) pattern.

### What Gets Auto-Created?

- **ManagedCluster** resource with the same name
- **Klusterlet** deployment in the hosted cluster
- **KlusterletAddonConfig** with default settings
- **ManifestWorks** for klusterlet components

## Monitoring Auto-Import

Watch ManagedCluster creation:
```bash
oc get managedcluster example-hcp-auto -w
```

Watch klusterlet deployment:
```bash
oc get manifestwork -n example-hcp-auto -w
```

Watch addon deployment:
```bash
oc get klusterletaddonconfig -n example-hcp-auto
```

## Pros and Cons

### Pros
✓ **Minimal YAML** - Only 3 files in Git
✓ **Fully Automated** - No manual ManagedCluster creation
✓ **Simple Workflow** - Git → Argo → Automatic
✓ **Perfect for Learning** - Understand basics without complexity
✓ **Fast Setup** - ~5 minutes to deploy

### Cons
✗ **No Pre-Configuration** - Can't configure ManagedCluster before creation
✗ **Not in Git** - ManagedCluster outside version control
✗ **Less Predictable** - Auto-creation timing varies
✗ **Hard to Template** - Difficult to create cluster blueprints
✗ **Limited GitOps** - ManagedCluster not under Git control
✗ **Not Production-Ready** - Better for dev/demo

## Best Use Cases

This pattern is ideal for:

- **Learning Environments** - Understand HCP and auto-import
- **Quick Demos** - Rapid cluster creation
- **Development Clusters** - Ephemeral test environments
- **PoCs** - Proof of concept deployments
- **Training** - Teach GitOps + HCP concepts

## Not Recommended For

- **Production Environments** - Use Hybrid or Disabled
- **Strict GitOps** - Everything should be in Git
- **Cluster Templates** - Can't pre-configure
- **Audit Requirements** - ManagedCluster not tracked in Git
- **Pre-Configured ACM** - Can't set policies before import

## Upgrading from Full-Auto

If you later want to switch patterns:

1. **Switch to Disabled Pattern:**
   - On the hub, set **`autoImportDisabled: "true"`** on **`hypershift-addon-deploy-config`** (wait for addon rollout)
   - Add explicit **ManagedCluster** / **KlusterletAddonConfig** YAML in Git and adopt or replace existing hub objects per your migration plan

2. **Switch to Hybrid Pattern:**
   - Keep HostedCluster (auto-import stays enabled)
   - Add ManagedClusterSet YAML
   - Add Placement YAML
   - Add Policy YAML

## Cleanup

To remove this cluster:

```bash
# Option 1: Delete via Argo
argocd app delete example-hcp-auto --cascade

# Option 2: Delete resources directly
oc delete hostedcluster -n clusters example-hcp-auto
oc delete nodepool -n clusters example-hcp-auto-workers

# This triggers cleanup of:
# - Control plane namespace
# - KubeVirt VMs
# - ManagedCluster (auto-deleted)
# - Klusterlet (auto-deleted)
```

Wait 5-10 minutes for full cleanup.

## Troubleshooting

### ManagedCluster Not Auto-Creating

**Check if auto-import is disabled on the addon (hub):**
```bash
oc get addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine -o yaml | grep -A1 autoImportDisabled
```

If **`autoImportDisabled: "true"`**, new hosted clusters will not get an auto-created **ManagedCluster**—use the [Disabled](../disabled/) flow or remove that variable and roll out the addon again.

**Check MCE is running:**
```bash
oc get mce multiclusterengine -o jsonpath='{.status.phase}'
```

Should output: `Available`

**Check MCE logs:**
```bash
oc logs -n multicluster-engine deployment/multicluster-engine-operator | grep -i "import\|hostedcluster"
```

### Argo Application Stuck in Progressing

**Check Argo logs:**
```bash
oc logs -n openshift-gitops deployment/openshift-gitops-argocd-application-controller
```

**Check if repo is accessible:**
```bash
argocd repo list
```

**Re-sync manually:**
```bash
argocd app sync example-hcp-auto --force
```

### HostedCluster Stuck in Progressing

**Check control plane namespace:**
```bash
oc get pods -n clusters-example-hcp-auto
```

**Check HyperShift operator logs:**
```bash
oc logs -n hypershift deployment/operator | tail -50
```

**Check cluster events:**
```bash
oc get events -n clusters --sort-by='.lastTimestamp'
```

## Next Steps

After successful deployment:

1. **Access the cluster** - Get kubeconfig and explore
2. **Keep monitoring** - Continue watching **HostedCluster** / **ManagedCluster** / **NodePool** until fully stable
3. **Enable auto-sync** - Set Argo to auto-sync changes
4. **Try other patterns** - Deploy Disabled or Hybrid in separate namespace
5. **Read deeper docs** - See [../README.md](../README.md) for pattern comparison

## See Also

- [Disabled Pattern](../disabled/) - Full GitOps control
- [Hybrid Pattern](../hybrid/) - Production-ready fleet management
- [Scenario 1: Provisioning](../../01-provision/) - Basic HCP setup
- [ACM with GitOps](../../../../docs/gitops/00-acm-hosted-clusters-when-using-gitops.md)
