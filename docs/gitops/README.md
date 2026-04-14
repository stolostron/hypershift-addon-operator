# GitOps HCP Implementation Guide

## What is This?

This guide set sits at the intersection of **HyperShift hosted clusters**, **GitOps** (typically Argo CD applying manifests), and **Red Hat Advanced Cluster Management (ACM)**.

**Primary intent:** help teams who already use Git for `HostedCluster` / `NodePool` lifecycle understand **what ACM and MCE do next**—auto-import, `ManagedCluster` representation, hosted-mode klusterlet, policies—and what they **must still handle on the hub** (especially **secrets**, which do not belong in Git).

**Start with the ACM-centric overview:** [00-acm-hosted-clusters-when-using-gitops.md](./00-acm-hosted-clusters-when-using-gitops.md) (secrets, discovery, and mental model). The **ACM-focused** docs are **00**, **01** (integration + auto-import), and **02** (troubleshooting). **Hands-on manifests**—including HyperShift-only GitOps scenarios such as scaling and upgrades—live under [`examples/gitops-kubevirt/`](../../examples/gitops-kubevirt/).

**Key concept:** Git (or your sync tool) is one way to place desired-state API objects on the hosting cluster; **HyperShift** realizes them; **ACM** registers and manages the resulting cluster on the hub when auto-import and topology allow it.

### Who Should Read This?

- **Platform Engineers** managing HyperShift environments
- **Site Reliability Engineers** implementing cluster-as-code workflows
- **Infrastructure Teams** adopting GitOps practices
- **Operators** seeking to reduce manual cluster management

### What You'll Learn

1. **ACM + GitOps mindset** ([00](./00-acm-hosted-clusters-when-using-gitops.md)) — secrets, discovery, auto-import, hub responsibilities  
2. **ACM integration and auto-import** ([01](./01-acm-integration-overview.md)) — `ManagedCluster`, hosted-mode klusterlet, patterns, troubleshooting  
3. **GitOps examples** under [`examples/gitops-kubevirt/`](../../examples/gitops-kubevirt/) — provision, auto-import pattern demos, **HyperShift** scaling (`03-scaling/`) and upgrades (`04-upgrades/`)  
4. **ACM troubleshooting on the hub** ([02](./02-troubleshooting.md)) — auto-import, `ManagedCluster`, hosted-mode klusterlet; Argo only where it blocks the ACM path

---

## Prerequisites

### Required Knowledge

- OpenShift/Kubernetes fundamentals (pods, deployments, namespaces)
- Git and GitHub/GitLab workflows
- Basic YAML and Kubernetes manifests
- Container concepts and image registries

### Required Infrastructure

| Component | Minimum Version | Role |
|-----------|-----------------|------|
| OpenShift Hub Cluster | 4.14+ | Hosts HyperShift operator & Argo CD |
| HyperShift Operator | 4.14+ | Manages hosted control planes |
| Argo CD | 2.6+ | Synchronizes Git to cluster |
| ACM/MCE | 2.5+ | Manages cluster lifecycle & auto-import |
| Git Repository | Any | Source of truth (GitHub, GitLab, etc.) |

### Required Credentials

- SSH key for Git repository access (or token-based auth)
- Pull secret for OpenShift images (registry.redhat.io)
- Cloud provider credentials (AWS, Azure, etc. - for worker nodes)
- kubeconfig with admin access to hub cluster

### Recommended Tools

- `kubectl` (1.27+) for cluster inspection
- `oc` (OpenShift CLI) for cluster management
- `git` for version control operations
- `argocd` CLI for Argo CD operations (optional)

---

## Quick Start: Run Scenario 1 in 5 Minutes

Want to see GitOps HCP in action immediately? Here's the fastest path:

### 1. Get the Examples

```bash
git clone https://github.com/stolostron/hypershift-addon-operator.git
cd hypershift-addon-operator/examples/gitops-kubevirt/01-provision
```

### 2. Customize for Your Environment

```bash
# Edit base/hostedcluster.yaml
# - Change baseDomain to your domain
# - Set namespace if needed
# - Update pull-secret reference

# Edit base/nodepool.yaml  
# - Adjust replicas if needed
# - Update image if needed

# Edit argo-application.yaml
# - Change repoURL to your fork
# - Set target revision (main, release-4.19, etc.)
```

### 3. Apply the Argo Application

```bash
# Creates the GitOps sync definition
kubectl apply -f argo-application.yaml

# Watch Argo sync the infrastructure
argocd app wait gitops-hcp-scenario1
```

### 4. Monitor Cluster Creation

```bash
# In a new terminal, watch cluster come up
watch -n 5 kubectl get hostedcluster -A

# When Progressing=True, check node readiness
kubectl get nodes -n <hosted-cluster-namespace>
```

### 5. Verify Success

```bash
# Should show 2 running nodes
kubectl get nodes -A

# Should show successful kubeconfig secret
kubectl get secret -n <namespace> admin-kubeconfig

# Optional: Access the HCP
oc get secret -n <namespace> admin-kubeconfig -o json | \
  jq -r '.data.kubeconfig' | base64 -d > hcp-kubeconfig
kubectl --kubeconfig=hcp-kubeconfig get nodes
```

**Total time: 5-10 minutes** (varies by infrastructure readiness)

---

## Complete Documentation Navigation

### ACM-first (recommended entry)

- **[00-acm-hosted-clusters-when-using-gitops.md](./00-acm-hosted-clusters-when-using-gitops.md)** - What to keep in mind when lifecycle-managing hosted clusters from Git: **secrets vs Git**, **how ACM discovers and imports** a `HostedCluster` created by any client (including Argo), topology limits, and links to deeper material.

### Provisioning examples (outside this doc set)

- **[examples/gitops-kubevirt/01-provision/README.md](../../examples/gitops-kubevirt/01-provision/README.md)** - Scenario 1: customize manifests, apply Argo `Application`, validate cluster and ACM.

### ACM integration and operations

**After you have a `HostedCluster` (from Git or any other client), use these for ACM behavior:**

- **[01-acm-integration-overview.md](./01-acm-integration-overview.md)** - ACM integration **and** auto-import (overview, flow, key resources, minute-by-minute timeline, Full-Auto / Disabled / Hybrid patterns, troubleshooting)
- **[02-troubleshooting.md](./02-troubleshooting.md)** - ACM/MCE import and hub-side diagnostics (not HyperShift control-plane deep dives)

---

## FAQ

### General Questions

**Q: Do I need to understand Argo CD in detail?**  
A: No. This guide assumes basic Argo knowledge (apply manifests, sync). The examples show common patterns without requiring Argo expertise.

**Q: Can I use this with my own Git repository?**  
A: Yes! The examples reference GitHub, but they work with any Git provider. Update the `repoURL` in the Argo Application manifest.

**Q: Is this only for KubeVirt?**  
A: The examples use KubeVirt, but the GitOps patterns apply to AWS, Azure, or any HyperShift-supported platform. Only platform-specific fields change.

**Q: How does this compare to cluster-api or traditional IaC?**  
A: GitOps adds declarative, Git-based management with Argo sync. Cluster-api is also supported; this shows the Argo workflow specifically.

**Q: Can I mix GitOps and imperative commands?**  
A: Yes, but carefully. Prefer Git as source of truth; reconcile drift with Argo sync strategies or revert manual changes. See [02-troubleshooting.md](./02-troubleshooting.md#application-outofsync) if Argo shows unexpected drift.

---

### Scenario Selection

**Q: Do I need to run the Scenario 1 example first?**  
A: Yes, if you want a live cluster to exercise later examples. The **02–04** example directories assume a hosted cluster exists.

**Q: Can I skip the auto-import pattern examples (`02-auto-import/`)?**  
A: Yes, but [01](./01-acm-integration-overview.md) is the shortest path to understand how ACM ties in.

**Q: Which path matches my use case?**  
A: See the [decision tree](./01-acm-integration-overview.md#decision-tree) in the ACM integration doc.

**Q: How long do the examples take?**  
A: Roughly:
- Scenario 1 (provision): 30–40 min  
- Auto-import pattern demos: 20–40 min each  
- Scaling / upgrades examples: 45–120 min each

---

### Technical Questions

**Q: What happens if Git and cluster diverge?**  
A: This is "configuration drift." Argo can:
- **Sync:** Apply Git state to cluster
- **Skip:** Respect cluster state (manual changes)
- **Prune:** Delete cluster objects not in Git

See [02-troubleshooting.md](./02-troubleshooting.md#application-outofsync) and your Argo `syncPolicy` (e.g. automated sync with self-heal) for practical handling.

**Q: How does ACM auto-import work?**  
A: Controllers watch for an **Available** `HostedCluster` API object (regardless of whether Argo or `kubectl` created it), then create `ManagedCluster` and drive hosted-mode import. See [00-acm-hosted-clusters-when-using-gitops.md](./00-acm-hosted-clusters-when-using-gitops.md#how-acm-discovers-a-hostedcluster-that-was-created-via-git) and [01-acm-integration-overview.md](./01-acm-integration-overview.md#import-timeline) for the full timeline.

**Q: Can I have multiple HCPs in one Argo Application?**  
A: Yes. Typical approaches are multiple `HostedCluster` manifests in one path, Kustomize bases/overlays, or separate Argo apps per cluster. See layout notes in [examples/gitops-kubevirt/01-provision/README.md](../../examples/gitops-kubevirt/01-provision/README.md).

**Q: What's the relationship between HyperShift operator and Argo CD?**  
A: HyperShift operator manages HCP lifecycle (create, update, delete). Argo syncs manifests to the cluster. They complement each other—Argo ensures manifests match Git, HyperShift ensures HCP matches manifests.

**Q: How do I handle secrets (ssh keys, pull secrets) in Git?**  
A: Never commit those secrets in plain text. Pre-create them on the hub (or sync them with a cluster-side secret tool), then reference them by name in manifests. ACM does not replace that pattern—see [00-acm-hosted-clusters-when-using-gitops.md](./00-acm-hosted-clusters-when-using-gitops.md#secrets-what-acm-does-and-does-not-do).

---

### Troubleshooting Questions

**Q: My cluster is stuck in "Progressing" status.**  
A: That is a **HyperShift** / platform concern before ACM can import. See [HyperShift docs](https://hypershift-docs.netlify.app/) and the [Scenario 1 example README](../../examples/gitops-kubevirt/01-provision/README.md#troubleshooting).

**Q: Nodes won't become Ready.**  
A: Worker / **NodePool** issues are **HyperShift**-side. Use the hosted kubeconfig and [Scenario 1 troubleshooting](../../examples/gitops-kubevirt/01-provision/README.md#troubleshooting); see HyperShift NodePool guidance in the upstream docs.

**Q: ManagedCluster never appears in ACM.**  
A: See [02-troubleshooting.md](./02-troubleshooting.md#managedcluster-not-created) (auto-import path, addon config, logs) and [01 — Troubleshooting auto-import](./01-acm-integration-overview.md#troubleshooting-auto-import).

**Q: Argo shows "OutOfSync" even though I didn't change anything.**  
A: Likely drift from cluster operations or controllers rewriting fields. See [02-troubleshooting.md](./02-troubleshooting.md#application-outofsync) for resolution.

**Q: Where do I look for logs?**  
A: Depends on the issue. See [02-troubleshooting.md](./02-troubleshooting.md#log-locations) for a guide by component.

---

### Production Questions

**Q: Is this production-ready?**  
A: The examples are production-grade. They use current APIs, best practices, and safety checks. Always test on non-prod first.

**Q: How do I implement change control?**  
A: Use Git branches and pull requests so infrastructure changes are reviewed before merge; Argo (or your sync tool) applies from the protected branch.

**Q: How do I handle multi-region deployments?**  
A: Use one Argo `Application` (or equivalent) per region or per cluster, each pointing at Git paths or overlays with region-specific values (base domain, credentials secret names, pools, and so on).

**Q: What's the disaster recovery story?**  
A: Git is your backup. Cluster state is reproducible from Git. See [02-troubleshooting.md](./02-troubleshooting.md) for procedures (Git remains your declarative source of truth to rebuild from).

**Q: How do I monitor cluster health?**  
A: Use HyperShift status conditions + ACM monitoring. See [02-troubleshooting.md](./02-troubleshooting.md#observability-commands) for commands and tools.

---

### ACM Integration Questions

**Q: What's the difference between HyperShift and ACM?**  
A: HyperShift provisions the hosted cluster (creates control plane, provisions nodes). ACM manages the cluster after creation (policies, observability, multi-cluster operations). They work together automatically through auto-import.

**Q: Do I need to manually import my hosted cluster to ACM?**  
A: Often no: when **hypershift-addon** auto-import applies on your topology, a `ManagedCluster` is created for you after the control plane is **Available** (timelines vary). Some topologies require a manual `ManagedCluster` or hub discovery setup—see [00-acm-hosted-clusters-when-using-gitops.md](./00-acm-hosted-clusters-when-using-gitops.md#how-acm-discovers-a-hostedcluster-that-was-created-via-git) and [Importing a hosted cluster with the CLI](../management/importing_hosted_cluster_cli.md).

**Q: What if auto-import fails?**  
A: See [Troubleshooting auto-import](./01-acm-integration-overview.md#troubleshooting-auto-import) in the ACM integration doc and [02-troubleshooting.md](./02-troubleshooting.md). Common issues include **`autoImportDisabled`**, topology, missing **`external-managed-kubeconfig`**, or hub → hosted API connectivity.

**Q: Can I disable ACM management?**  
A: Yes. See [Pattern 2: Disabled import](./01-acm-integration-overview.md#pattern-2-disabled-import). You can also delete the `ManagedCluster` so the cluster keeps running without ACM management.

**Q: What annotations are critical for hosted cluster import?**  
A: For **hosted mode** import, the important annotations include:
- `import.open-cluster-management.io/klusterlet-deploy-mode: "Hosted"` — run klusterlet for this cluster in hosted mode (typically on the hub)
- `import.open-cluster-management.io/hosting-cluster-name: "<managed-cluster-name-of-host>"` — which cluster hosts the control plane
- `open-cluster-management/created-via: hypershift` — indicates HyperShift-managed cluster (the hypershift-addon auto-import controller sets these when it creates the `ManagedCluster`)

When you author `ManagedCluster` YAML by hand, match the examples in [Importing a hosted cluster with the CLI](../management/importing_hosted_cluster_cli.md) and [01-acm-integration-overview.md](./01-acm-integration-overview.md#key-acm-resources).

**Q: Where does the klusterlet run for hosted clusters?**  
A: Unlike standalone clusters, the klusterlet for hosted clusters runs on the Hub cluster (not on the hosted cluster itself). It connects remotely using the external-managed-kubeconfig secret.

**Q: How do I verify auto-import succeeded?**  
A: Check ManagedCluster status: `kubectl get managedcluster <name> -o jsonpath='{.status.conditions[?(@.type=="ManagedClusterConditionAvailable")].status}'` should return `True`.

**Q: What's the external-managed-kubeconfig secret?**  
A: It's a kubeconfig created by hypershift-addon-agent in the klusterlet namespace. It points to the hosted cluster's API server and allows the klusterlet to register the cluster with ACM.

---

## Key Concepts

### GitOps

**Definition:** Infrastructure and configuration defined in Git, synchronized to clusters automatically.

**In this guide:** Argo CD watches your Git repository and applies changes to your clusters. Cluster state should match Git state.

**Benefit:** Audit trail, version history, rollback capability, code review for infrastructure.

### Hosted Control Planes

**Definition:** OpenShift control plane running on a hub cluster, worker nodes provided separately.

**In this guide:** HyperShift operator manages HCPs on a hub cluster. ACM provides cluster management.

**Benefit:** Simplified cluster provisioning, efficient resource use, centralized management.

### Auto-Import

**Definition:** Automatic discovery and management of HCPs by ACM.

**In this guide:** When HCP is created, ACM automatically creates a ManagedCluster. This enables policy, governance, and cluster operations.

**Benefit:** No manual import steps, automatic cluster lifecycle management.

---

## File Organization

All examples live in `examples/gitops-kubevirt/`:

```
examples/gitops-kubevirt/
├── 01-provision/              # Scenario 1: Provisioning
├── 02-auto-import/            # Scenario 2: Auto-Import Patterns
├── 03-scaling/                # Scenario 3: Scaling
└── 04-upgrades/               # Scenario 4: Upgrades
```

Each scenario includes:
- `README.md` - Complete guide
- `VALIDATION.md` - Step-by-step verification
- `argo-application.yaml` - GitOps manifest
- `base/` - Foundation configuration
- `operations/` or `upgrades/` - Progressive changes

---

## Next Steps

1. **ACM vs secrets vs discovery:** [00-acm-hosted-clusters-when-using-gitops.md](./00-acm-hosted-clusters-when-using-gitops.md)
2. **Provision with examples:** [examples/gitops-kubevirt/01-provision/README.md](../../examples/gitops-kubevirt/01-provision/README.md)
3. **ACM import details:** [01-acm-integration-overview.md](./01-acm-integration-overview.md) (integration + auto-import timeline and patterns)
4. **Troubleshooting:** [02-troubleshooting.md](./02-troubleshooting.md)

