# Scenario 1: Provisioning - Validation Checklist

This checklist validates that the HostedCluster provisioning via GitOps is working correctly.

## Prerequisites

- [ ] Secrets created in `clusters` namespace (`pull-secret`, `ssh-key`)
- [ ] Argo CD installed and console or CLI accessible

## Validation Steps

### 1. Argo Application Sync

- [ ] **Apply the Argo Application:**
  ```bash
  oc apply -f examples/gitops-kubevirt/01-provision/argo-application.yaml
  ```

- [ ] **Trigger initial sync:**
  ```bash
  # Via CLI
  argocd app sync example-hcp
  
  # Or via console: ArgoCD UI → Applications → example-hcp → SYNC
  ```

- [ ] **Verify sync status:**
  ```bash
  argocd app get example-hcp
  ```
  Expected: Health Status: Healthy, Sync Status: Synced

### 2. HostedCluster Creation

- [ ] **Verify HostedCluster resource created:**
  ```bash
  oc get hostedcluster -n clusters example-hcp
  ```
  Expected: Shows example-hcp

- [ ] **Watch HostedCluster status:**
  ```bash
  oc get hostedcluster -n clusters example-hcp -w
  ```
  Wait for: VERSION shows 4.21.0 (or your `release.image` tag), AVAILABLE becomes True (~10-15 minutes)

- [ ] **Check HostedCluster conditions:**
  ```bash
  oc get hostedcluster -n clusters example-hcp -o jsonpath='{.status.conditions[?(@.type=="Available")]}'
  ```
  Expected: status: "True"

### 3. Control Plane Verification

- [ ] **Verify control plane namespace created:**
  ```bash
  oc get namespace clusters-example-hcp
  ```
  Expected: Namespace exists

- [ ] **Check control plane pods running:**
  ```bash
  oc get pods -n clusters-example-hcp
  ```
  Expected: All pods in Running state, including:
  - etcd-*
  - kube-apiserver-*
  - kube-controller-manager-*
  - kube-scheduler-*

- [ ] **Verify API server is accessible:**
  ```bash
  oc get service -n clusters-example-hcp kube-apiserver
  ```
  Expected: Service has EXTERNAL-IP assigned

### 4. NodePool Verification

- [ ] **Verify NodePool resource created:**
  ```bash
  oc get nodepool -n clusters example-hcp-workers
  ```
  Expected: Shows example-hcp-workers

- [ ] **Check NodePool replicas:**
  ```bash
  oc get nodepool -n clusters example-hcp-workers -o jsonpath='{.spec.replicas}'
  ```
  Expected: 2

- [ ] **Wait for nodes to be ready:**
  ```bash
  oc get nodepool -n clusters example-hcp-workers -w
  ```
  Wait for: READY shows 2/2 (~15-20 minutes)

### 5. ACM Auto-Import Verification

- [ ] **Verify ManagedCluster auto-created:**
  ```bash
  oc get managedcluster example-hcp
  ```
  Expected: ManagedCluster exists

- [ ] **Check ManagedCluster status:**
  ```bash
  oc get managedcluster example-hcp -o jsonpath='{.status.conditions[?(@.type=="ManagedClusterConditionAvailable")]}'
  ```
  Expected: status: "True"

- [ ] **Verify klusterlet addon deployed:**
  ```bash
  oc get manifestwork -n example-hcp
  ```
  Expected: Shows klusterlet and addon manifestworks

### 6. Access Hosted Cluster

- [ ] **Get kubeconfig:**
  ```bash
  oc get secret -n clusters example-hcp-admin-kubeconfig -o jsonpath='{.data.kubeconfig}' | base64 -d > /tmp/example-hcp-kubeconfig
  ```

- [ ] **Verify access to hosted cluster:**
  ```bash
  export KUBECONFIG=/tmp/example-hcp-kubeconfig
  oc get nodes
  ```
  Expected: Shows 2 worker nodes in Ready state

- [ ] **Check hosted cluster version:**
  ```bash
  oc get clusterversion
  ```
  Expected: VERSION reflects the release in `hostedcluster.yaml` (e.g. 4.21.0)

- [ ] **Verify all cluster operators available:**
  ```bash
  oc get clusteroperators
  ```
  Expected: All operators show AVAILABLE=True

## Success Criteria

All checkboxes above should be checked (✓).

**Total time:** ~30-40 minutes from Argo sync to fully available hosted cluster.

## Troubleshooting

If any validation step fails, see:
- [../../../docs/gitops/02-troubleshooting.md](../../../docs/gitops/02-troubleshooting.md)
- [../../../docs/gitops/00-acm-hosted-clusters-when-using-gitops.md](../../../docs/gitops/00-acm-hosted-clusters-when-using-gitops.md) - ACM, secrets, and auto-import

Common issues:
- **HostedCluster stuck in Progressing:** Check control plane pod logs
- **Nodes not ready:** Check KubeVirt VMs are running
- **ManagedCluster not created:** Verify MCE auto-import is enabled
