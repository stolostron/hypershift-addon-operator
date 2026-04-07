# Scenario 3: Scaling and NodePool Management - Validation Checklist

This checklist validates each scaling operation in Scenario 3.

## Prerequisites

- [ ] Scenario 1 (Provisioning) completed successfully
- [ ] Base HostedCluster and NodePool are healthy (AVAILABLE=True, READY=2/2)
- [ ] Argo CD Application synced and healthy
- [ ] You can access the hosted cluster kubeconfig

## Base State Validation

Before starting operations, verify the base state:

### 1. HostedCluster Status

- [ ] **Verify HostedCluster exists and is available:**
  ```bash
  oc get hostedcluster -n clusters example-hcp
  ```
  Expected: AVAILABLE=True, VERSION shows current version

- [ ] **Check availability condition:**
  ```bash
  oc get hostedcluster -n clusters example-hcp -o jsonpath='{.status.conditions[?(@.type=="Available")]}'
  ```
  Expected: status=True

### 2. NodePool Status

- [ ] **Verify NodePool exists with 2 replicas:**
  ```bash
  oc get nodepool -n clusters example-hcp-workers
  ```
  Expected: READY=2/2, UPDATED=2, AVAILABLE=2

- [ ] **Check replicas field:**
  ```bash
  oc get nodepool -n clusters example-hcp-workers -o jsonpath='{.spec.replicas}'
  ```
  Expected: 2

### 3. Nodes Status

- [ ] **Verify 2 worker nodes are ready:**
  ```bash
  export KUBECONFIG=/tmp/example-hcp-kubeconfig
  oc get nodes
  ```
  Expected: 2 nodes in Ready state

- [ ] **Check node resources:**
  ```bash
  oc describe nodes | grep -A5 "Capacity:"
  ```
  Expected: Each node has 4 CPUs, 16Gi memory

### 4. Cluster Health

- [ ] **Verify all cluster operators available:**
  ```bash
  oc get clusteroperators | grep -v "True.*False.*False"
  ```
  Expected: No rows (all operators are AVAILABLE=True)

---

## Operation 1: Scale Up (2 → 5 nodes)

**Duration:** ~15-20 minutes

### 1. Apply the Operation

- [ ] **Update replicas to 5:**
  ```bash
  oc patch nodepool -n clusters example-hcp-workers \
    --type merge -p '{"spec":{"replicas":5}}'
  ```
  Or via Git: edit base/nodepool.yaml, commit, and push

- [ ] **Verify patch applied:**
  ```bash
  oc get nodepool -n clusters example-hcp-workers -o jsonpath='{.spec.replicas}'
  ```
  Expected: 5

### 2. Monitor Scale-Up Progress

- [ ] **Watch NodePool status in real-time:**
  ```bash
  oc get nodepool -n clusters example-hcp-workers -w
  ```
  Expected progression:
  - READY: 2/2 → 3/3 → 4/4 → 5/5
  - UPDATED: 2 → 3 → 4 → 5
  - Timeline: 3-5 minutes per new node

- [ ] **Check node creation in management cluster:**
  ```bash
  oc get vm -n clusters-example-hcp -w
  ```
  Expected: 3 new VMs being created

### 3. Verify New Nodes in Hosted Cluster

- [ ] **After reaching READY=5/5, check hosted cluster nodes:**
  ```bash
  export KUBECONFIG=/tmp/example-hcp-kubeconfig
  oc get nodes
  ```
  Expected: 5 nodes in Ready state

- [ ] **Check node status individually:**
  ```bash
  oc get nodes -o wide
  ```
  Expected: All nodes have STATUS=Ready, all with same KERNEL-VERSION

### 4. Verify Workload Rescheduling

- [ ] **Check if any pods are pending:**
  ```bash
  oc get pods -A --field-selector=status.phase=Pending
  ```
  Expected: No pending pods (or only known pending pods)

- [ ] **Verify pod distribution across nodes:**
  ```bash
  oc get pods -A -o wide | awk '{print $NF}' | tail -n +2 | sort | uniq -c
  ```
  Expected: Pods distributed roughly evenly across 5 nodes

### 5. Cluster Health Check

- [ ] **Verify cluster operators still available:**
  ```bash
  oc get clusteroperators | grep -v "True.*False.*False"
  ```
  Expected: No rows (all healthy)

---

## Operation 2: Enable Autoscaling

**Duration:** ~5-10 minutes

### 1. Apply Autoscaling Configuration

- [ ] **Apply the autoscaling manifest:**
  ```bash
  oc apply -f operations/02-add-autoscaling.yaml
  ```
  Or via Git: edit base/nodepool.yaml and change replicas to autoScaling config

- [ ] **Verify autoscaling is configured:**
  ```bash
  oc get nodepool -n clusters example-hcp-workers -o yaml | grep -A2 autoScaling
  ```
  Expected:
  ```yaml
  autoScaling:
    maxReplicas: 8
    minReplicas: 2
  ```

### 2. Verify Replicas Field is Removed

- [ ] **Check that static replicas is no longer set:**
  ```bash
  oc get nodepool -n clusters example-hcp-workers -o jsonpath='{.spec.replicas}'
  ```
  Expected: Empty output (field doesn't exist)

### 3. Monitor Autoscaling Adjustment

- [ ] **Watch NodePool to see if it adjusts:**
  ```bash
  oc get nodepool -n clusters example-hcp-workers -w
  ```
  Expected behavior:
  - If current replicas > maxReplicas: scale down to maxReplicas
  - If current replicas < minReplicas: scale up to minReplicas
  - If current replicas within range: no change

  Note: Current state is 5 nodes, minReplicas=2, maxReplicas=8, so no adjustment expected

### 4. Test Autoscaling (Optional but Recommended)

Deploy a test workload that requires more resources:

- [ ] **Create a test namespace:**
  ```bash
  export KUBECONFIG=/tmp/example-hcp-kubeconfig
  oc create namespace autoscale-test
  ```

- [ ] **Deploy resource-hungry pods:**
  ```bash
  cat <<'EOF' | oc apply -f -
  apiVersion: apps/v1
  kind: Deployment
  metadata:
    name: stress-test
    namespace: autoscale-test
  spec:
    replicas: 10
    selector:
      matchLabels:
        app: stress
    template:
      metadata:
        labels:
          app: stress
      spec:
        containers:
        - name: stress
          image: busybox
          resources:
            requests:
              cpu: "2"
              memory: "4Gi"
            limits:
              cpu: "2"
              memory: "4Gi"
          command: ["sleep", "3600"]
  EOF
  ```

- [ ] **Monitor autoscaler behavior:**
  ```bash
  # Watch NodePool
  oc get nodepool -n clusters example-hcp-workers -w
  
  # Check if it scales up toward 8 nodes due to pod pressure
  # (may take 1-2 minutes for autoscaler to react)
  ```

- [ ] **Clean up test workload:**
  ```bash
  oc delete namespace autoscale-test
  oc get nodepool -n clusters example-hcp-workers -w
  # Should scale back down toward minReplicas after 10 minutes
  ```

### 5. Cluster Health Check

- [ ] **Verify cluster operators still available:**
  ```bash
  export KUBECONFIG=/tmp/example-hcp-kubeconfig
  oc get clusteroperators | grep -v "True.*False.*False"
  ```
  Expected: No rows

---

## Operation 3: Vertical Scaling (Change Instance Size)

**Duration:** ~25-40 minutes (5-10 min per node + overhead)

### 1. Apply Instance Size Change

- [ ] **Apply the vertical scaling manifest:**
  ```bash
  oc apply -f operations/03-modify-instance-size.yaml
  ```
  Or via Git: edit base/nodepool.yaml, change cores and memory

- [ ] **Verify compute resources updated:**
  ```bash
  oc get nodepool -n clusters example-hcp-workers -o jsonpath='{.spec.platform.kubevirt.compute}'
  ```
  Expected:
  ```json
  {"cores":8,"memory":"32Gi"}
  ```

### 2. Monitor Node Replacement

- [ ] **Watch NodePool status:**
  ```bash
  oc get nodepool -n clusters example-hcp-workers -w
  ```
  Expected progression:
  - UPDATED decreases (old nodes being replaced)
  - New nodes with new spec being created
  - READY may decrease temporarily
  - Timeline: 5-10 minutes per node, ~30-50 minutes for 5 nodes

### 3. Monitor VM Replacement

- [ ] **Check VM status in management cluster:**
  ```bash
  oc get vm -n clusters-example-hcp -w
  ```
  Expected: Old VMs terminated, new VMs created with larger resources

### 4. Verify Workload Migration

- [ ] **During upgrade, check node status:**
  ```bash
  export KUBECONFIG=/tmp/example-hcp-kubeconfig
  oc get nodes -w
  ```
  Expected: Some nodes NotReady briefly, evicted pods migrating to other nodes

- [ ] **Check for pending pods:**
  ```bash
  oc get pods -A --field-selector=status.phase=Pending
  ```
  Expected: Minimal pending pods (system pods rescheduling)

### 5. Post-Upgrade Verification

- [ ] **Verify all nodes have new resources:**
  ```bash
  oc describe nodes | grep -A5 "Capacity:"
  ```
  Expected: All nodes show 8 CPUs, 32Gi memory

- [ ] **Check node readiness:**
  ```bash
  oc get nodes
  ```
  Expected: All 5 nodes in Ready state (took 30-50 minutes)

- [ ] **Verify cluster operators available:**
  ```bash
  oc get clusteroperators | grep -v "True.*False.*False"
  ```
  Expected: No rows (all healthy)

### 6. Cluster Health Verification

- [ ] **Test API availability:**
  ```bash
  oc whoami
  ```
  Expected: Shows current user

- [ ] **Check storage:**
  ```bash
  oc get pvc -A
  ```
  Expected: All PVCs bound

---

## Operation 4: Add Second NodePool

**Duration:** ~15-20 minutes

### 1. Add the New NodePool

- [ ] **Apply the infra NodePool:**
  ```bash
  oc apply -f operations/04-add-nodepool.yaml
  ```
  Or via Git: add operations/04-add-nodepool.yaml to your kustomization

- [ ] **Verify new NodePool created:**
  ```bash
  oc get nodepool -n clusters
  ```
  Expected: Both `example-hcp-workers` and `example-hcp-infra` shown

### 2. Monitor Infra NodePool Creation

- [ ] **Watch infra NodePool status:**
  ```bash
  oc get nodepool -n clusters example-hcp-infra -w
  ```
  Expected progression:
  - READY: 0/2 → 1/2 → 2/2
  - Timeline: 5-10 minutes total

### 3. Verify Infra Nodes in Hosted Cluster

- [ ] **Check nodes with labels:**
  ```bash
  export KUBECONFIG=/tmp/example-hcp-kubeconfig
  oc get nodes -L node-type
  ```
  Expected: 2 new nodes with node-type=infra label, 5 existing nodes without it

- [ ] **Verify node resources:**
  ```bash
  oc get nodes -L node-type -o wide
  ```
  Expected: Infra nodes show different sizes (smaller than workers)

### 4. Verify Taints

- [ ] **Check infra node taints:**
  ```bash
  oc describe nodes -l node-type=infra | grep Taints
  ```
  Expected: Shows `node-type=infra:NoSchedule`

### 5. Test Workload Scheduling

- [ ] **Deploy pod without toleration (should not schedule on infra):**
  ```bash
  cat <<'EOF' | oc apply -f -
  apiVersion: v1
  kind: Pod
  metadata:
    name: test-no-tolerate
    namespace: default
  spec:
    containers:
    - name: test
      image: busybox
      command: ["sleep", "3600"]
  EOF
  ```

- [ ] **Check pod is NOT on infra nodes:**
  ```bash
  oc get pod -n default test-no-tolerate -o wide
  ```
  Expected: NODE shows a non-infra node (node-type label not present)

- [ ] **Deploy pod WITH toleration (should schedule on infra):**
  ```bash
  cat <<'EOF' | oc apply -f -
  apiVersion: v1
  kind: Pod
  metadata:
    name: test-tolerate
    namespace: default
  spec:
    tolerations:
    - key: node-type
      operator: Equal
      value: infra
      effect: NoSchedule
    nodeSelector:
      node-type: infra
    containers:
    - name: test
      image: busybox
      command: ["sleep", "3600"]
  EOF
  ```

- [ ] **Verify pod is on infra node:**
  ```bash
  oc get pod -n default test-tolerate -o wide
  ```
  Expected: NODE shows an infra node (has node-type=infra label)

- [ ] **Clean up test pods:**
  ```bash
  oc delete pod -n default test-no-tolerate test-tolerate
  ```

### 6. Cluster Health Check

- [ ] **Verify cluster operators still available:**
  ```bash
  oc get clusteroperators | grep -v "True.*False.*False"
  ```
  Expected: No rows

---

## Operation 5: Remove NodePool

**Duration:** ~10-15 minutes

### 1. Prepare for Removal

- [ ] **Verify current state has 2 NodePools:**
  ```bash
  oc get nodepool -n clusters
  ```
  Expected: example-hcp-workers and example-hcp-infra

- [ ] **Check if any pods are on infra nodes:**
  ```bash
  export KUBECONFIG=/tmp/example-hcp-kubeconfig
  oc get pods -A -L node-type -o wide | grep infra
  ```
  Expected: Ideally, minimal or no pods on infra nodes

### 2. Evict Workloads (if any)

If there are pods on infra nodes:

- [ ] **Cordon the nodes:**
  ```bash
  oc cordon -l node-type=infra
  ```

- [ ] **Drain the nodes:**
  ```bash
  for node in $(oc get nodes -l node-type=infra -o name); do
    oc drain $node --ignore-daemonsets --delete-emptydir-data
  done
  ```

- [ ] **Verify pods moved:**
  ```bash
  oc get pods -A -L node-type
  ```
  Expected: No pods with node-type=infra

### 3. Delete the NodePool

- [ ] **Delete the infra NodePool:**
  ```bash
  oc delete nodepool -n clusters example-hcp-infra
  ```
  Or via Git: remove operations/04-add-nodepool.yaml from your configuration

### 4. Monitor Deletion

- [ ] **Watch NodePool deletion:**
  ```bash
  oc get nodepool -n clusters -w
  ```
  Expected: example-hcp-infra transitions to Terminating and disappears

- [ ] **Check VMs are terminated:**
  ```bash
  oc get vm -n clusters-example-hcp | grep infra
  ```
  Expected: Infra VMs disappear (2-5 minutes)

### 5. Verify Node Removal

- [ ] **Check nodes are removed from hosted cluster:**
  ```bash
  export KUBECONFIG=/tmp/example-hcp-kubeconfig
  oc get nodes -L node-type
  ```
  Expected: Only 5 worker nodes remain, no nodes with node-type=infra

- [ ] **Verify no infra labels:**
  ```bash
  oc get nodes -L node-type | grep infra
  ```
  Expected: No output (no nodes with infra label)

### 6. Final Cluster Verification

- [ ] **Verify remaining NodePool status:**
  ```bash
  oc get nodepool -n clusters
  ```
  Expected: Only example-hcp-workers shown

- [ ] **Verify cluster health:**
  ```bash
  oc get hostedcluster -n clusters example-hcp
  ```
  Expected: AVAILABLE=True

- [ ] **Verify all cluster operators:**
  ```bash
  oc get clusteroperators | grep -v "True.*False.*False"
  ```
  Expected: No rows (all healthy)

---

## Summary

**Success Criteria:**
- [ ] All 5 operations completed
- [ ] Each operation validated
- [ ] Cluster remains healthy throughout
- [ ] No data loss or service interruption

**Total Time:** ~90-150 minutes
- Base state verification: 5 minutes
- Operation 1 (scale-up): 15-20 minutes
- Operation 2 (autoscaling): 5-10 minutes
- Operation 3 (vertical scale): 25-40 minutes
- Operation 4 (add pool): 15-20 minutes
- Operation 5 (remove pool): 10-15 minutes

**Troubleshooting:**
See [README.md](README.md) for detailed troubleshooting and GitOps workflow guidance.
