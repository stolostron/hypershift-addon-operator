# Architecture

## System overview

The HyperShift addon operator runs in two modes: a **hub manager** on the ACM/MCE hub cluster and a **spoke agent** on each managed cluster. The manager uses the OCM addon-framework to deploy and configure the agent, which in turn installs and manages the HyperShift operator on the managed cluster.

```text
Hub Manager deploys Agent via ManifestWork ─→ Agent installs HyperShift Operator on spoke
```

## Component layers

### Hub Manager (`pkg/manager/`)

The hub-side addon manager built on `open-cluster-management.io/addon-framework`:

- **Template rendering:** Renders Helm-like templates (`pkg/manager/manifests/templates/`) into ManifestWork resources per managed cluster. Injects cluster-specific values (images, TLS config, pull secrets, feature flags).
- **Registration:** Handles addon CSR signing and approval for spoke agents.
- **Custom controllers:**
  - `DiscoveryConfigController` — manages MCE discovery configuration across clusters
  - `CLIDownloadInstall` — installs the `hcp` CLI download route

### Spoke Agent (`pkg/agent/`)

The spoke-side controllers that run on each managed cluster:

- **HostedCluster reconciler** — watches `HostedCluster` CRs, copies kubeconfig/kubeadmin secrets, generates external-managed-kubeconfig for klusterlet, computes placement scores.
- **Install/Upgrade controller** (`pkg/install/`) — installs, upgrades, and configures the HyperShift operator Deployment. Manages install flags, pull secrets, OIDC credentials, private link credentials, and node placement.
- **Addon status controller** — reports addon health back to the hub via `ManagedClusterAddOn` conditions.
- **Auto-import controller** — automatically imports hosted clusters as managed clusters.
- **Discovery agent** — discovers hosted clusters for the discovery service.
- **External secret controller** — manages external secret synchronization.
- **HCP kubeconfig watcher** — watches HostedControlPlane kubeconfig changes.
- **Capacity calculation** — computes HCP sizing metrics and cluster claims (full/threshold/zero).
- **ManagedCluster sync controller** — propagates labels between `HostedCluster`, `ManagedCluster` (MCE), and `ManagedCluster` (ACM hub).

### Install lifecycle (`pkg/install/`)

Manages the HyperShift operator on spoke clusters:

1. Renders install arguments from addon configuration (install flags ConfigMap).
2. Creates an install Job to deploy HyperShift via its CLI.
3. Monitors the operator Deployment health.
4. Handles upgrades by detecting image/flag changes and re-running the install.
5. Propagates node placement (nodeSelector/tolerations) from the agent pod to the operator Deployment.

## Data flow

```text
┌──────────────────────────────────────────────────────────────────┐
│                          Hub Cluster                              │
│                                                                  │
│  ManagedClusterAddOn ──→ Addon Manager ──→ ManifestWork          │
│  (hypershift-addon)       (template render)  (per cluster)       │
│                                                                  │
│  AddOnDeploymentConfig ──→ Agent config (images, flags, etc.)    │
│                                                                  │
│  ManagedCluster ←── Status/Claims ←── Agent (via hub client)    │
└──────────────────────────────────────────────────────────────────┘
                               │
                               ↓ (OCM ManifestWork)
┌──────────────────────────────────────────────────────────────────┐
│                        Managed Cluster                            │
│                                                                  │
│  Agent Pod (hypershift-addon-agent)                              │
│    ├── kube-rbac-proxy sidecar (metrics TLS termination)         │
│    └── hypershift-addon-agent container                          │
│          ├── Install/Upgrade HyperShift Operator                 │
│          ├── Watch HostedClusters → copy secrets, import         │
│          ├── Compute placement scores → ClusterClaims            │
│          └── Report addon status → ManagedClusterAddOn           │
│                                                                  │
│  HyperShift Operator Deployment                                  │
│    └── Manages HostedClusters → HostedControlPlanes              │
└──────────────────────────────────────────────────────────────────┘
```

## Deployment model

- **Production (ACM/MCE):** Deployed by the MCE operator (backplane-operator). The manager Deployment runs in `multicluster-engine` namespace. Agent pods are deployed per managed cluster in `open-cluster-management-agent-addon` namespace via ManifestWork.
- **Image override:** MCE supports image overrides via CSV env vars or ConfigMap for testing custom builds.
- **Development:** Build locally with `make build`, test with `make test`. Deploy custom images via `docker buildx build --platform linux/amd64 --push`.

## Key design decisions

- **Addon-framework based:** Uses OCM's addon-framework for lifecycle management, avoiding custom ManifestWork generation. The framework handles registration, template rendering, and health reporting.
- **Single binary, multiple modes:** `cmd/main.go` uses Cobra subcommands (`manager`, `agent`, `cleanup`) — same image for hub and spoke.
- **Template-based deployment:** Agent Deployment is rendered from Go templates with per-cluster values (TLS profile, images, feature flags), enabling configuration without code changes.
- **Dynamic TLS configuration:** Both the kube-rbac-proxy sidecar and client connections read the cluster's `APIServer` TLS profile dynamically, falling back to Intermediate (TLS 1.2) if unavailable.
- **Label propagation:** ManagedCluster sync controller implements bidirectional label sync between HostedCluster CRs and ManagedCluster resources across ACM hub and MCE hub, with conflict resolution and system label exclusions.
- **Node placement propagation:** Agent reads its own pod's nodeSelector/tolerations (inherited from AddOnDeploymentConfig) and applies them to the HyperShift operator Deployment.

## Ecosystem integration

| Project | Module | Role |
|---------|--------|------|
| OCM Addon Framework | `open-cluster-management.io/addon-framework` | Hub-side addon lifecycle |
| OCM API | `open-cluster-management.io/api` | ManagedCluster, ManagedClusterAddOn, AddOnDeploymentConfig, AddOnPlacementScore |
| HyperShift | `github.com/openshift/hypershift/api` | HostedCluster, HostedControlPlane, NodePool types |
| OpenShift API | `github.com/openshift/api` | Infrastructure, Route, ClusterVersion, APIServer (TLS) |
| MCE / Backplane | `github.com/stolostron/backplane-operator` | MultiClusterEngine type |
| Discovery | `github.com/stolostron/discovery` | DiscoveryConfig, DiscoveredCluster |
| OLM | `github.com/operator-framework/api` | ClusterServiceVersion for version detection |
| controller-runtime-common | `github.com/openshift/controller-runtime-common` | TLS profile fetching library |
