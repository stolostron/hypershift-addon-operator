# ACM Resources for Hosted Clusters

This directory contains example ACM resources that are automatically created when a hosted cluster is imported.

## What Gets Created Automatically

When HyperShift creates a hosted cluster and it becomes Available, ACM automatically creates these resources through the auto-import process:

### 1. ManagedCluster

**Created by:** hypershift-addon-agent (ACM controller)  
**Purpose:** Represents the hosted cluster in ACM  
**File:** [managedcluster-example.yaml](./managedcluster-example.yaml)

Key points:
- Contains critical annotations (`klusterlet-deploy-mode: "Hosted"`, `hosting-cluster-name`, `open-cluster-management/created-via: hypershift`)
- Labels indicate auto-import (`vendor: auto-detect`, `cloud: auto-detect`)
- Status conditions show import progress (Joined, Available)

### 2. KlusterletAddonConfig

**Created by:** ACM controllers  
**Purpose:** Configures which ACM add-ons are enabled  
**File:** [klusterletaddonconfig-example.yaml](./klusterletaddonconfig-example.yaml)

Enables:
- Application manager (for app deployment)
- Policy controller (for governance)
- Search collector (for discoverability)
- Certificate and IAM policy controllers

### 3. ManagedClusterInfo

**Created by:** klusterlet (populated dynamically)  
**Purpose:** Contains detailed cluster information  
**File:** [managedclusterinfo-example.yaml](./managedclusterinfo-example.yaml)

Populated with:
- Kubernetes/OpenShift version
- Node inventory with capacity
- Distribution details (OCP version, upgrade info)
- Console and API URLs

## You Don't Create These

**Important:** These resources are created automatically by ACM. You do NOT need to manually create them. They are shown here for:

1. **Educational purposes** - Understanding what ACM creates
2. **Troubleshooting** - Verifying the correct resources exist
3. **Reference** - Checking expected structure and annotations

## Verification

After your hosted cluster is provisioned, verify these resources were created:

```bash
# Check ManagedCluster
kubectl get managedcluster <cluster-name>

# Check annotations
kubectl get managedcluster <cluster-name> -o yaml | grep -A 5 annotations

# Check KlusterletAddonConfig
kubectl get klusterletaddonconfig -n <cluster-name> <cluster-name>

# Check ManagedClusterInfo
kubectl get managedclusterinfo -n <cluster-name> <cluster-name>
```

## Troubleshooting

If these resources don't exist after 15 minutes:

1. **ManagedCluster missing** → Check hypershift-addon-agent logs
2. **Klusterlet not deployed** → Check cluster-import-controller logs
3. **ManagedClusterInfo not populated** → Check klusterlet logs

See [../../../../docs/gitops/02-troubleshooting.md](../../../../docs/gitops/02-troubleshooting.md) for detailed troubleshooting steps.

## Learn More

- [01-acm-integration-overview.md](../../../../docs/gitops/01-acm-integration-overview.md) - ACM integration and auto-import (overview through patterns and troubleshooting)
