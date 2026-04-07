# Hosted clusters, GitOps, and ACM: what to keep in mind

## What this document is for

If you **create, update, or delete** `HostedCluster` / `NodePool` objects with GitOps (Argo CD, Flux, or any tool that applies manifests), the **HyperShift operator** still does the provisioning work. **ACM (and MCE) answer different questions:** membership on the hub, agents, policies, console inventory, and hosted-mode registration.

This page is **ACM-centric**: it explains what happens after manifests land on the cluster, what ACM does **not** solve, and how **discovery / auto-import** relate to the fact that Git was the delivery mechanism.

The step-by-step GitOps examples under `examples/gitops-kubevirt/` stay in the other guides. Read this first if your questions sound like:

- “I cannot put pull secrets, SSH keys, or cloud credentials in Git—what should I do, and does ACM help?”
- “When Argo creates my `HostedCluster`, how does ACM **discover** it and **import** it?”

---

## Mental model: three different concerns

| Concern | Who owns it | Role |
|--------|-------------|------|
| **Desired cluster shape** | You (Git + sync tool) | Declares `HostedCluster`, `NodePool`, routes, optional labels, and **references** to secrets by name. |
| **Sensitive material** | Hub cluster (and your secret platform) | Pull secrets, SSH keys, infrastructure kubeconfigs, encryption keys, etc. Live on the API server, **not** in Git. |
| **Hub management plane** | ACM / MCE | Registers the hosted cluster as a `ManagedCluster`, runs hosted-mode klusterlet where applicable, wires kubeconfigs for agents, enables add-ons and policies. |

GitOps changes **how** API objects get onto the hosting cluster; it does not change **what** HyperShift and ACM controllers do once those objects exist.

---

## Secrets: what ACM does and does not do

### What ACM does *not* do

ACM **does not** replace a secrets vault and **does not** give you a supported pattern to commit pull secrets, SSH private keys, or cloud credentials to a Git repository.

Those secrets are required by **HyperShift** so the control plane and workers can pull images, boot nodes, and talk to your platform. They must exist (or be materialized) **on the hosting cluster** before or when the `HostedCluster` reconciles.

### What you should do instead

Common patterns (same as any Kubernetes GitOps workflow):

1. **Create secrets out of band on the hub** (one-time or automation outside Git): `oc create secret generic …` in the namespace where the `HostedCluster` expects them, then in Git only set `spec.pullSecret.name`, `spec.sshKey.name`, `platform.*.credentials`, and so on.
2. **Use a secret sync mechanism on the cluster**: for example External Secrets Operator, vault agents, or Sealed Secrets so Git contains **references** or encrypted blobs acceptable to your security model—not raw `registry.redhat.io` pull configs in plain text.
3. **Bootstrap pipeline**: CI or a controlled job applies Secret objects to the hub; Git only carries non-sensitive YAML.

### Where ACM fits

ACM helps **after** the hosted control plane exists and import runs: policies, governance, applications across managed clusters, inventory. Some teams use **ACM governance** to *detect* missing or invalid configuration (for example policies around namespaces or secrets); that is **compliance and visibility**, not a substitute for **provisioning-time** credentials for HyperShift.

**`external-managed-kubeconfig` and other registration secrets** are created by the **hypershift-addon** / import pipeline for agent registration. That is separate from your **cluster install** pull secret and platform credentials.

---

## How ACM discovers a `HostedCluster` that was created via Git

From ACM’s perspective, **Git is irrelevant**: something (Argo, `kubectl`, a pipeline) created a **`HostedCluster` object in the Kubernetes API**. Controllers react to **that object** and its status.

### Default path: hypershift-addon **auto-import** (hub / local hosting)

On the hosting cluster where **MCE hypershift-addon** runs, the **AutoImport** controller watches **`HostedCluster`** resources. For each new hosted cluster whose **control plane is Available**, it can create hub-side objects so ACM can manage it.

Implementation highlights (see `pkg/agent/auto_import_controller.go` in this repository):

1. **Auto-import can be disabled** on the hypershift-addon agent by setting **`autoImportDisabled: "true"`** in the addon **`AddOnDeploymentConfig`** (`hypershift-addon-deploy-config` in the **`multicluster-engine`** namespace), which surfaces as **`DISABLE_AUTO_IMPORT`** on the deployment—if `ManagedCluster` is never created, check that first.
2. The controller only performs this path when the addon instance is associated with the **local / self-managed** hosting cluster (the “hosting HCP on the same cluster where hub agents run” style topology assumed by the GitOps examples). Other topologies may require **manual** `ManagedCluster` YAML or hub discovery flows; see [Hosting cluster topologies](../planning/hosting_cluster_topologies.md) and [Discovering and managing hosted clusters with ACM](../management/discovering_hostedclusters.md).
3. It **waits** until the hosted **control plane is available** (`HostedCluster` status), then creates a **`ManagedCluster`** with hosted import annotations, including:
   - `import.open-cluster-management.io/klusterlet-deploy-mode: "Hosted"`
   - `import.open-cluster-management.io/hosting-cluster-name: <hosting cluster name>`
   - `open-cluster-management/created-via: hypershift`
4. If the **ACM operator** is detected on the cluster, it also creates **`KlusterletAddonConfig`** so standard add-ons can be enabled.

After the `ManagedCluster` exists, **cluster import / klusterlet** machinery runs (hosted mode: agents run on the hub namespace and use kubeconfigs aimed at the hosted API). The sequence, diagrams, timelines, and patterns are in [01-acm-integration-overview.md](./01-acm-integration-overview.md).

### Important detail for operators

Auto-import is driven by **HostedCluster** lifecycle and status, **not** by Argo `Application` status. If Argo shows Synced but `ManagedCluster` never appears, debug **HyperShift** (Is the control plane Available?), **hypershift-addon** logs, **`AddOnDeploymentConfig` / `autoImportDisabled`**, and **topology** (whether you expected local auto-import vs multi-cluster hub).

---

## Lifecycle: create, update, delete

### Create

Git (or a sync tool) applies `HostedCluster` → HyperShift reconciles → when the control plane is **Available**, auto-import may create `ManagedCluster` (+ `KlusterletAddonConfig`). Expect a delay after the API is up.

### Update

Changing Git and re-syncing updates the **`HostedCluster` / `NodePool`**; HyperShift handles rolling changes. ACM continues to represent the same **managed cluster name** (aligned with hosted cluster naming in the default flow). Day-2 **scale and upgrade** mechanics are HyperShift/GitOps concerns—see the example trees under [`examples/gitops-kubevirt/03-scaling/`](../../examples/gitops-kubevirt/03-scaling/) and [`examples/gitops-kubevirt/04-upgrades/`](../../examples/gitops-kubevirt/04-upgrades/)—while **ACM** surfaces version and capacity through **`ManagedCluster`** / **`ManagedClusterInfo`** once the cluster is imported.

### Delete

Removing the `HostedCluster` from Git (and pruning with Argo if configured) starts HyperShift **deletion** of that hosted cluster. **ManagedCluster** cleanup and import workflows are separate resources: confirm your operational runbook (whether you delete `ManagedCluster` explicitly, rely on controllers, or use policies). If in doubt, see [02-troubleshooting.md](./02-troubleshooting.md) and [01-acm-integration-overview.md](./01-acm-integration-overview.md#troubleshooting-auto-import).

---

## Where to read next

| Topic | Document |
|--------|-----------|
| ACM import flow, klusterlet hosted mode, timelines, patterns, `external-managed-kubeconfig` | [01-acm-integration-overview.md](./01-acm-integration-overview.md) |
| Manual `ManagedCluster` YAML (when auto-import does not apply) | [Importing a hosted cluster with the CLI](../management/importing_hosted_cluster_cli.md) |
| Hub ↔ many MCE clusters, discovery at scale | [Discovering and managing hosted clusters with ACM](../management/discovering_hostedclusters.md) |
| Examples and Git-oriented setup | [GitOps guide README](./README.md) (quick start), [Scenario 1 examples](../../examples/gitops-kubevirt/01-provision/README.md) |
