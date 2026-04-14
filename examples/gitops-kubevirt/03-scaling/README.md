# Scenario 3: Scaling and NodePool Management

This scenario demonstrates dynamic scaling and multi-NodePool management patterns for hosted clusters using GitOps.

## Overview

**Goal:** Learn horizontal scaling, vertical scaling, autoscaling, and multi-NodePool management through practical operations applied via Git commits.

**Key concepts:**
- Horizontal scaling: Adding/removing nodes (replicas)
- Vertical scaling: Changing node compute resources
- Autoscaling: Dynamic scaling based on demand
- Multi-NodePool patterns: Workload isolation and resource segmentation

**What this scenario covers:**
- Base state: 2-node cluster (same as Scenario 1)
- Operation 1: Scale horizontally from 2 to 5 nodes
- Operation 2: Enable autoscaling (2-8 nodes)
- Operation 3: Vertical scaling (change node resources)
- Operation 4: Add a second NodePool with different characteristics
- Operation 5: Remove a NodePool

## Prerequisites

- [ ] ACM/MCE installed with HyperShift addon
- [ ] Argo CD installed and configured
- [ ] KubeVirt configured
- [ ] Secrets created in `clusters` (`pull-secret`, `ssh-key`)

Optional but recommended:
- [ ] Completed Scenario 1: Provisioning
- [ ] Completed Scenario 2: Auto-Import
- [ ] Cluster Autoscaler deployed (for Operation 2)

## Directory Structure

```
03-scaling/
├── README.md                    # This file
├── VALIDATION.md               # Step-by-step validation checklist
├── argo-application.yaml       # Argo CD Application for base configuration
├── base/
│   ├── hostedcluster.yaml     # Base HostedCluster (4.21.0-multi, standard config)
│   └── nodepool.yaml          # Base NodePool (2 replicas, 4 cores, 16Gi)
└── operations/
    ├── 01-scale-up.yaml               # Scale replicas 2 → 5
    ├── 02-add-autoscaling.yaml        # Enable autoscaling (2-8 nodes)
    ├── 03-modify-instance-size.yaml   # Vertical scale: 4c/16Gi → 8c/32Gi
    ├── 04-add-nodepool.yaml           # Create infra pool (2c/8Gi, tainted)
    └── 05-remove-nodepool.yaml        # Guide for removing a NodePool
```

## Quick Start

### Step 1: Deploy Base Configuration

First, ensure you have a running HostedCluster from Scenario 1. If not, deploy the base:

```bash
# Apply the Argo Application for base configuration
oc apply -f argo-application.yaml

# Trigger initial sync
argocd app sync example-hcp-scaling
```

Wait for base HostedCluster and NodePool to be ready (see VALIDATION.md).

### Step 2: Apply Scaling Operations

Operations are applied sequentially, each building on the previous state.

#### Operation 1: Scale Up (2 → 5 nodes)

```bash
# Option A: Direct patch
oc patch nodepool -n clusters example-hcp-workers \
  --type merge -p '{"spec":{"replicas":5}}'

# Option B: Via Git (recommended for GitOps)
# Edit base/nodepool.yaml, change replicas: 2 to replicas: 5
# Commit and push, Argo auto-syncs
```

Monitor the scale-up:
```bash
oc get nodepool -n clusters example-hcp-workers -w
```

Expected: READY goes from 2/2 → 3/3 → 4/4 → 5/5 (2-3 minutes per node)

#### Operation 2: Enable Autoscaling

This operation **removes** the static `replicas` field and adds `autoScaling` configuration.

```bash
# Option A: Apply the manifest (only update, not delete)
oc apply -f operations/02-add-autoscaling.yaml

# Option B: Via Git
# Edit base/nodepool.yaml:
#   - Remove: spec.replicas: 2 (matches base; use 5 if you completed Operation 1 above)
#   - Add: spec.autoScaling.minReplicas: 2
#         spec.autoScaling.maxReplicas: 8
# Commit and push
```

Verify autoscaling is active:
```bash
oc get nodepool -n clusters example-hcp-workers -o yaml | grep -A2 autoScaling
```

Expected output:
```yaml
autoScaling:
  maxReplicas: 8
  minReplicas: 2
```

**Important:** After enabling autoscaling, the current node count may adjust to match minReplicas if it was higher.

#### Operation 3: Vertical Scaling (Change Instance Size)

This increases the compute resources for each node (4c/16Gi → 8c/32Gi).

```bash
# Option A: Apply the manifest
oc apply -f operations/03-modify-instance-size.yaml

# Option B: Via Git
# Edit base/nodepool.yaml spec.platform.kubevirt.compute:
#   - cores: 8 (was 4)
#   - memory: 32Gi (was 16Gi)
# Commit and push
```

Monitor the upgrade:
```bash
oc get nodepool -n clusters example-hcp-workers -w
```

Expected: Nodes will be replaced with new instances (5-10 minutes per node, depends on upgradeType)

**Consequences:**
- Old nodes will be drained and replaced
- Workloads will be rescheduled
- Larger instances = higher resource cost per node
- May need fewer nodes for same capacity

#### Operation 4: Add a Second NodePool

Create an additional NodePool for infrastructure workloads.

```bash
# Apply the infra NodePool manifest
oc apply -f operations/04-add-nodepool.yaml

# Or add it to your Git repo in the base/ directory and commit
```

Verify the new NodePool:
```bash
oc get nodepool -n clusters
```

Expected: Both `example-hcp-workers` and `example-hcp-infra` are visible

Check the infra nodes:
```bash
oc get nodes -L node-type
```

The infra pool has:
- Taint: `node-type=infra:NoSchedule` (only tolerant pods scheduled)
- Label: `node-type=infra` (for explicit pod selection)
- Resources: 2 cores, 8Gi memory (cost-optimized)

**Deploy workloads to infra pool:**
```yaml
podSpec:
  tolerations:
  - key: node-type
    operator: Equal
    value: infra
    effect: NoSchedule
  nodeSelector:
    node-type: infra
```

#### Operation 5: Remove a NodePool

When you no longer need a NodePool, remove it carefully.

**Important:** Only remove after workloads are rescheduled to other pools!

```bash
# Option A: Direct deletion
oc delete nodepool -n clusters example-hcp-infra

# Option B: Via Git (recommended)
# Remove operations/04-add-nodepool.yaml from your repo
# Or remove the NodePool from your kustomization
# Commit and push
```

Monitor the removal:
```bash
oc get nodepool -n clusters -w
oc get nodes -w
```

Expected timeline:
- Pods evicted: 2-3 minutes
- Nodes NotReady: 3-5 minutes
- VMs terminated: 3-5 minutes
- Total cleanup: 10-15 minutes

## GitOps Workflow for Scaling

The recommended approach is to use Git as the source of truth:

1. **Create a feature branch:**
   ```bash
   git checkout -b scale-cluster
   ```

2. **Make your changes:**
   ```bash
   # Edit base/nodepool.yaml or operations files
   vim base/nodepool.yaml
   ```

3. **Commit and push:**
   ```bash
   git add .
   git commit -m "scale: increase replicas to 5"
   git push origin scale-cluster
   ```

4. **Create a Pull Request:**
   - Review changes
   - Request approval
   - Merge to main

5. **Argo CD auto-syncs:**
   - (If auto-sync is enabled)
   - Detects the change
   - Applies new configuration
   - Cluster scales automatically

6. **Monitor the operation:**
   ```bash
   oc get nodepool -n clusters example-hcp-workers -w
   ```

## Important Scaling Concepts

### Horizontal Scaling (Adding/Removing Nodes)

**When to use:**
- Need more total CPU/memory capacity
- Workload requires more parallelism
- Cost-conscious (distribute load across cheaper nodes)

**Pros:**
- Simple to implement
- Linear capacity increase
- Good for stateless workloads

**Cons:**
- Can't help if pods are too large for individual nodes
- Increases operational complexity
- Higher number of nodes = more management overhead

### Vertical Scaling (Changing Node Size)

**When to use:**
- Single large job won't fit on smaller nodes
- Applications have specific memory/CPU requirements
- Consolidating workloads onto fewer, larger nodes

**Pros:**
- Reduced node count
- Simpler management
- Better for stateful workloads

**Cons:**
- Higher cost per node
- Longer replacement time
- Affects all pods on a node during replacement

### Autoscaling

**How GitOps + Autoscaler Works Together:**

1. **GitOps sets the boundaries:** `minReplicas: 2, maxReplicas: 8`
2. **Cluster Autoscaler operates within these bounds:**
   - Monitors pod scheduling failures
   - Adds nodes if pods can't be scheduled
   - Removes nodes if underutilized (10-min grace period)
3. **Both are necessary:**
   - GitOps provides governance and sustainability
   - Autoscaler provides responsiveness to load changes

**Autoscaling Prerequisites:**
- Cluster Autoscaler must be deployed in the hosted cluster
- Pod resource requests/limits must be configured
- Sufficient cluster capacity (in management cluster)

**Monitoring Autoscaling:**
```bash
# Check autoscaler logs in hosted cluster
export KUBECONFIG=/tmp/example-hcp-kubeconfig
oc logs -n kube-system -l app=cluster-autoscaler -f

# Check for scaling events
oc describe nodes | grep -A5 "Scale-Up"
```

### Multi-NodePool Best Practices

**Use multiple pools for:**
1. **Workload isolation:** Keep different types of workloads separate
2. **Resource optimization:** Different sizes for different jobs
3. **Availability:** Spread across different infrastructure
4. **Cost management:** Pay only for what you need per pool
5. **Security:** Separate security domains with different security groups

**Pool naming convention:**
- `workers` or `default`: General-purpose workloads
- `infra`: System/infrastructure services
- `compute`: Batch/computational jobs
- `storage`: Storage-heavy workloads
- `gpu`: GPU workloads (if applicable)

**Example multi-pool setup:**
```yaml
# Pool 1: General workloads
- name: default-workers
  replicas: 3
  resources: 8c/32Gi (medium)

# Pool 2: Infrastructure
- name: infra
  replicas: 2
  resources: 2c/8Gi (small)
  taint: infra=true:NoSchedule

# Pool 3: Batch jobs
- name: compute
  autoScaling: min=0, max=20
  resources: 16c/64Gi (large)
  label: workload-type=batch
```

## Verification and Monitoring

See [VALIDATION.md](VALIDATION.md) for detailed validation steps after each operation.

Quick health checks:
```bash
# Overall cluster health
oc get hostedcluster -n clusters example-hcp

# NodePool status
oc get nodepool -n clusters

# Node status
oc get nodes

# Pod scheduling
oc get pods -A --field-selector=status.phase=Pending
```

## Rollback Procedures

If a scaling operation causes problems, you can roll back:

### Via Git (Recommended):

```bash
# Revert the commit
git revert <commit-hash>
git push

# Argo CD auto-syncs to the previous state
# Monitor: oc get nodepool -n clusters -w
```

### Via kubectl:

```bash
# Patch back to previous state
oc patch nodepool -n clusters example-hcp-workers \
  --type merge -p '{"spec":{"replicas":2}}'
```

### Via Argo CD:

```bash
# Sync to a previous Git revision
argocd app set example-hcp-scaling --target-revision=<previous-commit>
argocd app sync example-hcp-scaling
```

## Troubleshooting

**Nodes stuck in NotReady:**
```bash
# Check the node events
oc describe node <node-name>

# Check VM status
oc get vm -n clusters-example-hcp

# Check HyperShift operator logs
oc logs -n hypershift -l app=operator -f
```

**Pods not scheduling during scale-up:**
```bash
# Check pending pods
oc get pods -A --field-selector=status.phase=Pending

# Check pod events
oc describe pod <pod-name> -n <namespace>

# Check node capacity
oc top nodes
oc describe nodes
```

**Autoscaler not scaling:**
```bash
# Verify autoscaler is running
oc get pods -n kube-system -l app=cluster-autoscaler

# Check autoscaler logs
oc logs -n kube-system -l app=cluster-autoscaler

# Verify pod requests are set
oc get pods -A -o json | jq '.items[] | select(.spec.containers[].resources.requests != null)'
```

**NodePool deletion is slow:**
```bash
# Check if pods are stuck in Terminating
oc get pods -A | grep Terminating

# Force delete stuck pods
oc delete pod <pod-name> --grace-period=0 --force -n <namespace>

# Check VM termination status
oc get vm -n clusters-example-hcp
```

## Next Steps

- [Scenario 4: Progressive Upgrades](../04-upgrades/) - Learn about cluster upgrades
- [Documentation: ACM with GitOps](../../../docs/gitops/00-acm-hosted-clusters-when-using-gitops.md) — hub/ACM context; this scenario is HyperShift `NodePool` scaling via Git
- [HyperShift NodePool API Reference](https://github.com/openshift/hypershift/tree/main/docs/content/how-to)

## References

- [HyperShift NodePool Documentation](https://hypershift-docs.netlify.app/)
- [Kubernetes Cluster Autoscaler](https://github.com/kubernetes/autoscaler)
- [Pod Disruption Budgets](https://kubernetes.io/docs/tasks/run-application/configure-pdb/)
