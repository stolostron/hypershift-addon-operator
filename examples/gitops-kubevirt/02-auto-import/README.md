# Scenario 2: Auto-Import Patterns

This scenario explores three different approaches to integrating HostedClusters with ACM (Advanced Cluster Management). Each pattern represents a different balance between automation and GitOps control.

## Overview

When a **HostedCluster** becomes **Available**, **hypershift-addon** (MCE/ACM stack) can **auto-import** it by creating a **`ManagedCluster`**, unless auto-import is turned off on the hub **`AddOnDeploymentConfig`**. This scenario compares three ways to relate that behavior to Git:

1. **Full-Auto**: Minimal Git; let the addon create and manage **ManagedCluster**
2. **Disabled**: Set **`autoImportDisabled`** on the addon; declare **ManagedCluster** (and addons) in Git
3. **Hybrid**: Keep auto-import; manage **ManagedClusterSet**, **Placement**, policies, and related objects in Git

## Pattern Comparison

### 1. Full-Auto Pattern

**Description:** Let ACM automatically create and manage the ManagedCluster resource.

| Aspect | Detail |
|--------|--------|
| **HostedCluster Annotation** | None (auto-import enabled by default) |
| **ManagedCluster** | Auto-created by ACM, not in Git |
| **Control** | Fully automated |
| **GitOps Compliance** | Medium - HostedCluster in Git, ManagedCluster auto-created |
| **Best For** | Demos, dev environments, rapid prototyping |
| **Complexity** | Low |

**Pros:**
- Minimal YAML to maintain
- Fully automated cluster discovery
- Perfect for ephemeral/dev environments
- Zero manual configuration after HostedCluster creation

**Cons:**
- ManagedCluster not under Git control
- Cannot pre-configure ACM integrations
- Less predictable in GitOps workflows
- Harder to implement cluster templates

**Use Case:**
```
Perfect for teams that:
- Need quick cluster provisioning
- Don't require pre-configured ACM policies
- Accept automation trade-offs for simplicity
```

### 2. Disabled Pattern

**Description:** Disable auto-import on the **hypershift-addon** (hub **`AddOnDeploymentConfig`**, **`autoImportDisabled`**) and manage **ManagedCluster** (and **KlusterletAddonConfig**) explicitly in Git.

| Aspect | Detail |
|--------|--------|
| **Auto-import** | Off: **`autoImportDisabled: "true"`** on **`AddOnDeploymentConfig`** `hypershift-addon-deploy-config` (`multicluster-engine`) → **`DISABLE_AUTO_IMPORT`** on the addon |
| **ManagedCluster** | Explicit YAML in Git |
| **Control** | Full manual control |
| **GitOps Compliance** | High - Everything in Git |
| **Best For** | Strict GitOps environments, production |
| **Complexity** | Medium |

**Pros:**
- Complete Git-based control
- Can customize ManagedCluster before creation
- Predictable cluster registration
- Full audit trail in Git
- Can use GitOps for cluster configuration

**Cons:**
- More YAML to maintain
- Must manually create ManagedCluster
- Coordination overhead between HC and MC
- More complex deployment procedure

**Use Case:**
```
Perfect for teams that:
- Follow strict GitOps practices
- Need audit trails and version control
- Want to pre-configure ACM integrations
- Run compliance-heavy environments
```

### 3. Hybrid Pattern (Recommended)

**Description:** Auto-import the ManagedCluster, but manage higher-level ACM resources in Git.

| Aspect | Detail |
|--------|--------|
| **HostedCluster Annotation** | None (auto-import enabled) |
| **ManagedCluster** | Auto-created by ACM |
| **ACM Resources** | ManagedClusterSet, Placement, Policies in Git |
| **Control** | Balanced automation + explicit control |
| **GitOps Compliance** | High - Core resources in Git, MC auto-created |
| **Best For** | Production fleet management |
| **Complexity** | Medium-High |

**Pros:**
- Leverages ACM automation
- Cluster organization (ManagedClusterSet) in Git
- Policy-driven cluster management in Git
- Scalable for large environments
- Production-ready approach
- Can define templates and constraints

**Cons:**
- Requires understanding of ACM resources
- Split responsibility (some auto, some manual)
- More complex validation and testing
- Coordination between multiple resource types

**Use Case:**
```
Perfect for teams that:
- Manage multiple clusters (fleet management)
- Want to organize clusters by function/env
- Need policy-driven governance
- Balance automation with control
- Run production environments
```

## Decision Tree

Choose your pattern based on these questions:

```
1. Do you need strict GitOps compliance?
   ├─ YES → Go to Question 2
   └─ NO → Use Full-Auto (Simplest option)

2. Must everything be in Git, including ManagedCluster?
   ├─ YES → Use Disabled Pattern (Full control)
   └─ NO → Go to Question 3

3. Do you manage multiple clusters (fleet management)?
   ├─ YES → Use Hybrid Pattern (Recommended)
   └─ NO → Use Disabled Pattern (Still good for single cluster)
```

## Quick Comparison Table

| Feature | Full-Auto | Disabled | Hybrid |
|---------|-----------|----------|--------|
| **Automation** | High | None | Medium |
| **GitOps Compliance** | Medium | High | High |
| **YAML Files** | 3 | 6 | 7+ |
| **Setup Time** | ~5 min | ~10 min | ~15 min |
| **Production Ready** | No | Yes | Yes (Recommended) |
| **Fleet Ready** | No | No | Yes |
| **Learning Curve** | Easy | Medium | Medium-Hard |

## Pattern Details

Each pattern has its own directory with complete working examples:

### Full-Auto Pattern
- **Directory:** `full-auto/`
- **Files:** HostedCluster, NodePool, Argo Application, watch script
- **Focus:** Simplicity and automation
- **Time to Deploy:** ~30 minutes
- **Read:** [full-auto/README.md](full-auto/README.md)

### Disabled Pattern
- **Directory:** `disabled/`
- **Files:** HostedCluster, NodePool, ManagedCluster, KlusterletAddonConfig, Argo Application
- **Focus:** Full GitOps control
- **Time to Deploy:** ~35 minutes
- **Read:** [disabled/README.md](disabled/README.md)

### Hybrid Pattern
- **Directory:** `hybrid/`
- **Files:** HostedCluster, NodePool, ManagedClusterSet, Placement, Policy, Argo Application
- **Focus:** Scalable production fleet management
- **Time to Deploy:** ~40 minutes
- **Read:** [hybrid/README.md](hybrid/README.md)

## Implementation Sequence

If you're new to these patterns, we recommend this order:

1. **Start with Full-Auto** (5-10 min reading + 30 min deployment)
   - Understand basic auto-import mechanism
   - See minimal YAML requirements
   - Learn how ACM discovers HostedClusters

2. **Progress to Disabled** (10-15 min reading + 35 min deployment)
   - Understand explicit ManagedCluster control
   - Learn GitOps best practices
   - Practice cluster registration workflows

3. **Master Hybrid** (15-20 min reading + 40 min deployment)
   - Understand fleet management concepts
   - Learn about Placements and Policies
   - Implement production patterns

## Common Configuration

All three patterns share these specifications:

| Component | Specification |
|-----------|---------------|
| **OpenShift Version** | 4.21.0-multi (see each pattern's `hostedcluster.yaml` / `nodepool.yaml`) |
| **Platform** | KubeVirt |
| **NodePool Replicas** | 2 |
| **CPU per Worker** | 4 cores |
| **Memory per Worker** | 16 GiB |
| **Root Volume** | 120 GiB |

## Prerequisites

Before starting any pattern, ensure:

1. ✓ Secrets created: `pull-secret`, `ssh-key` in `clusters` namespace
2. ✓ Argo CD installed and accessible
3. ✓ MCE with HyperShift addon running
4. ✓ KubeVirt configured
5. ✓ Sufficient compute resources (32+ CPUs, 64+ GB RAM)

## Testing Each Pattern

Each pattern can be tested independently:

```bash
# Test Full-Auto
cd full-auto
oc apply -f argo-application.yaml
# Monitor with watch script

# Test Disabled (in separate namespace)
cd ../disabled
# Edit argo-application.yaml to use different namespace
oc apply -f argo-application.yaml
# Monitor ManagedCluster creation

# Test Hybrid (in third namespace)
cd ../hybrid
oc apply -f argo-application.yaml
# Monitor Placement decisions
```

**Important:** Deploy each pattern in different namespaces to avoid conflicts.

## Monitoring and Validation

Each pattern includes validation steps in its README. Key things to watch:

**Full-Auto:**
- Watch ManagedCluster auto-creation: `oc get managedcluster <name> -w` (or `oc get mc -w`)
- Watch **HostedCluster** and **NodePool** in parallel; see [full-auto/README.md](full-auto/README.md#monitoring-auto-import)

**Disabled:**
- Verify ManagedCluster matches expectations
- Check KlusterletAddonConfig is applied
- Validate explicit Git control

**Hybrid:**
- Verify ManagedClusterSet creation
- Check Placement selects cluster
- Monitor Policy application to cluster

## Troubleshooting

Common issues and solutions:

### ManagedCluster Not Created (Full-Auto)
```bash
# Check if auto-import is disabled on the hypershift-addon (hub)
oc get addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine -o yaml | grep -A1 autoImportDisabled
```

If **`autoImportDisabled: "true"`**, the addon does not auto-create **ManagedCluster** for **new** hosted clusters after that rollout—see the [Disabled](./disabled/) pattern. Otherwise check **HostedCluster** **Available**, topology (local-cluster agent), and hypershift-addon logs; see [02-troubleshooting.md](../../../docs/gitops/02-troubleshooting.md).

### Disabled pattern: ManagedCluster never appears from Git
```bash
# Confirm hub addon config (prerequisite for disabled pattern)
oc get addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine -o yaml | grep -A1 autoImportDisabled
```

You should see **`autoImportDisabled`** with value **`"true"`** before the **HostedCluster** becomes **Available** if you expect zero auto-created **ManagedCluster**. Apply your Git **ManagedCluster** and follow [disabled/README.md](./disabled/README.md).

### Placement Not Selecting Cluster (Hybrid)
```bash
# Check Placement status
oc get placement -n clusters-<clustername> -o yaml

# Check ManagedClusterSet has cluster
oc get managedclusterset -o yaml
```

## Next Steps

1. **Read this entire document** - Understand the patterns
2. **Choose your pattern** - Based on your requirements
3. **Follow pattern-specific README** - Detailed deployment steps
4. **Validate deployment** - Each pattern has validation checklist
5. **Proceed to Scenario 3** - Learn about scaling operations

## Additional Resources

- [ACM with GitOps](../../../docs/gitops/00-acm-hosted-clusters-when-using-gitops.md) - Secrets, discovery, and hub behavior
- [Troubleshooting Guide](../../../docs/gitops/02-troubleshooting.md) - Common issues and solutions
- [ACM Documentation](https://access.redhat.com/documentation/en-us/red_hat_advanced_cluster_management_for_kubernetes/) - Official ACM docs

## Pattern Selection Summary

**Use Full-Auto if:**
- You're learning these patterns
- You want minimal YAML
- You're building demos or ephemeral clusters
- You don't need pre-configured ACM integrations

**Use Disabled if:**
- You need strict GitOps compliance
- You want everything under version control
- You manage single or small clusters
- You need full audit trails in Git

**Use Hybrid if (RECOMMENDED):**
- You manage multiple clusters (fleet)
- You need production-level reliability
- You want policy-driven governance
- You need to organize clusters by function/environment
- You want scalable cluster management

---

**Start exploring:** Choose a pattern from the links above and follow its README!
