# Scenario 1: Provisioning HCP via GitOps

This scenario demonstrates creating a Hosted Control Plane (HCP) using GitOps with Argo CD.

## Overview

**Goal:** Deploy a KubeVirt-based HostedCluster with 2 worker nodes using Argo CD to sync manifests from Git.

**What gets created:**
- Namespace: `clusters`
- HostedCluster: `example-hcp`
- NodePool: `example-hcp-workers` (2 replicas)
- Argo Application: `example-hcp`

**What ACM creates automatically:**
- ManagedCluster: `example-hcp`
- Klusterlet and addons in the hosted cluster

## Prerequisites

- [ ] ACM/MCE installed with HyperShift addon
- [ ] Argo CD installed and configured
- [ ] KubeVirt configured
- [ ] Namespace `clusters` exists (or will be created by `namespace.yaml`)
- [ ] Secrets created in `clusters`: `pull-secret`, `ssh-key` (see HyperShift / MCE docs for format)

## Files in This Directory

| File | Description |
|------|-------------|
| `namespace.yaml` | Creates the `clusters` namespace |
| `hostedcluster.yaml` | Defines the HostedCluster resource |
| `nodepool.yaml` | Defines the NodePool with 2 workers |
| `argo-application.yaml` | Argo CD Application that manages these resources |
| `kustomization.yaml` | Kustomize file (optional, for kustomize users) |
| `VALIDATION.md` | Step-by-step validation checklist |

## Quick Start

### Step 1: Review and Customize Manifests

Before applying, customize these manifests for your environment:

**hostedcluster.yaml:**
- `spec.release.image` - Change to your desired OpenShift version
- `spec.platform.kubevirt.baseDomain` - Change to your base domain
- `spec.dns.baseDomain` - Must match platform baseDomain

**argo-application.yaml:**
- `spec.source.repoURL` - Change to your fork/repo
- `spec.source.targetRevision` - Change to your branch (e.g., `main`)

### Step 2: Fork or Clone This Repository

If using a Git repository other than the original:

```bash
# Update argo-application.yaml with your repo URL
sed -i 's|https://github.com/YOUR_ORG/hypershift-addon-operator.git|https://github.com/your-org/your-repo.git|' argo-application.yaml
```

### Step 3: Apply the Argo Application

```bash
oc apply -f argo-application.yaml
```

### Step 4: Sync the Application

Option A - Via ArgoCD CLI:
```bash
argocd app sync example-hcp
```

Option B - Via ArgoCD Console:
1. Open the Argo CD console (for example `oc get route -n openshift-gitops`)
2. Login with admin credentials
3. Click on `example-hcp` application
4. Click **SYNC** button
5. Click **SYNCHRONIZE**

### Step 5: Monitor the Deployment

Watch HostedCluster status:
```bash
oc get hostedcluster -n clusters example-hcp -w
```

Wait for:
- AVAILABLE: True
- VERSION: 4.21.0 (matches `spec.release.image`, e.g. `4.21.0-multi`)

This takes ~10-15 minutes.

Watch NodePool status:
```bash
oc get nodepool -n clusters example-hcp-workers -w
```

Wait for:
- READY: 2/2

This takes ~15-20 additional minutes.

### Step 6: Validate

Follow the complete validation checklist in [VALIDATION.md](VALIDATION.md).

## What Happens Behind the Scenes

When you sync the Argo Application:

1. **Argo CD** applies the manifests to the management cluster
2. **HyperShift Operator** detects the HostedCluster and:
   - Creates a namespace: `clusters-example-hcp`
   - Deploys control plane components (etcd, API server, etc.)
   - Creates LoadBalancer service for API server
3. **HyperShift Operator** processes the NodePool and:
   - Creates KubeVirt VirtualMachines
   - VMs boot and join the hosted cluster
4. **ACM** detects the HostedCluster and:
   - Auto-creates a ManagedCluster resource
   - Deploys klusterlet to the hosted cluster
   - Registers the cluster in ACM

## Accessing the Hosted Cluster

Get the kubeconfig:
```bash
oc get secret -n clusters example-hcp-admin-kubeconfig \
  -o jsonpath='{.data.kubeconfig}' | base64 -d > /tmp/example-hcp-kubeconfig
```

Use it:
```bash
export KUBECONFIG=/tmp/example-hcp-kubeconfig
oc get nodes
oc get co
```

## GitOps Workflow

After the initial deployment:

1. **Enable auto-sync** in `argo-application.yaml`:
   ```yaml
   syncPolicy:
     automated:
       prune: true
       selfHeal: false
   ```

2. **Make changes** by editing manifests in Git
3. **Commit and push** to your repository
4. **Argo CD automatically syncs** changes to the cluster

Example: Scale to 3 workers:
```yaml
# In nodepool.yaml
spec:
  replicas: 3
```

Commit → Push → Argo syncs → New worker node created

## Cleanup

To delete this HostedCluster:

```bash
# Option 1: Via Argo CD (preserves Application)
argocd app delete example-hcp --cascade

# Option 2: Delete the Application (removes everything)
oc delete application -n openshift-gitops example-hcp
```

Wait for resources to be cleaned up (~5 minutes).

## Next Steps

- [Scenario 2: Auto-Import Patterns](../02-auto-import/) - Learn about the three auto-import approaches
- [Documentation: ACM with GitOps](../../../docs/gitops/00-acm-hosted-clusters-when-using-gitops.md)
- [Documentation: GitOps guide](../../../docs/gitops/README.md)

## Troubleshooting

See [VALIDATION.md](VALIDATION.md) for validation steps and [../../../docs/gitops/02-troubleshooting.md](../../../docs/gitops/02-troubleshooting.md) for common issues.
