# Importing a Hosted Cluster Using CLI

This guide explains how to manually import a hosted cluster into MCE/ACM as a managed cluster using command-line tools.

## Overview

When you create a hosted cluster using HyperShift, it is not automatically managed by MCE/ACM unless auto-import is enabled. To manage the hosted cluster's lifecycle and enable ACM features like policies and applications, you need to import it as a managed cluster.

## Prerequisites

- Access to the MCE/ACM hub cluster with cluster-admin privileges
- `oc` CLI tool installed and configured
- A hosted cluster that is in `Available` state
- Network connectivity between the hub cluster and the hosted cluster API server

## Step 1: Identify Required Information

Before importing, gather the following information:

| Variable | Description | Example |
|----------|-------------|---------|
| `CLUSTER_NAME` | Name of the hosted cluster to import | `my-hosted-cluster` |
| `HOSTING_CLUSTER_NAME` | Name of the managed cluster where the hosted cluster runs | `local-cluster` |

To find your hosted cluster name:

```bash
oc get hostedcluster -A
```

Example output:

```
NAMESPACE   NAME                 VERSION   KUBECONFIG                          PROGRESS    AVAILABLE
clusters    my-hosted-cluster    4.15.0    my-hosted-cluster-admin-kubeconfig  Completed   True
```

## Step 2: Create the ManagedCluster Resource

Create a `ManagedCluster` resource on the hub cluster to initiate the import process.

```bash
export CLUSTER_NAME=<your-hosted-cluster-name>
export HOSTING_CLUSTER_NAME=local-cluster

cat <<EOF | oc apply -f -
apiVersion: cluster.open-cluster-management.io/v1
kind: ManagedCluster
metadata:
  annotations:
    import.open-cluster-management.io/hosting-cluster-name: ${HOSTING_CLUSTER_NAME}
    import.open-cluster-management.io/klusterlet-deploy-mode: Hosted
    open-cluster-management/created-via: hypershift
  labels:
    cloud: auto-detect
    cluster.open-cluster-management.io/clusterset: default
    name: ${CLUSTER_NAME}
    vendor: auto-detect
  name: ${CLUSTER_NAME}
spec:
  hubAcceptsClient: true
  leaseDurationSeconds: 60
EOF
```

### Required Labels

| Label | Value | Description |
|-------|-------|-------------|
| `cloud` | `auto-detect` | Automatically detects the cloud provider (AWS, Azure, GCP, etc.) |
| `vendor` | `auto-detect` | Automatically detects the Kubernetes vendor (OpenShift, etc.) |

These labels are required for proper cluster identification and enable MCE/ACM to correctly categorize and manage the imported cluster.

### Required Annotations

| Annotation | Required | Description |
|------------|----------|-------------|
| `import.open-cluster-management.io/hosting-cluster-name` | Yes | The managed cluster name where the hosted cluster's control plane runs |
| `import.open-cluster-management.io/klusterlet-deploy-mode: Hosted` | Yes | Deploys the klusterlet in hosted mode alongside the hosted control plane |
| `open-cluster-management/created-via` | No | Indicates how the cluster was created |


## Step 3: Complete the Import

After creating the `ManagedCluster` resource, extract the import manifest and apply it to the hosted cluster to complete the import process.

1. Extract the import manifest from the hub cluster:

   ```bash
   oc get secret ${CLUSTER_NAME}-import -n ${CLUSTER_NAME} -o jsonpath={.data.import\\.yaml} | base64 --decode > import.yaml
   ```

2. Apply the import manifest to the hosted cluster:

   ```bash
   oc apply -f import.yaml
   ```

This deploys the klusterlet agent on the hosted cluster, which establishes the connection back to the hub cluster.

## Step 4: Enable ACM Addons (Optional)

If you are running ACM (not just MCE) and want to enable additional addons like policy controllers, application management, and governance, create a `KlusterletAddonConfig` resource.

**Note:** Skip this step if you have MCE only installed.

```bash
cat <<EOF | oc apply -f -
apiVersion: agent.open-cluster-management.io/v1
kind: KlusterletAddonConfig
metadata:
  name: ${CLUSTER_NAME}
  namespace: ${CLUSTER_NAME}
spec:
  clusterName: ${CLUSTER_NAME}
  clusterNamespace: ${CLUSTER_NAME}
  clusterLabels:
    cloud: auto-detect
    vendor: auto-detect
  applicationManager:
    enabled: true
  certPolicyController:
    enabled: true
  policyController:
    enabled: true
  searchCollector:
    enabled: false
EOF
```

### Available Addons

| Addon | Description |
|-------|-------------|
| `applicationManager` | Enables application lifecycle management |
| `certPolicyController` | Manages certificate policies |
| `policyController` | Enforces governance policies |
| `searchCollector` | Enables search indexing for the cluster |

## Step 5: Verify the Import

### Check ManagedCluster Status

Wait for the hosted cluster to be imported and become available:

```bash
oc get managedcluster ${CLUSTER_NAME}
```

Expected output when successful:

```
NAME                HUB ACCEPTED   MANAGED CLUSTER URLS                              JOINED   AVAILABLE   AGE
my-hosted-cluster   true           https://api.my-hosted-cluster.example.com:6443    True     True        5m
```

### Check ManagedCluster Conditions

For detailed status information:

```bash
oc get managedcluster ${CLUSTER_NAME} -o jsonpath='{.status.conditions}' | jq .
```

### Check Addon Status

If you enabled ACM addons, verify they are running:

```bash
oc get managedclusteraddon -n ${CLUSTER_NAME}
```

Expected output:

```
NAME                          AVAILABLE   DEGRADED   PROGRESSING
application-manager           True
cert-policy-controller        True
cluster-proxy                 True
config-policy-controller      True
governance-policy-framework   True
work-manager                  True
```

### Check Klusterlet on Hosting Cluster

On the hosting cluster, verify the klusterlet is deployed:

```bash
oc get pods -n klusterlet-${CLUSTER_NAME}
```

## Detaching a Hosted Cluster

To remove a hosted cluster from MCE/ACM management without destroying it:

```bash
oc delete managedcluster ${CLUSTER_NAME}
```

**Note:** This only removes the cluster from management. The hosted cluster continues to run.

