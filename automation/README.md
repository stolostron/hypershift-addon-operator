# Hypershift Hosted Cluster Discovery Automation

This directory contains automation tools for the manual steps described in `docs/discovering_hostedclusters.md`.

## Overview

The manual discovery process involves several phases:
1. **ACM Hub Setup** - Configure addon deployment configs and cluster management addons
2. **MCE Import** - Import MCE clusters as managed clusters
3. **Hypershift Addon Enablement** - Enable hypershift addon on MCE managed clusters
4. **Auto-Import Configuration** - Set up policies for automatic hosted cluster import

## Automation Approaches

### 1. Shell Script Automation (Quick Start)
- `scripts/setup-acm-hub.sh` - Automate ACM hub configuration
- `scripts/import-mce-cluster.sh` - Import individual MCE clusters
- `scripts/enable-hypershift-addon.sh` - Enable hypershift addon on MCE clusters
- `scripts/setup-auto-import-policy.sh` - Deploy auto-import policies

### 2. Kubernetes Controller (Production Ready)
- `controllers/mce-discovery-controller.go` - Custom controller for automated MCE discovery
- `manifests/` - Kubernetes manifests for deploying the controller

### 3. GitOps/Policy Templates
- `gitops/` - ArgoCD/Flux templates for declarative management
- `policies/` - ACM policy templates for automated configuration

## Quick Start

1. **Setup ACM Hub Configuration:**
   ```bash
   ./scripts/setup-acm-hub.sh
   ```

2. **Import MCE Clusters:**
   ```bash
   ./scripts/import-mce-cluster.sh --cluster-name mce-a --api-url https://api.mce-a.example.com:6443
   ```

3. **Enable Hypershift Addon:**
   ```bash
   ./scripts/enable-hypershift-addon.sh --clusters mce-a,mce-b
   ```

4. **Deploy Auto-Import Policy:**
   ```bash
   ./scripts/setup-auto-import-policy.sh
   ```

## Configuration

All scripts support configuration through environment variables or command-line arguments:

- `ACM_NAMESPACE` - ACM installation namespace (default: `multicluster-engine`)
- `ADDON_INSTALL_NAMESPACE` - Addon installation namespace (default: `open-cluster-management-agent-addon-discovery`)
- `BACKUP_ENABLED` - Enable backup labels (default: `true`)

## Prerequisites

- `oc` CLI tool
- `clusteradm` CLI tool
- Access to ACM hub cluster
- Access to MCE clusters for auto-import secret creation
