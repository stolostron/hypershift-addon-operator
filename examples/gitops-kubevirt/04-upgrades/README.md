# Scenario 4: Progressive Upgrades via GitOps

This scenario demonstrates safe, controlled cluster upgrades using GitOps patterns. Learn how to upgrade control planes and nodes with minimal downtime and full GitOps traceability.

## Overview

**Goal:** Master cluster upgrades with Git as the source of truth, enabling auditable, reversible changes.

**Key concepts:**
- Control plane upgrades (safely separate from nodes)
- Node upgrade strategies (Replace vs InPlace)
- Version skew management (control plane ≤ N+2 ahead of nodes)
- Monitoring and validation during upgrades
- Rollback procedures and disaster recovery

**What this scenario covers:**
- Base state: 4.21.0 for both HC and NodePool
- Upgrade 1: Control plane 4.21.0 → 4.21.1 (without upgrading nodes)
- Upgrade 2: Nodes with Replace strategy (safe for stateless workloads)
- Upgrade 3: Nodes with InPlace strategy (better for stateful workloads)
- Upgrade 4: Rollback example (revert to 4.21.0 if needed)

## Prerequisites

- [ ] ACM/MCE installed with HyperShift addon
- [ ] Argo CD installed and configured
- [ ] KubeVirt configured
- [ ] Secrets created in `clusters` (`pull-secret`, `ssh-key`)

Recommended prerequisites:
- [ ] Completed Scenario 1: Provisioning
- [ ] Completed Scenario 3: Scaling (to understand NodePool management)
- [ ] Have a test/non-production cluster for upgrade validation
- [ ] Familiarity with OpenShift upgrade processes

## Directory Structure

```
04-upgrades/
├── README.md                             # This file
├── VALIDATION.md                         # Step-by-step upgrade validation
├── argo-application.yaml                 # Argo CD Application
├── base/
│   ├── hostedcluster.yaml               # Base HC (4.21.0)
│   └── nodepool.yaml                    # Base NodePool (4.21.0)
├── upgrades/
│   ├── 01-control-plane-upgrade.yaml    # HC 4.21.0 → 4.21.1
│   ├── 02-nodepool-upgrade-replace.yaml # NodePool Replace strategy
│   ├── 03-nodepool-upgrade-rolling.yaml # NodePool InPlace strategy
│   └── 04-rollback-example.yaml         # Revert to 4.21.0
```

## Quick Start

### Step 1: Deploy Base Configuration

First, deploy a running cluster at 4.21.0:

```bash
# Apply the Argo Application
oc apply -f argo-application.yaml

# Trigger initial sync
argocd app sync example-hcp-upgrades

# Wait for base cluster to be ready
oc get hostedcluster -n clusters example-hcp -w
oc get nodepool -n clusters example-hcp-workers -w
```

Expected: Both HC and NodePool healthy, version 4.21.0

### Step 2: Verify Base State

Before upgrading, ensure cluster health:

```bash
# Check HostedCluster health
oc get hostedcluster -n clusters example-hcp -o jsonpath='{.status.conditions[?(@.type=="Available")]}'

# Check NodePool health
oc get nodepool -n clusters example-hcp-workers

# Access hosted cluster to verify operators
export KUBECONFIG=/tmp/example-hcp-kubeconfig
oc get secret -n clusters example-hcp-admin-kubeconfig \
  -o jsonpath='{.data.kubeconfig}' | base64 -d > $KUBECONFIG

oc get clusteroperators | grep -v "True.*False.*False"
oc get nodes
```

### Step 3: Upgrade Control Plane

Now upgrade the HostedCluster from 4.21.0 to 4.21.1:

```bash
# Option A: Apply the upgrade manifest
oc apply -f upgrades/01-control-plane-upgrade.yaml

# Option B: Via Git (recommended)
# Edit base/hostedcluster.yaml
# Change image: quay.io/openshift-release-dev/ocp-release:4.21.0-multi
#         to: quay.io/openshift-release-dev/ocp-release:4.21.1-multi
# Commit and push
```

Monitor the upgrade:
```bash
oc get hostedcluster -n clusters example-hcp -w
oc get clusterversion  # on the hosted cluster: control plane version
oc get pods -n clusters-example-hcp -w  # control plane components
```

Expected timeline:
- etcd backup: 2-5 minutes
- etcd upgrade: 5-10 minutes
- API server restart: 3-5 minutes
- Controller restart: 3-5 minutes
- **Total: 15-30 minutes**

### Step 4: Verify Control Plane Upgrade

After HC Available=True and Progressing=False:

```bash
# Check control plane version
oc get hostedcluster -n clusters example-hcp -o jsonpath='{.spec.release.image}'
# Should show 4.21.1

# Verify control plane is healthy
oc get hostedcluster -n clusters example-hcp -o jsonpath='{.status.conditions[?(@.type=="Available")]}'
# Should show status: "True"

# Check cluster version in hosted cluster
export KUBECONFIG=/tmp/example-hcp-kubeconfig
oc get clusterversion
# Desired version should be 4.21.1
```

### Step 5: Upgrade NodePool - Choose Strategy

Now upgrade nodes. Choose based on your workload:

#### Option A: Replace Strategy (Faster, for Stateless Workloads)

```bash
oc apply -f upgrades/02-nodepool-upgrade-replace.yaml

# Or via Git:
# Edit base/nodepool.yaml
# Keep upgradeType: Replace
# Change release.image to 4.21.1
# Commit and push
```

Timeline: **15-30 minutes for 2 nodes** (5-10 min per node)

Best for:
- Stateless workloads (web apps, APIs, batch jobs)
- Sufficient capacity to handle workload consolidation
- Want faster upgrade completion

```bash
# Watch specific progress
oc get nodepool -n clusters example-hcp-workers -w
oc get nodes -w
```

#### Option B: InPlace Strategy (Smoother, for Stateful Workloads)

```bash
oc apply -f upgrades/03-nodepool-upgrade-rolling.yaml

# Or via Git:
# Edit base/nodepool.yaml
# Change upgradeType: Replace → upgradeType: InPlace
# Add: management.maxUnavailable: 1
# Change release.image to 4.21.1
# Commit and push
```

Timeline: **20-40 minutes for 2 nodes** (10-20 min per node)

Best for:
- Stateful workloads (databases, statefulsets)
- Want smoother user experience
- Can afford temporary 2x capacity during rolling update

```bash
# Check rolling update progress
oc get nodes -w
oc get pods -A -o wide  # Observe workload migration
```

### Step 6: Verify Node Upgrade Complete

After all nodes are Ready with new version:

```bash
# Check NodePool status
oc get nodepool -n clusters example-hcp-workers
# Should show: Ready: 2/2, Updated: 2/2, Available: 2/2

# Check nodes in hosted cluster
export KUBECONFIG=/tmp/example-hcp-kubeconfig
oc get nodes -o wide
# All nodes should show 4.21.1 in STATUS

# Verify cluster operators
oc get clusteroperators | grep -v "True.*False.*False"
# All should be AVAILABLE

# Check cluster version
oc get clusterversion
# Status should show cluster fully updated
```

### Step 7: Validate Workload Health

After all operators available:

```bash
# Check all pods running
oc get pods -A | grep -v Running
# Only completed/succeeded pods should be shown

# Verify persistent volumes
oc get pvc -A

# Test application functionality
# (Application-specific validation)
```

## Understanding Upgrade Strategies

### Control Plane Upgrade (Always Separate)

**Why separate from node upgrades?**
- Control plane is critical infrastructure
- Node replacement/upgrade doesn't require control plane downtime
- Control plane upgrades can proceed while nodes run older version (N-1 skew supported)
- Allows phased approach to minimize overall cluster downtime

**OpenShift Version Skew Policy:**
- Control plane: N (current version)
- Nodes: N-1 or N (can be 1 version behind)
- Control plane can manage N+2 behind nodes (but not recommended for >N-1)

**Safe approach:**
1. Upgrade control plane
2. Wait for stability check (5-10 minutes)
3. Upgrade nodes
4. Complete within 2-3 weeks for full upgrade

### Node Upgrade Strategies

#### Replace Strategy

**What happens:**
1. New nodes created with target OS/image
2. Old nodes marked unschedulable (no new pods)
3. Pods evicted from old nodes
4. Pods rescheduled to new nodes (or others if capacity exists)
5. Old nodes deleted once empty
6. Repeat for next node

**Timeline per node:**
- Mark unschedulable: 1-2 minutes
- Pod eviction: 5-10 minutes
- New node provisioning: 5-10 minutes
- New node boot and join: 5-10 minutes
- **Total: 15-30 minutes per node**

**Pros:**
- Faster (no overlap period)
- Guaranteed fresh state
- Simpler logic

**Cons:**
- Requires capacity to absorb all pods
- More disruptive for persistent storage
- All pods evicted at once per node

**Best practices:**
- Configure Pod Disruption Budgets (PDBs)
- Ensure sufficient cluster capacity
- Schedule during maintenance windows
- Have backup/restore procedures ready

```yaml
# Example PDB for critical apps
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: critical-app-pdb
spec:
  minAvailable: 1
  selector:
    matchLabels:
      app: critical-app
```

#### InPlace Strategy with RollingUpdate

**What happens:**
1. New node created alongside old
2. Scheduler tries to move workloads to new node
3. Once workloads on new node, old node cordoned
4. Remaining pods forced to migrate
5. Old node drained and deleted
6. Repeat for next node

**Timeline per node:**
- New node creation: 5-10 minutes
- Workload migration: 5-10 minutes
- Old node drain: 5-10 minutes
- Total: **10-20 minutes per node**

**Pros:**
- Smoother user experience
- Fewer evictions per pod
- Better for stateful workloads
- More graceful handling of persistent storage

**Cons:**
- Requires temporary capacity (running 2N nodes briefly)
- Longer overall upgrade time
- More complex upgrade logic

**Configuration options:**
```yaml
management:
  upgradeType: InPlace
  maxUnavailable: 1      # Max 1 node unavailable during rolling
  # Can also use maxSurge (not always supported)
  # maxSurge: 1          # Allow 1 extra temporary node
```

**maxUnavailable strategies:**
- `maxUnavailable: 0`: Never drop below current ready count (slowest, safest)
- `maxUnavailable: 1`: At most 1 node down (recommended default)
- `maxUnavailable: 2`: At most 2 nodes down (faster, less safe)

## GitOps Workflow for Upgrades

The recommended GitOps workflow ensures auditability and enables easy rollback:

### Workflow Steps

```
1. Feature branch
   git checkout -b upgrade-4.21.1

2. Update base files
   vim base/hostedcluster.yaml    # Change image version
   vim base/nodepool.yaml          # Change image version, upgrade strategy

3. Commit changes
   git add .
   git commit -m "chore: upgrade to 4.21.1"

4. Create PR
   git push origin upgrade-4.21.1
   # Create pull request

5. Review & Approve
   # Team reviews upgrade notes, compatibility, etc.
   # Run test upgrade on dev cluster

6. Merge to main
   git merge upgrade-4.21.1

7. Argo syncs
   # (If auto-sync enabled)
   # Or manual: argocd app sync example-hcp-upgrades

8. Validate
   # Run full validation steps from VALIDATION.md

9. Close PR / Archive branch
    git branch -d upgrade-4.21.1
```

### Pre-Upgrade Checklist

Create a PR template or checklist:

```markdown
## Upgrade Checklist

### Planning
- [ ] Scheduled maintenance window
- [ ] Communicated with users
- [ ] Backup recent etcd snapshot
- [ ] Have rollback plan
- [ ] Notify support/ops team

### Testing
- [ ] Tested on dev cluster (same version)
- [ ] Validated all dependent components
- [ ] Checked CVE announcements for target version
- [ ] Review release notes for breaking changes

### Preparation
- [ ] All applications have PDBs
- [ ] Verified cluster capacity
- [ ] Checked resource limits/requests
- [ ] Confirmed image pull credentials valid
- [ ] Documentation updated

### Execution
- [ ] Control plane upgrade successful
- [ ] Verified control plane health (5-10 min)
- [ ] NodePool upgrade started
- [ ] Monitoring active
- [ ] Team available during process

### Validation
- [ ] All nodes Ready
- [ ] All operators available
- [ ] No pending pods
- [ ] Applications responsive
- [ ] No data loss
- [ ] Performance acceptable

### Post-Upgrade
- [ ] Close PR
- [ ] Update upgrade documentation
- [ ] Notify users of completion
- [ ] Archive relevant CLI or hub logs if you need an audit trail
- [ ] Schedule next upgrade (if applicable)
```

## Monitoring Upgrades

Use `oc` / `kubectl` watches (and your usual dashboards) while upgrades run:

```bash
# Watch HostedCluster
oc get hostedcluster -n clusters example-hcp -w

# Watch NodePool
oc get nodepool -n clusters example-hcp-workers -w

# Watch nodes
oc get nodes -w

# Check specific conditions
oc describe hostedcluster -n clusters example-hcp
oc describe nodepool -n clusters example-hcp-workers

# Monitor control plane pods
oc get pods -n clusters-example-hcp -w

# Check for problematic pods
oc get pods -A --field-selector=status.phase=Failed
oc get pods -A --field-selector=status.phase=Pending

# Monitor events
oc get events -n clusters --sort-by='.lastTimestamp' -w
```

## Handling Upgrade Issues

### Control Plane Stuck Updating

If HostedCluster shows Progressing=True for > 45 minutes:

```bash
# Check control plane logs
oc logs -n clusters-example-hcp -l component=etcd -f
oc logs -n clusters-example-hcp -l app=kube-apiserver -f

# Check operator logs
oc logs -n hypershift deployment/operator -f

# Check resource availability
oc get pvc -n clusters-example-hcp
oc describe pvc -n clusters-example-hcp

# Look for error events
oc get events -n clusters-example-hcp --sort-by='.lastTimestamp'
```

Common causes:
- Image pull failure: Check credentials, network connectivity
- Storage full: Check PVC capacity
- Network issues: Check connectivity between nodes
- Resource constraints: Insufficient CPU/memory for control plane

### Nodes Stuck NotReady

If nodes don't become ready after 20+ minutes:

```bash
# Check VM status (in management cluster)
oc get vm -n clusters-example-hcp -w

# Check VM details
oc describe vm <vm-name> -n clusters-example-hcp

# Check node initialization status
oc get nodepool -n clusters example-hcp-workers -o yaml | grep -A10 conditions

# Check node logs (from management cluster)
oc logs -n hypershift -l app=operator -f --tail=100

# Examine specific node
oc describe node <node-name>
oc describe nodepool -n clusters example-hcp-workers
```

Common causes:
- Image pull failure: Check pull-secret, image availability
- Network configuration: Check networking pods
- Storage issues: Check persistent volume attachment
- Insufficient capacity: Check management cluster resources

### Pods Stuck Pending/Terminating

```bash
# Find stuck pods
oc get pods -A --field-selector=status.phase=Pending
oc get pods -A --field-selector=status.phase=Terminating

# Check pod details
oc describe pod <pod-name> -n <namespace>

# Check for PDB blocking
oc get pdb -A
oc describe pdb <pdb-name> -n <namespace>

# Force cleanup (last resort)
oc delete pod <pod-name> -n <namespace> --grace-period=0 --force
```

## Rollback Procedures

Rollback is complex and risky. Only attempt if:
1. Cluster is still functional
2. You have expert guidance/support
3. Few workloads have migrated

### Simple Rollback (via Git)

If upgrade just started and you want to abort:

```bash
# Revert the commit
git revert <upgrade-commit-hash>
git push

# Argo auto-syncs to previous state (if enabled)
# Or manually sync
argocd app sync example-hcp-upgrades

# Monitor (example watches)
oc get hostedcluster -n clusters example-hcp -w
```

### Node-Only Rollback

If control plane upgraded but nodes should rollback:

```bash
# Edit base/nodepool.yaml: set release.image back to quay.io/openshift-release-dev/ocp-release:4.21.0-multi
# (Do not re-apply upgrades/02-*.yaml for rollback — those manifests target 4.21.1-multi.)
# Then sync from Git, or patch directly:
oc patch nodepool -n clusters example-hcp-workers \
  --type merge -p '{"spec":{"release":{"image":"quay.io/openshift-release-dev/ocp-release:4.21.0-multi"}}}'

# Monitor replacement
oc get nodepool -n clusters example-hcp-workers -w
oc get nodes -w

# Verify nodes rejoin at 4.21.0
export KUBECONFIG=/tmp/example-hcp-kubeconfig
oc get nodes -o wide
```

Timeline: 15-30 minutes (same as forward upgrade)

### Full Rollback (NOT RECOMMENDED)

Rolling back control plane:

```bash
# ONLY with support guidance!
# Risks:
# - etcd corruption
# - Cluster instability
# - Data loss
# - Unrecoverable state

# If you absolutely must:
oc patch hostedcluster -n clusters example-hcp \
  --type merge -p '{"spec":{"release":{"image":"quay.io/openshift-release-dev/ocp-release:4.21.0-multi"}}}'

# Monitor (expect issues)
oc get clusterversion -w
oc get pods -n clusters-example-hcp -w

# Verify
oc get hostedcluster -n clusters example-hcp -o yaml | grep -A20 conditions
```

**Do NOT rollback control plane without expert support!**

## Best Practices for Safe Upgrades

1. **Always upgrade control plane first**
   - Nodes can lag behind by 1-2 versions
   - Control plane cannot be downgraded safely

2. **Use Replace strategy for stateless workloads**
   - Faster upgrade
   - Cleaner state
   - Better for cattle (easily replaceable) workloads

3. **Use InPlace for stateful workloads**
   - Smoother migration
   - Better for pets (unique, important) workloads
   - Requires extra capacity temporarily

4. **Configure Pod Disruption Budgets**
   - Protects critical apps during node replacement
   - Prevents cascading failures
   - Required for safe Replace upgrades

5. **Have sufficient capacity**
   - Replace: 2x normal nodes available briefly
   - InPlace: 3x normal nodes available briefly
   - Check before upgrading

6. **Monitor continuously**
   - Watch **HostedCluster**, **NodePool**, and hosted **ClusterVersion** / nodes (see [Monitoring Upgrades](#monitoring-upgrades))
   - Have alert rules configured
   - Team available during process

7. **Validate thoroughly**
   - Follow VALIDATION.md checklist
   - Run application health checks
   - Test critical user journeys

8. **Have rollback plan**
   - Understand rollback risks
   - Have backup procedure
   - Coordinate with support

## Upgrade Timing

Recommend these intervals between major version upgrades:

```
4.16 → 4.17: 3-6 months
4.17 → 4.18: 3-6 months
4.18 → 4.19: 3-6 months
```

Do NOT skip major versions (e.g., 4.16 → 4.18 not supported).

Patch-level upgrades within the same minor (for example 4.21.0 → 4.21.1, as in this scenario) can be done more frequently than minor-to-minor jumps; still follow your change window and release-notes process.

## Next Steps

- [Scenario 1: Provisioning](../01-provision/) - Review base provisioning
- [Scenario 3: Scaling](../03-scaling/) - Review scaling patterns
- [Documentation: ACM with GitOps](../../../docs/gitops/00-acm-hosted-clusters-when-using-gitops.md) — hub/ACM context; this scenario is HyperShift release upgrades via Git
- [OpenShift Upgrade Documentation](https://docs.openshift.com/container-platform/)
- [HyperShift Upgrade Guide](https://hypershift-docs.netlify.app/)

## References

- [OpenShift Release Notes](https://docs.openshift.com/container-platform/latest/release_notes/)
- [OpenShift Upgrade Paths](https://access.redhat.com/articles/4583258)
- [Pod Disruption Budgets](https://kubernetes.io/docs/tasks/run-application/configure-pdb/)
- [HyperShift NodePool API](https://github.com/openshift/hypershift/blob/main/api/hypershift/v1beta1/nodepool_types.go)
