# ACM integration and auto-import

## What This Document Covers

After HyperShift provisions a hosted cluster, ACM automatically takes over management through a process called "auto-import." This document explains:

- What ACM provides for hosted clusters
- How the integration works (high-level flow)
- Key ACM resources and their purpose
- Auto-import timeline, patterns (Full-Auto / Disabled / Hybrid), troubleshooting, and next steps to scaling, upgrades, and the troubleshooting guide

### What "auto-import" means (read this first)

In this guide, **auto-import** means the **default hub path** where you do **not** apply a separate ACM import manifest: once the hosted **control plane is Available**, **hypershift-addon** can create a **`ManagedCluster`** (with hosted-import annotations), after which ACM’s **cluster import** machinery runs **hosted-mode klusterlet** on the hub and uses **`external-managed-kubeconfig`** so agents talk to the hosted cluster API.

**Broader sense:** any time a hosted cluster ends up represented as a **`ManagedCluster`** and joined to the hub is “import.” **Auto-import** here is the **controller-driven** slice of that—discovery from the **`HostedCluster`** API object rather than you hand-writing import YAML.

**Out of scope for this word as used below:** topologies or settings where that controller path is off (for example **`autoImportDisabled: "true"`** on the hypershift-addon **`AddOnDeploymentConfig`**, which sets **`DISABLE_AUTO_IMPORT`** on the addon agent—see [Pattern 2: Disabled import](#pattern-2-disabled-import)), or flows that rely on **manual** `ManagedCluster` / hub discovery docs instead—those are still import, but not automatic in the sense above.

---

## ACM's Role in HCP Lifecycle

### What ACM Provides

Once a hosted cluster is imported, ACM provides:

#### 1. Cluster Discovery (Auto-Import)

- **Automatic detection** when hosted clusters become available
- **Registration without manual intervention** - no kubectl apply needed
- **Visibility in ACM console** - see all clusters in one place
- **Inventory management** - track cluster versions, capacity, health

#### 2. Policy & Governance

- **Configuration policies** - enforce cluster configurations
- **Compliance scanning** - audit against security standards
- **Remediation** - auto-fix policy violations
- **Audit logging** - track all policy changes

#### 3. Observability & Monitoring

- **Cluster health dashboards** - unified view of all clusters
- **Metrics collection** - CPU, memory, pod counts
- **Alerting integration** - notify on cluster issues
- **Search** - find resources across all managed clusters

#### 4. Multi-Cluster Management

- **Unified cluster view** - manage 100s of clusters from one hub
- **Application deployment** - deploy apps across cluster sets
- **Placement** - intelligent workload distribution
- **Cluster sets** - group clusters for operations

---

## The Integration Flow

```
┌────────────────────────────────────────────────────────────────────────────┐
│                          Hub Cluster                                       │
│                                                                            │
│  ┌──────────────┐         ┌──────────────┐                                 │
│  │   Argo CD    │ applies │  HyperShift  │                                 │
│  │              │────────>│   Operator   │                                 │
│  └──────────────┘         └──────┬───────┘                                 │
│                                  │                                         │
│                                  │ creates                                 │
│                                  ▼                                         │
│                        ┌──────────────────┐                                │
│                        │ Hosted Control   │                                │
│                        │ Plane Pods       │                                │
│                        │ (hosted-* ns)    │                                │
│                        └────────┬─────────┘                                │
│                                 │                                          │
│                                 │ becomes Available                        │
│                                 ▼                                          │
│                        ┌──────────────────┐                                │
│                        │ hypershift-      │                                │
│     ┌──────────────────│ addon-agent      │                                │
│     │ detects HC ready │ (ACM controller) │                                │
│     │                  └────────┬─────────┘                                │
│     │                           │                                          │
│     │ creates                   │ creates                                  │
│     ▼                           ▼                                          │
│  ┌──────────────────┐  ┌──────────────────┐                                │
│  │ ManagedCluster   │  │ external-managed-│                                │
│  │ Resource         │  │ kubeconfig       │                                │
│  └────────┬─────────┘  │ (secret)         │                                │
│           │            └────────┬─────────┘                                │
│           │                     │                                          │
│           │ triggers            │                                          │
│           ▼                     │                                          │
│  ┌──────────────────┐           │                                          │
│  │ cluster-import-  │           │                                          │
│  │ controller       │───────────┤                                          │
│  │ (ACM controller) │  deploys  │                                          │
│  └────────┬─────────┘           │                                          │
│           │                     │                                          │
│           │ creates             │                                          │
│           ▼                     │                                          │
│  ┌──────────────────┐           │                                          │
│  │ Klusterlet       │◄──────────┘                                          │
│  │ (klusterlet-* ns)│  uses kubeconfig                                     │
│  └────────┬─────────┘                                                      │
│           │                                                                │
└───────────┼────────────────────────────────────────────────────────────────┘
            │
            │ connects using external-managed-kubeconfig
            │ registers hosted cluster
            ▼
┌────────────────────────────────────────────────────────────────────────────┐
│                      Hosted Cluster (Workers)                              │
│                                                                            │
│  ┌──────────────┐         ┌──────────────┐         ┌──────────────┐        │
│  │  Worker-0    │         │  Worker-1    │         │  Worker-N    │        │
│  └──────────────┘         └──────────────┘         └──────────────┘        │
└────────────────────────────────────────────────────────────────────────────┘
```

### Step-by-Step Process

#### Step 1: HyperShift Creates Hosted Control Plane

- GitOps (Argo CD) applies HostedCluster and NodePool manifests
- HyperShift operator creates control plane pods on the Hub
- Control plane becomes Available (API server responding)
- HostedCluster status: Available=True

#### Step 2: hypershift-addon-agent Detects Ready Cluster

- ACM controller monitors HostedClusters for Available status
- Detects the new hosted cluster is ready
- Creates ManagedCluster resource with critical annotations:
  - `import.open-cluster-management.io/klusterlet-deploy-mode: "Hosted"`
  - `open-cluster-management/created-via: hypershift`

#### Step 3: cluster-import-controller Deploys Klusterlet

- Detects ManagedCluster with `klusterlet-deploy-mode: "Hosted"`
- Creates klusterlet-<name> namespace on Hub cluster
- Deploys klusterlet pods to Hub (NOT to hosted cluster)
- Klusterlet runs on Hub, manages remotely

#### Step 4: hypershift-addon-agent Creates Kubeconfig

- Creates external-managed-kubeconfig secret in klusterlet namespace
- Secret contains kubeconfig for hosted cluster API server
- Kubeconfig allows klusterlet to connect to the hosted cluster

#### Step 5: Klusterlet Registers Cluster

- Klusterlet pod reads external-managed-kubeconfig
- Connects to hosted cluster API using the kubeconfig
- Registers hosted cluster with ACM Hub
- ManagedCluster status updates: Joined=True, then Available=True

#### Step 6: Management Enabled

- ManagedClusterInfo gets populated with cluster details
- ACM add-ons deployed (if KlusterletAddonConfig configured)
- Cluster appears in ACM console
- Policies and observability enabled

---

## Key ACM Resources

### ManagedCluster

**Represents the hosted cluster in ACM**

```yaml
apiVersion: cluster.open-cluster-management.io/v1
kind: ManagedCluster
metadata:
  name: example-hcp
  annotations:
    import.open-cluster-management.io/klusterlet-deploy-mode: "Hosted"
    open-cluster-management/created-via: hypershift
    import.open-cluster-management.io/hosting-cluster-name: local-cluster
  labels:
    cloud: auto-detect
    vendor: auto-detect
spec:
  hubAcceptsClient: true
```

**Key annotations:**
- `klusterlet-deploy-mode: "Hosted"` → Klusterlet runs on Hub, not on hosted cluster
- `open-cluster-management/created-via: hypershift` → Indicates HyperShift-managed cluster (set by hypershift-addon auto-import)
- `import.open-cluster-management.io/hosting-cluster-name` → Names the managed cluster that hosts the control plane

**Key labels:**
- `vendor: auto-detect` → Auto-imported (vs manual import)
- `cloud: auto-detect` → Cloud provider auto-detected

**Example:** [examples/gitops-kubevirt/01-provision/acm/managedcluster-example.yaml](../../examples/gitops-kubevirt/01-provision/acm/managedcluster-example.yaml)

### KlusterletAddonConfig

**Configures ACM add-ons for the cluster**

Enables:
- **Application manager** - Deploy apps via ACM
- **Policy controller** - Enforce configuration policies
- **Search collector** - Make resources searchable
- **Certificate policy controller** - Certificate compliance
- **IAM policy controller** - RBAC compliance

**Example:** [examples/gitops-kubevirt/01-provision/acm/klusterletaddonconfig-example.yaml](../../examples/gitops-kubevirt/01-provision/acm/klusterletaddonconfig-example.yaml)

### ManagedClusterInfo

**Populated by klusterlet with cluster details**

Contains:
- Kubernetes/OpenShift version
- Node inventory with capacity and status
- Distribution info (OCP version, available updates)
- Console and API URLs

**Example:** [examples/gitops-kubevirt/01-provision/acm/managedclusterinfo-example.yaml](../../examples/gitops-kubevirt/01-provision/acm/managedclusterinfo-example.yaml)

---

## What Makes Hosted Clusters Different

### Klusterlet Location

**Standalone clusters:**
- Klusterlet runs ON the managed cluster
- Direct in-cluster access to resources

**Hosted clusters:**
- Klusterlet runs on the hosting Hub cluster
- Connects remotely via external-managed-kubeconfig
- More efficient (no agent overhead on hosted cluster)

### Deploy Mode Annotation

The `import.open-cluster-management.io/klusterlet-deploy-mode: "Hosted"` annotation is **critical**:

- **Present** → cluster-import-controller deploys klusterlet to Hub
- **Missing or "Default"** → Import fails (klusterlet tries to deploy to hosted cluster)

This annotation is set automatically by hypershift-addon-agent.

### External-Managed-Kubeconfig Secret

**Purpose:** Allows Hub-based klusterlet to manage the hosted cluster

**Created by:** hypershift-addon-agent  
**Location:** klusterlet-<name> namespace on Hub  
**Contains:** Kubeconfig for hosted cluster API server  

**Different from:**
- `admin-kubeconfig` - For end users to access hosted cluster
- `kubeconfig` - Internal HyperShift secret

---

## Auto-import timeline, patterns, and operations

The sections below expand on the integration flow: **minute-by-minute verification**, **Full-Auto / Disabled / Hybrid** patterns, decision guidance, enterprise notes, and **troubleshooting**. For GitOps-specific issues, see [02-troubleshooting.md](./02-troubleshooting.md).

<a id="import-timeline"></a>

## Import Timeline - The Observable Journey

This section shows the minute-by-minute progression from HostedCluster creation to full ACM management. Each phase includes observable state and verification commands.

### Auto-Import Timeline Diagram

```
Timeline: HyperShift → ACM Auto-Import (0-20 minutes)

T+0min                T+5min               T+8min               T+10min              T+15min
│                     │                    │                    │                    │
│ HostedCluster       │ Control Plane      │ ManagedCluster     │ Klusterlet         │ Registration
│ Created             │ Available          │ Created            │ Deployed           │ Complete
│                     │                    │                    │                    │
▼                     ▼                    ▼                    ▼                    ▼
┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
│ Argo CD         │  │ HyperShift      │  │ hypershift-     │  │ cluster-import- │  │ Klusterlet      │
│ applies HC/NP   │─>│ creates control │─>│ addon-agent     │─>│ controller      │─>│ registers HC    │
│ manifests       │  │ plane pods      │  │ creates         │  │ deploys         │  │ with Hub        │
│                 │  │                 │  │ ManagedCluster  │  │ klusterlet +    │  │                 │
│                 │  │                 │  │                 │  │ external-       │  │                 │
│                 │  │                 │  │                 │  │ managed-        │  │                 │
│                 │  │                 │  │                 │  │ kubeconfig      │  │                 │
└─────────────────┘  └─────────────────┘  └─────────────────┘  └─────────────────┘  └─────────────────┘
        │                     │                   │                   │                   │
    Verify:             Verify:             Verify:             Verify:             Verify:
    kubectl get hc      kubectl get pods    kubectl get         kubectl get pods    kubectl get
                        -n hosted-*         managedcluster      -n klusterlet-*     managedcluster
                                                                kubectl get secret   (status=Available)
                                                                external-managed-
                                                                kubeconfig
```

### Phase 1: HyperShift Creates Control Plane (T+0 to T+5)

**What Happens:**

1. Argo CD applies HostedCluster and NodePool manifests to the Hub
2. HyperShift operator detects new HostedCluster resource
3. HyperShift creates hosted-<name> namespace for control plane
4. Control plane pods start: etcd, kube-apiserver, controllers
5. Pull secret and SSH key mounted from pre-created secrets
6. Worker node provisioning begins (parallel process)

**Observable State:**

```bash
# Check HostedCluster status (early state)
kubectl get hostedcluster -n clusters example-hcp -o wide

# Expected output (early):
# NAME          AVAILABLE   PROGRESSING   AGE
# example-hcp   False       True          2m

# Check control plane pods being created
kubectl get pods -n clusters-example-hcp

# Expected output (in progress):
# NAME                                READY   STATUS              AGE
# etcd-0                              1/1     Running             1m30s
# kube-apiserver-0                    0/1     ContainerCreating   1m
# kube-controller-manager-0           0/1     Pending             30s
```

**Verification Commands:**

```bash
# Check detailed Available condition
kubectl get hostedcluster -n clusters example-hcp \
  -o jsonpath='{.status.conditions[?(@.type=="Available")]}' | jq

# Expected (early): status: "False", reason: "HostedClusterAsExpected"
# Expected (ready): status: "True", reason: "AsExpected"

# Check all control plane pods are Running
kubectl get pods -n clusters-example-hcp --field-selector=status.phase!=Running

# Expected (when ready): No resources found
```

**When to Move to Next Phase:** HostedCluster Available=True

---

### Phase 2: ACM Detects Hosted Cluster (T+5 to T+8)

**What Happens:**

1. hypershift-addon-agent (ACM controller) detects HostedCluster is Available
2. hypershift-addon-agent creates ManagedCluster resource
3. Annotations automatically set:
   - `import.open-cluster-management.io/klusterlet-deploy-mode: "Hosted"`
   - `import.open-cluster-management.io/hosting-cluster-name: <hosting managed cluster name>`
   - `open-cluster-management/created-via: hypershift`
4. Labels automatically set (example values):
   - `cloud: auto-detect`
   - `vendor: auto-detect`

**Observable State:**

```bash
# Check if ManagedCluster was created
kubectl get managedcluster example-hcp

# Expected output:
# NAME          HUB ACCEPTED   MANAGED CLUSTER URLS   JOINED   AVAILABLE   AGE
# example-hcp   true                                  Unknown  Unknown     10s
```

**Verification Commands:**

```bash
# Check ManagedCluster annotations (CRITICAL for hosted clusters)
kubectl get managedcluster example-hcp -o yaml | grep -A 5 annotations

# Expected:
#   annotations:
#     open-cluster-management/created-via: hypershift
#     import.open-cluster-management.io/klusterlet-deploy-mode: Hosted
#     import.open-cluster-management.io/hosting-cluster-name: <your-local-cluster-name>

# Check ManagedCluster labels
kubectl get managedcluster example-hcp -o jsonpath='{.metadata.labels}' | jq

# Expected to include:
#   "vendor": "auto-detect",
#   "cloud": "auto-detect"

# Check which controller created it (hypershift-addon-agent)
kubectl get managedcluster example-hcp \
  -o jsonpath="{.metadata.annotations['open-cluster-management/created-via']}{'\n'}"

# Expected: hypershift
```

**When to Move to Next Phase:** ManagedCluster resource exists

---

### Phase 3: Klusterlet Deployment (T+8 to T+10)

**What Happens:**

1. cluster-import-controller detects new ManagedCluster with `klusterlet-deploy-mode: "Hosted"`
2. cluster-import-controller creates klusterlet-<name> namespace on Hub
3. cluster-import-controller deploys klusterlet deployment to Hub (NOT to hosted cluster)
4. hypershift-addon-agent creates external-managed-kubeconfig secret
5. Secret contains kubeconfig pointing to hosted cluster API

**Observable State:**

```bash
# Check klusterlet namespace exists on Hub
kubectl get namespace | grep klusterlet-example-hcp

# Expected: klusterlet-example-hcp namespace listed

# Check klusterlet pods on Hub
kubectl get pods -n klusterlet-example-hcp

# Expected output:
# NAME                                READY   STATUS    AGE
# klusterlet-registration-<hash>      1/1     Running   30s
# klusterlet-work-<hash>              1/1     Running   30s

# Check external-managed-kubeconfig secret exists
kubectl get secret -n klusterlet-example-hcp external-managed-kubeconfig

# Expected: secret listed
```

**Verification Commands:**

```bash
# Verify klusterlet deployment exists
kubectl get deployment -n klusterlet-example-hcp

# Expected: At least one deployment named "klusterlet-*"

# Check external-managed-kubeconfig secret content
kubectl get secret -n klusterlet-example-hcp external-managed-kubeconfig \
  -o jsonpath='{.data.kubeconfig}' | base64 -d | head -10

# Expected: Valid kubeconfig YAML showing hosted cluster API server URL

# Validate kubeconfig points to correct cluster
kubectl get secret -n klusterlet-example-hcp external-managed-kubeconfig \
  -o jsonpath='{.data.kubeconfig}' | base64 -d | grep "server:"

# Expected: server: https://api.example-hcp.example.com:6443
```

**When to Move to Next Phase:** Klusterlet pods Running AND external-managed-kubeconfig exists

---

### Phase 4: Registration (T+10 to T+15)

**What Happens:**

1. Klusterlet pod starts on Hub cluster
2. Klusterlet reads external-managed-kubeconfig secret
3. Klusterlet connects to hosted cluster API using the kubeconfig
4. Klusterlet registers hosted cluster with ACM Hub
5. ManagedCluster status updates: Joined=True, then Available=True

**Observable State:**

```bash
# Watch ManagedCluster conditions evolve
kubectl get managedcluster example-hcp \
  -o jsonpath='{.status.conditions[*].type}' | tr ' ' '\n'

# Expected progression:
# HubAcceptedManagedCluster
# ManagedClusterJoined
# ManagedClusterConditionAvailable

# Check full ManagedCluster status
kubectl get managedcluster example-hcp -o yaml | grep -A 30 "status:"

# Expected: conditions showing progression to Available
```

**Verification Commands:**

```bash
# Check ManagedCluster is joined
kubectl get managedcluster example-hcp \
  -o jsonpath='{.status.conditions[?(@.type=="ManagedClusterJoined")].status}'
# Expected: True

# Check ManagedCluster is available
kubectl get managedcluster example-hcp \
  -o jsonpath='{.status.conditions[?(@.type=="ManagedClusterConditionAvailable")].status}'
# Expected: True

# Check klusterlet logs for successful registration
kubectl logs -n klusterlet-example-hcp deployment/klusterlet-registration-agent --tail=50 | grep -i "cluster registered\|registration successful"

# Expected: Log entries showing successful registration
```

**When to Move to Next Phase:** ManagedCluster status Available=True AND Joined=True

---

### Phase 5: Management Enabled (T+15+)

**What Happens:**

1. ManagedCluster status fully reconciled (Available=True, Joined=True)
2. ManagedClusterInfo resource gets populated with cluster details
3. ACM add-ons deployed based on KlusterletAddonConfig
4. Cluster appears in ACM console
5. Policies and governance can now be applied

**Observable State:**

```bash
# Check ManagedCluster final status
kubectl get managedcluster example-hcp -o wide

# Expected:
# NAME          HUB ACCEPTED   MANAGED CLUSTER URLS                    JOINED   AVAILABLE   AGE
# example-hcp   true           https://api.example-hcp.example.com     True     True        5m

# Check ManagedClusterInfo populated
kubectl get managedclusterinfo -n example-hcp example-hcp -o yaml | head -50

# Expected: Detailed cluster information including version, nodes, etc.
```

**Verification Commands:**

```bash
# Complete verification - all resources healthy
kubectl get managedcluster example-hcp && \
kubectl get managedclusterinfo -n example-hcp example-hcp && \
kubectl get klusterletaddonconfig -n example-hcp example-hcp

# All should exist and show healthy status

# Check node inventory in ManagedClusterInfo
kubectl get managedclusterinfo -n example-hcp example-hcp \
  -o jsonpath='{.status.nodeList[*].name}' | tr ' ' '\n'

# Expected: List of worker node names

# Verify cluster appears in ACM (if console access available)
# Navigate to: ACM Console → Clusters → All Clusters
# Expected: example-hcp listed with status "Ready"
```

**Import Complete!** The hosted cluster is now fully managed by ACM.

---

## Pattern 1: Full-Auto Import

### What Happens

```
Create HostedCluster
    ↓ (HyperShift)
Create ManagedCluster
    ↓ (Kubernetes)
Deploy klusterlet
    ↓ (klusterlet agent)
Register with ACM Hub
    ↓ (ACM controller)
Available in ACM console
```

**Timeline:** Cluster appears in ACM in 10-15 minutes automatically.

### Configuration

Nothing special needed! Just create a HostedCluster normally:

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: example-hcp
  namespace: clusters
spec:
  baseDomain: example.com
  release:
    image: quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64
  # ... rest of spec
  # With auto-import enabled on the addon, hypershift-addon creates ManagedCluster after the control plane is Available
```

### What ACM Creates Automatically

When the cluster is detected, ACM creates:

```yaml
# 1. ManagedCluster (cluster representation)
apiVersion: cluster.open-cluster-management.io/v1
kind: ManagedCluster
metadata:
  name: example-hcp
spec:
  hubAcceptsClient: true
  leaseDurationSeconds: 60
status:
  conditions:
    - type: HubAccepted
      status: "True"
    - type: ManagedClusterJoined
      status: "True"
    - type: Available
      status: "True"

# 2. KlusterletAddonConfig (add-on configuration)
apiVersion: agent.open-cluster-management.io/v1
kind: KlusterletAddonConfig
metadata:
  name: example-hcp
  namespace: example-hcp
spec:
  clusterName: example-hcp
  clusterNamespace: example-hcp
  applicationManager:
    enabled: true
  policyController:
    enabled: true
  searchCollector:
    enabled: true

# 3. Namespace (cluster's namespace on Hub)
apiVersion: v1
kind: Namespace
metadata:
  name: example-hcp
```

### Verify Full-Auto

```bash
# 1. Check klusterlet deployment on Hub
kubectl get deployment -A | grep klusterlet-addon

# 2. Check ManagedCluster exists
kubectl get managedcluster example-hcp
# Should show: Available=True

# 3. Check in ACM console
# Login to Hub cluster ACM console
# Navigate to: Clusters > All Clusters
# Should see "example-hcp" with status "Ready"

# 4. Check cluster metadata
kubectl get managedcluster example-hcp -o json | \
  jq '.metadata.labels'
# Should show cloud, vendor, and other auto-added labels
```

### Advantages

- **Automatic:** No manual import steps
- **Fast:** Cluster ready for management in 10-15 minutes
- **Complete:** All ACM features available immediately
- **Safe:** Klusterlet is standard HyperShift component

### When to Use

- Production clusters
- Standard deployments
- When you want ACM governance
- Default choice for most users

---

<a id="pattern-2-disabled-import"></a>

## Pattern 2: Disabled Import

### What Happens

```
HostedCluster control plane becomes Available
    ↓ (HyperShift)
hypershift-addon auto-import controller does NOT create ManagedCluster
    (autoImportDisabled on AddOnDeploymentConfig → DISABLE_AUTO_IMPORT on agent)
    ↓
Hosted cluster runs; not registered with ACM until you import manually
```

**Result:** The hosted cluster is still provisioned by HyperShift; it is simply **not** automatically represented as a **`ManagedCluster`** on the hub. You can import later (UI, CLI, or your own Git-managed `ManagedCluster`).

### Configuration (addon-level)

**Auto-import is disabled on the hypershift-addon agent**, not by patching random `HostedCluster` fields. Set the **`autoImportDisabled`** customized variable on the addon’s **`AddOnDeploymentConfig`** (default name **`hypershift-addon-deploy-config`**, namespace **`multicluster-engine`**). The addon manifest maps that to the agent environment variable **`DISABLE_AUTO_IMPORT`**, which the auto-import controller checks (`pkg/agent/auto_import_controller.go`).

Add or merge the variable (keep other `customizedVariables` entries intact):

```bash
oc patch addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine --type=json -p='[{"op":"add","path":"/spec/customizedVariables/-","value":{"name":"autoImportDisabled","value":"true"}}]'
```

Example fragment:

```yaml
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: AddOnDeploymentConfig
metadata:
  name: hypershift-addon-deploy-config
  namespace: multicluster-engine
spec:
  customizedVariables:
    # ... existing variables ...
    - name: autoImportDisabled
      value: "true"
```

**Behavior notes:**

- Only **new** hosted clusters created after this change are skipped for auto-import; clusters **already** imported are unchanged.
- To **re-enable** auto-import, remove the `autoImportDisabled` entry from the same `AddOnDeploymentConfig` (then allow the hypershift-addon deployment to roll out).

More detail: [Disabling automatic import](../planning/provision_hosted_cluster_on_mce_local_cluster.md#disabling-automatic-import).

### Per-cluster: remove ACM management after import (not addon-wide)

If **addon auto-import stays on** but you want **one** hosted cluster out of ACM after it was imported, delete that cluster’s **`ManagedCluster`**. The hosted cluster keeps running; you can import again later (UI, CLI, or Git) if needed.

```bash
kubectl delete managedcluster example-hcp
```

### When to Use

- Development/test clusters
- Air-gapped environments (no Hub connectivity)
- Temporary clusters (planning to delete soon)
- Complex custom configurations
- Early lifecycle (before production readiness)

### Disadvantages

- No ACM governance or policies
- No cluster console access via ACM
- Manual operations needed for cluster management
- No integration with ACM multi-cluster features

---

## Pattern 3: Hybrid Import

### What Happens

```
Cluster 1 (Full-Auto)    → Imported, Managed by ACM
Cluster 2 (Full-Auto)    → Imported, Managed by ACM
Cluster 3 (Disabled)     → Not imported, Independent

Same Hub managing mix
```

**Concept:** Some clusters auto-import, some don't, all on same Hub.

### Configuration

Simple: Just create some clusters with auto-import, others without:

```yaml
# In Git repository:

clusters/
├── production/
│   ├── hostedcluster.yaml      (will be auto-imported)
│   └── nodepool.yaml
│
├── development/
│   ├── hostedcluster.yaml      (will be auto-imported)
│   └── nodepool.yaml
│
└── testing/
    ├── hostedcluster.yaml      (disable import via delete)
    └── nodepool.yaml
```

Argo Application:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: gitops-hcp-all
spec:
  source:
    path: clusters/
  destination:
    namespace: clusters
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
```

Then manually manage which are imported:

```bash
# Production: keep auto-import
kubectl get managedcluster prod-hcp

# Development: keep auto-import
kubectl get managedcluster dev-hcp

# Testing: delete ManagedCluster to disable import
kubectl delete managedcluster test-hcp
# Cluster still runs, just not managed
```

### Cluster-by-Cluster Control

```bash
# Get list of all clusters
kubectl get hostedcluster -A

# For each cluster, choose:
# - Auto-import: do nothing (default)
# - Disabled: kubectl delete managedcluster <name>
# - Re-enable: kubectl apply -f managedcluster-<name>.yaml
```

### When to Use

- Mixed environments (prod + test on same Hub)
- Gradual rollout (import prod, test others first)
- Different management levels needed
- Complex organizational requirements

### Advantages

- Flexibility per cluster
- Selective governance
- Easy on/off per cluster
- Matches real-world complexity

---

<a id="decision-tree"></a>

## Decision Tree

Choose your pattern:

```
                      Want cluster?
                         |
         ┌───────────────┼───────────────┐
         │               │               │
      Dev/Test        Production      Mixed
         │               │               │
    Disabled         Full-Auto        Hybrid
  (Simple,fast)  (Automatic,       (Flexible)
                 complete)
```

**Detailed decision tree:**

```
1. Is this a production cluster?
   ├─ YES → Full-Auto (recommended)
   └─ NO → Continue to 2

2. Do you need ACM features?
   ├─ YES → Full-Auto (recommended)
   └─ NO → Continue to 3

3. Is cluster temporary?
   ├─ YES → Disabled
   └─ NO → Continue to 4

4. Will this environment evolve?
   ├─ YES → Hybrid (start Disabled, move to Full-Auto later)
   └─ NO → Disabled
```

---

## Comparing the Three Patterns

| Aspect | Full-Auto | Disabled | Hybrid |
|--------|-----------|----------|--------|
| **Setup** | Default, nothing needed | Modify HCP config | Per-cluster choice |
| **Time to ready** | 10-15 min | N/A (no ACM) | 10-15 min per cluster |
| **ACM features** | All available | None | Per-cluster choice |
| **Governance** | ACM policies apply | Manual control | Mixed |
| **Console access** | Via ACM | Direct only | Mixed |
| **Cluster dashboard** | Full | Basic | Mixed |
| **Policy enforcement** | Automatic | Manual | Mixed |
| **Effort** | Minimal | Minimal | Medium |
| **Flexibility** | Good | Excellent | Best |

---

## ACM Features by Pattern

### Available with Full-Auto (or Hybrid when enabled)

```
✓ Cluster dashboard in ACM console
✓ Multi-cluster application deployment (Application CRD)
✓ Governance policies (ConfigurationPolicy)
✓ Compliance scanning
✓ Multi-cluster observability
✓ Automatic cluster health monitoring
✓ Cluster resource quotas
✓ Network policy enforcement
✓ Image vulnerability scanning
```

### Not Available with Disabled Import

```
✗ ACM console management
✗ Multi-cluster app deployment via ACM
✗ ACM policies
✗ Multi-cluster observability
```

But cluster still works fine for:
```
✓ Deploying applications directly
✓ Using cluster's native Argo CD
✓ Local monitoring and management
✓ Direct kubectl access
```

---

## Next Steps

- [examples/gitops-kubevirt/03-scaling/README.md](../../examples/gitops-kubevirt/03-scaling/README.md) - GitOps scaling walkthrough (HyperShift `NodePool`, not ACM-specific)
- [examples/gitops-kubevirt/04-upgrades/README.md](../../examples/gitops-kubevirt/04-upgrades/README.md) - GitOps upgrade walkthrough (HyperShift releases, not ACM-specific)
- [02-troubleshooting.md](./02-troubleshooting.md) - ACM/MCE import and hub-side troubleshooting (GitOps only where it blocks ACM)
