# GitOps hosted clusters: ACM troubleshooting (hub)

This guide focuses on **ACM / MCE behavior** when you lifecycle **HyperShift `HostedCluster`** resources with GitOps: auto-import, **`ManagedCluster`** status, **hosted-mode klusterlet**, registration secrets, and related hub controllers.

**Out of scope here:** deep diagnosis of the HyperShift control plane (etcd, kube-apiserver), worker **Node** readiness, **Machine** provisioning, image pulls inside the hosted cluster, and version upgrades. For those, use the [HyperShift documentation](https://hypershift-docs.netlify.app/), the example walkthroughs under [`examples/gitops-kubevirt/`](../../examples/gitops-kubevirt/), and the operator docs in this repository (for example [`docs/management/importing_hosted_cluster_cli.md`](../management/importing_hosted_cluster_cli.md)).

---

## Quick symptom index (ACM)

| Symptom | Start here |
|--------|----------------|
| No **`ManagedCluster`** after the control plane is up | [Auto-import never runs](#auto-import-never-creates-a-managedcluster) |
| **`ManagedCluster`** stuck **Joined=False** or **Available=False** | [Import / klusterlet stuck](#managedcluster-not-available-or-not-joined) |
| **`external-managed-kubeconfig`** missing or invalid | [Registration secret issues](#external-managed-kubeconfig-or-registration-failures) |
| Add-ons / **`KlusterletAddonConfig`** not applied | [Add-on configuration](#klusterletaddonconfig-and-add-ons) |
| Argo **OutOfSync** / app never syncs (no `HostedCluster` on hub) | [GitOps blocking the ACM path](#gitops--argo-when-it-blocks-acm) |

---

## Preconditions (auto-import)

The **hypershift-addon** auto-import controller only creates a **`ManagedCluster`** after the hosted **control plane reports `Available=True`**. If the control plane is still **Progressing**, fix **HyperShift** first (see links above)—ACM cannot import a cluster whose API is not ready.

Quick check:

```bash
kubectl get hostedcluster -n <hostedcluster-namespace> <name> -o jsonpath='{range .status.conditions[?(@.type=="Available")]}{.type}={.status}{"\n"}{end}'
# Need: Available=True
```

---

<a id="managedcluster-not-created"></a>

## Auto-import never creates a ManagedCluster

### 1. Auto-import disabled on the addon

**Check** the hypershift-addon **`AddOnDeploymentConfig`** (default **`hypershift-addon-deploy-config`**, namespace **`multicluster-engine`**) for **`autoImportDisabled: "true"`**, which sets **`DISABLE_AUTO_IMPORT`** on the addon deployment. See [Pattern 2: Disabled import](./01-acm-integration-overview.md#pattern-2-disabled-import) in the integration guide.

```bash
oc get addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine -o yaml | grep -A1 autoImportDisabled
```

### 2. Wrong hosting topology / addon instance

Auto-import in **`pkg/agent/auto_import_controller.go`** only runs for the **local / self-managed** hosting path where the addon is configured for that cluster. If you host HCPs on **remote** MCE clusters, you may need **manual** `ManagedCluster` YAML or hub discovery flows—see [Hosting cluster topologies](../planning/hosting_cluster_topologies.md) and [Discovering and managing hosted clusters with ACM](../management/discovering_hostedclusters.md).

### 3. hypershift-addon logs

On the cluster where the addon runs (typically the **hub** when hosting on `local-cluster`):

```bash
# Find the hypershift-addon pod (exact name varies by install)
kubectl get pods -A | grep hypershift-addon

kubectl logs -n <addon-namespace> deploy/hypershift-addon -f --tail=200
# Or the pod name returned above; filter for auto-import / HostedCluster / ManagedCluster
```

### 4. More detail

See **[Troubleshooting auto-import](./01-acm-integration-overview.md#troubleshooting-auto-import)** in **01** for symptom-specific commands (ManagedCluster missing, klusterlet, kubeconfig).

---

## ManagedCluster not Available or not Joined

Hosted clusters use **hosted-mode klusterlet** on the **hub**: look in the **`klusterlet-<hosted-cluster-name>`** namespace (not only `open-cluster-management-agent`).

### Inspect conditions

```bash
MC=<hosted-cluster-name>

kubectl get managedcluster "$MC" -o json | jq '.status.conditions'

kubectl get managedcluster "$MC" -o jsonpath='{range .status.conditions[*]}{.type}={.status} reason={.reason}{"\n"}{end}'
```

### Klusterlet pods and logs (hub)

```bash
kubectl get pods -n "klusterlet-$MC"
kubectl logs -n "klusterlet-$MC" -l app=klusterlet-registration-agent --tail=100
```

### cluster-import and hub controllers

```bash
kubectl get pods -n open-cluster-management-hub
# Pick the import / registration / cluster-manager related pod name for your version, then:
# kubectl logs -n open-cluster-management-hub <pod-name> --tail=100
```

### Hub → hosted API reachability

The klusterlet must reach the **hosted** Kubernetes API (DNS, TLS, routes/LB). From the hub:

```bash
# Use the API URL from your HostedCluster / kubeconfig secret
curl -kI "https://api.${MC}.<basedomain>:6443"
```

If the API is unreachable from the hub, **ManagedClusterJoined** / **Available** will not clear even when HyperShift reports the control plane healthy.

---

## external-managed-kubeconfig or registration failures

For hosted mode, **hypershift-addon** (with import helpers) materializes **`external-managed-kubeconfig`** in the **`klusterlet-<name>`** namespace so registration agents can talk to the hosted API.

```bash
MC=<hosted-cluster-name>
kubectl get secret -n "klusterlet-$MC" external-managed-kubeconfig -o jsonpath='{.data.kubeconfig}' | base64 -d | head -20
```

If the secret is missing after **`ManagedCluster`** exists, check **hypershift-addon** and **cluster-import-controller** logs (above). Wrong API URL or CA often shows up as TLS or connection errors in registration-agent logs.

---

## Wrong or missing ManagedCluster annotations

Hosted import expects annotations such as **`import.open-cluster-management.io/klusterlet-deploy-mode: Hosted`**, **`import.open-cluster-management.io/hosting-cluster-name`**, and **`open-cluster-management/created-via: hypershift`**. Compare your object to the examples in [01 — Key ACM resources](./01-acm-integration-overview.md#key-acm-resources) and [Importing a hosted cluster with the CLI](../management/importing_hosted_cluster_cli.md).

```bash
kubectl get managedcluster "$MC" -o jsonpath='{.metadata.annotations}' | jq
```

---

## KlusterletAddonConfig and add-ons

If **`ManagedCluster`** is healthy but add-ons never appear, confirm **`KlusterletAddonConfig`** exists in the cluster namespace and that the **ACM operator** is installed (hypershift-addon skips creating **KAC** when it does not detect ACM—see **`isACMInstalled`** in `auto_import_controller.go`).

```bash
MC=<hosted-cluster-name>
kubectl get klusterletaddonconfig -n "$MC" "$MC" -o yaml
kubectl get operators -A | grep -i advanced-cluster-management
```

---

<a id="application-outofsync"></a>

## GitOps / Argo (when it blocks ACM)

ACM only reacts to **`HostedCluster`** objects that exist on the API server. If Argo never syncs, **auto-import never starts**.

### Application OutOfSync

```bash
kubectl get application -n argocd <app-name>
argocd app diff <app-name>   # if argocd CLI is configured
kubectl describe application -n argocd <app-name>
kubectl logs -n argocd deployment/argocd-application-controller -f --tail=100
```

Resolve drift (revert manual changes or update Git), fix repo access, or fix invalid YAML so the **`HostedCluster`** is actually applied—then return to the [ACM sections](#auto-import-never-creates-a-managedcluster) above.

---

<a id="log-locations"></a>

## Log locations (ACM / import path)

| Area | Where to look |
|------|----------------|
| **hypershift-addon** (auto-import, kubeconfig) | Pod logs in the addon namespace (often `multicluster-engine` or the spoke addon namespace for your topology) |
| **ManagedCluster** / join conditions | `kubectl describe managedcluster <name>` |
| **Hosted-mode klusterlet** | `kubectl logs -n klusterlet-<hosted-cluster-name> -l app=klusterlet-registration-agent` |
| **cluster-import** / hub | Pods in `open-cluster-management-hub` |
| **Argo CD** (Git not landing) | `kubectl logs -n argocd deployment/argocd-application-controller -f` |

HyperShift operator logs (`kubectl logs -n hypershift deployment/hypershift-operator`) are useful **only** while proving the control plane is healthy enough for ACM to start import—they are not the primary lens for **ManagedCluster** issues.

<a id="observability-commands"></a>

## Hub observability (ACM quick checks)

```bash
kubectl get managedcluster -A
kubectl get managedcluster <name> -o wide
kubectl get managedclusterinfo -A | head
kubectl get klusterletaddonconfig -A
```

For **`ManagedCluster`** / **`ManagedClusterInfo`** field meanings and the import timeline, see [04](./01-acm-integration-overview.md).

---

## Collecting information for escalation

1. **Hub / hosting cluster context:** OpenShift and MCE/ACM versions; whether HCP is on `local-cluster` or a remote managed cluster.
2. **`HostedCluster`:** `kubectl get hostedcluster -n <ns> <name> -o yaml` (redact secrets).
3. **`ManagedCluster`:** full YAML if it exists.
4. **Logs:** hypershift-addon (auto-import), `klusterlet-<name>` registration agent, cluster-import-controller (as available).
5. **AddOnDeploymentConfig:** `hypershift-addon-deploy-config` snippet showing `autoImportDisabled` if relevant.

---

## References

- [00 — ACM mindset with GitOps](./00-acm-hosted-clusters-when-using-gitops.md)
- [01 — Integration & auto-import](./01-acm-integration-overview.md) (includes [troubleshooting auto-import](./01-acm-integration-overview.md#troubleshooting-auto-import))
- [Importing a hosted cluster with the CLI](../management/importing_hosted_cluster_cli.md)
- [Discovering and managing hosted clusters with ACM](../management/discovering_hostedclusters.md)
- [HyperShift docs](https://hypershift-docs.netlify.app/)
- [ACM product documentation](https://access.redhat.com/documentation/en-us/red_hat_advanced_cluster_management_for_kubernetes/)

