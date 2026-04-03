# Configuring Azure Private Hosted Clusters with hypershift-addon

This document explains how to configure `hypershift-addon` so the addon agent installs the HyperShift operator with Azure private platform settings.

The goal is to make operator installation include:

- `--private-platform Azure`
- `--azure-pls-resource-group <resource-group>`
- `--azure-private-secret hypershift-operator-azure-credentials` (injected automatically by the addon)

## What this enables

With this configuration:

- The addon agent reads Azure private install flags from `hypershift-operator-install-flags` in `local-cluster`.
- The addon agent detects `hypershift-operator-azure-credentials` on hub (`local-cluster` namespace), copies it to `hypershift` namespace on spoke, and re-installs the operator when this secret changes.
- The HyperShift operator is installed in Azure private mode and can be used to create private Azure hosted clusters.

## Prerequisites

- ACM/MCE with `hypershift-addon` enabled on `local-cluster`.
- `oc` CLI access to the hub cluster.
- Azure private connectivity prerequisites for your environment (networking, DNS, permissions, and resource group for Private Link Service components).
- HyperShift CLI (`hcp`) available for creating hosted clusters.

## 1) Prepare Azure private credentials file

Create the credentials file that will be stored in the secret as key `credentials`.

If you already have an Azure credentials file used by `hcp create cluster azure`, you can reuse it and skip to step 2.

Example flow to create a service principal and file:

```bash
AZ_SUBSCRIPTION_ID="<subscription-id>"
AZ_TENANT_ID="<tenant-id>"
AZ_LOCATION="eastus"
AZ_RG="<resource-group>"

az login
az account set --subscription "${AZ_SUBSCRIPTION_ID}"

SP_JSON=$(az ad sp create-for-rbac \
  --name "hypershift-private-sp" \
  --role Contributor \
  --scopes "/subscriptions/${AZ_SUBSCRIPTION_ID}" \
  --sdk-auth)
```

Extract values and write a credentials env file:

```bash
AZ_APP_ID=$(echo "${SP_JSON}" | jq -r '.clientId')
AZ_PASSWORD=$(echo "${SP_JSON}" | jq -r '.clientSecret')

cat > azure-private-credentials.json <<EOF
AZURE_CLIENT_ID=${AZ_APP_ID}
AZURE_CLIENT_SECRET=${AZ_PASSWORD}
AZURE_TENANT_ID=${AZ_TENANT_ID}
AZURE_SUBSCRIPTION_ID=${AZ_SUBSCRIPTION_ID}
AZURE_RESOURCE_GROUP=${AZ_RG}
AZURE_LOCATION=${AZ_LOCATION}
EOF
```

Validate file content before creating the secret:

```bash
cat azure-private-credentials.json
```

## 2) Create Azure private credentials secret on hub

Create the secret in `local-cluster` namespace with key `credentials`:

```bash
oc create secret generic hypershift-operator-azure-credentials \
  -n local-cluster \
  --from-file=credentials=./azure-private-credentials.json
```

## 3) Configure HyperShift operator install flags

Create or update `hypershift-operator-install-flags` in `local-cluster` namespace:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: hypershift-operator-install-flags
  namespace: local-cluster
data:
  installFlagsToAdd: "--private-platform Azure --azure-pls-resource-group abcd-rg"
  installFlagsToRemove: ""
```

Apply it:

```bash
oc apply -f hypershift-operator-install-flags.yaml
```

## 4) Verify addon-driven operator installation

Check addon status:

```bash
oc get managedclusteraddons -n local-cluster hypershift-addon
```

Check install jobs:

```bash
oc get jobs -n open-cluster-management-agent-addon
```

Inspect latest install job args and logs:

```bash
oc get job -n open-cluster-management-agent-addon -o yaml
oc logs -n open-cluster-management-agent-addon job/<hypershift-install-job-name>
```

Confirm Azure secret was copied to HyperShift namespace:

```bash
oc get secret hypershift-operator-azure-credentials -n hypershift
```

Confirm operator deployment includes private Azure flags:

```bash
oc get deploy operator -n hypershift -o yaml
```

## 5) Create Azure hosted cluster

After operator installation is healthy, create a hosted cluster using HyperShift CLI:

```bash
hcp create cluster azure --help
```

Then run your `hcp create cluster azure ...` command with your Azure-specific values.

## Behavior notes

- The addon checks watched resources every 2 minutes. If the Azure secret or install flags configmap changes, the addon re-installs HyperShift operator.
- The managed Azure private secret name must be:
  - `hypershift-operator-azure-credentials`
- If `--private-platform Azure` is set but `--azure-pls-resource-group` or the Azure credentials secret is missing, operator installation fails with a validation error.

## Troubleshooting

- If install job fails, inspect:
  - `oc describe job <job-name> -n open-cluster-management-agent-addon`
  - `oc logs job/<job-name> -n open-cluster-management-agent-addon`
- Ensure:
  - secret exists in `local-cluster` and has `credentials` key
  - configmap flags are syntactically correct and include required Azure private options
  - resource group in `--azure-pls-resource-group` exists and is correct
