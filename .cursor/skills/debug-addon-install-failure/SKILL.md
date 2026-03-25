---
name: debug-addon-install-failure
description: >-
  Diagnoses HyperShift operator install and upgrade failures on spoke clusters
  managed by the hypershift-addon agent. Use when the HyperShift operator fails
  to install, an install job fails, secrets are missing, or the addon reports
  installation errors.
---

# Debug Addon Install Failure

## Overview

The HyperShift operator install lifecycle is managed by `pkg/install/UpgradeController`. It runs every 2 minutes, checks whether reinstallation is needed (startup, previous failure, changed secrets/configmaps, image upgrade), then creates a Kubernetes batch Job that runs `hypershift install`.

## Diagnostic Workflow

Copy this checklist and track progress:

```
- [ ] Step 1: Check if install is disabled
- [ ] Step 2: Inspect UpgradeController logs
- [ ] Step 3: Check the install Job
- [ ] Step 4: Verify hub secrets
- [ ] Step 5: Verify hub ConfigMaps
- [ ] Step 6: Check operator deployment
- [ ] Step 7: Inspect metrics
- [ ] Step 8: Check MCE-managed annotation
```

### Step 1: Check if install is disabled

The agent skips install when `DISABLE_HO_MANAGEMENT=true`. Check the agent deployment env vars:

```bash
kubectl get deploy hypershift-addon-agent -n open-cluster-management-agent-addon -o jsonpath='{.spec.template.spec.containers[0].env}' | jq .
```

If `DISABLE_HO_MANAGEMENT` is `true`, the UpgradeController logs `"hypershift operator management is disabled"` and does nothing.

### Step 2: Inspect UpgradeController logs

Look for these key log lines in the agent pod:

```bash
kubectl logs -n open-cluster-management-agent-addon deploy/hypershift-addon-agent | grep -E "(re-installation|install|failed|skip|bucket|private link|external dns|configmap|image.*changed)"
```

Key log patterns:
- `"hypershift operator re-installation is required"` — install will proceed
- `"no change in hypershift operator images, secrets and install flags"` — skipped (no change detected)
- `"hypershift operator exists but not deployed by addon, skip update"` — `not-by-mce` annotation blocks install
- `"failed to install hypershift operator"` — install failed; `installfailed` flag set for retry next cycle
- `"bucket secret(...) not found on the hub"` — S3 OIDC secret missing (non-AWS path)
- `"private link secret(...) not found on the hub"` — private link not configured
- `"HyperShift install job: <name> completed successfully"` — success

### Step 3: Check the install Job

Install jobs are created in the addon namespace with prefix `hypershift-install-job-`:

```bash
kubectl get jobs -n open-cluster-management-agent-addon -l 'batch.kubernetes.io/job-name' --sort-by='.metadata.creationTimestamp'
```

Jobs have:
- `backoffLimit: 0` (no retries)
- `activeDeadlineSeconds: 600` (10 min timeout)
- `ttlSecondsAfterFinished: 172800` (48h cleanup)
- Service account: `hypershift-addon-agent-sa`

Check failed job pods:

```bash
kubectl get pods -n open-cluster-management-agent-addon -l job-name=<JOB_NAME>
kubectl logs -n open-cluster-management-agent-addon <POD_NAME>
```

Common job failures:
- **Image pull error**: pull secret missing or incorrect
- **RBAC error**: `hypershift-addon-agent-sa` lacks permissions
- **Timeout**: job exceeded 600s deadline
- **Bad args**: invalid install flags from ConfigMap

### Step 4: Verify hub secrets

The agent reads these secrets from namespace `<clusterName>` on the hub:

| Secret Name | Purpose | Required |
|---|---|---|
| `hypershift-operator-oidc-provider-s3-credentials` | S3 OIDC bucket (AWS) | No (non-AWS skips) |
| `hypershift-operator-private-link-credentials` | AWS private link | No |
| `hypershift-operator-external-dns-credentials` | External DNS | No |
| `ho-pull-secret` | Operator pull secret override | No |

For the S3 secret, verify required keys: `bucket`, `region`, `credentials` (or `aws-access-key-id` + `aws-secret-access-key`).

```bash
# On the hub cluster
kubectl get secret hypershift-operator-oidc-provider-s3-credentials -n <CLUSTER_NAME> -o jsonpath='{.data}' | jq 'to_entries[] | {key: .key, value: (.value | @base64d | length | tostring) + " bytes"}'
```

The agent also saves copies locally in the addon namespace after successful install. Compare:

```bash
kubectl get secret -n open-cluster-management-agent-addon -l '!batch.kubernetes.io/job-name'
```

### Step 5: Verify hub ConfigMaps

| ConfigMap Name | Namespace | Purpose |
|---|---|---|
| `hypershift-operator-install-flags` | `<clusterName>` on hub | Extra install flags (`installFlagsToAdd`, `installFlagsToRemove`) |
| `hypershift-override-images` | `<clusterName>` on hub | Image overrides for downstream imagestream |
| `hypershift-operator-imagestream` | addon NS on spoke | Downstream imagestream (from MCE) |

Check install flags:

```bash
kubectl get cm hypershift-operator-install-flags -n <CLUSTER_NAME> -o yaml
```

The `installFlagsToAdd` and `installFlagsToRemove` fields are space-separated flag strings. Malformed flags cause install job failures.

### Step 6: Check operator deployment

After install, verify the operator exists:

```bash
kubectl get deploy operator -n hypershift
kubectl get deploy external-dns -n hypershift
```

Check if the operator was deployed by MCE (addon-managed):

```bash
kubectl get deploy operator -n hypershift -o jsonpath='{.metadata.annotations.hypershift\.open-cluster-management\.io/not-by-mce}'
```

If this annotation is `"true"`, the addon will **not** manage the operator.

### Step 7: Inspect metrics

Relevant metrics (scraped from addon agent on `:8080`):

| Metric | Meaning |
|---|---|
| `mce_hs_addon_install_failing_gauge` | 1 = last install failed, 0 = OK |
| `mce_hs_addon_in_installation_or_upgrade` | 1 = install in progress |
| `mce_hs_addon_installation_or_upgrade_failed_count` | Cumulative failures since last success |
| `mce_hs_addon_aws_s3_bucket_secret_configured` | 1 = S3 secret used |
| `mce_hs_addon_hub_resource_sync_failure_count` | Hub secret/CM sync failures by resource type |

### Step 8: Check MCE-managed annotation

The `operatorUpgradable()` function gates installs. If the operator deployment has:

```yaml
annotations:
  hypershift.open-cluster-management.io/not-by-mce: "true"
```

The addon logs `"hypershift operator exists but not deployed by addon, skip update"` and returns without error.

## Install Flow Summary

```
UpgradeController.Start() [every 2 min]
  ├─ DISABLE_HO_MANAGEMENT=true? → skip
  ├─ startup || installfailed || installOptionsChanged() || upgradeImageCheck()
  │   └─ runHypershiftInstall()
  │       ├─ operatorUpgradable() → check not-by-mce annotation
  │       ├─ Ensure hypershift namespace
  │       ├─ Copy pull secret
  │       ├─ Read S3/private-link/ext-DNS secrets from hub
  │       ├─ Read install-flags ConfigMap from hub
  │       ├─ Build install args
  │       ├─ If withOverride: read downstream imagestream, compare images
  │       ├─ runHyperShiftInstallJob() → batch Job
  │       ├─ Poll job completion (10s interval, 2min timeout)
  │       ├─ Save secrets/CM locally on spoke
  │       └─ Update operator + ext-DNS deployments
  └─ No changes? → skip
```

## Common Root Causes

1. **Missing hub secrets**: S3/private-link secrets not created in `<clusterName>` namespace on hub
2. **Invalid credentials format**: S3 secret missing `bucket` or `region` keys
3. **Image pull failures**: pull secret not propagated to `hypershift` namespace
4. **Install flags malformed**: bad values in `hypershift-operator-install-flags` ConfigMap
5. **RBAC missing**: `hypershift-addon-agent-sa` service account lacks cluster-admin-like permissions
6. **Imagestream decode failure**: `hypershift-operator-imagestream` ConfigMap has corrupt base64 data
7. **Operator managed externally**: `not-by-mce: true` annotation blocks addon management
