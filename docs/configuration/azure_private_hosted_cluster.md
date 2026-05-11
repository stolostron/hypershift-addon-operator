# Azure Private Hosted Cluster Setup (Addon + HCP)

This document is the single end-to-end guide for creating Azure private hosted clusters with `hypershift-addon`.

It combines:

- addon-driven HyperShift operator configuration on `local-cluster`
- Azure Private Link prerequisites
- `hcp` cluster creation flow for private endpoint access

## Overview

For Azure private hosted clusters, the HyperShift operator must run with private platform flags:

- `--private-platform Azure`
- `--azure-pls-resource-group <resource-group>`
- `--azure-private-secret hypershift-operator-azure-credentials`

With `hypershift-addon`, this is handled through hub resources in `local-cluster`:

- `ConfigMap/hypershift-operator-install-flags`
- `Secret/hypershift-operator-azure-credentials` (copied by addon to `hypershift` namespace)

When either resource changes, the addon triggers HyperShift operator reinstall.

## Prerequisites

- Management cluster (Azure, OCP 4.22+ recommended) with MCE/ACM and `hypershift-addon` enabled on `local-cluster`
- `oc` logged into the management hub cluster
- Azure CLI (`az`) authenticated
- `hcp` CLI available
- Pull secret and OIDC issuer already prepared

## Permission Requirements

Your Azure service principal should have:

- **Subscription level**
  - `Contributor`
  - `User Access Administrator`
- **Microsoft Graph API**
  - `Application.ReadWrite.OwnedBy` (often requires DPTP request in managed environments)

## 1) Create Azure service principal and credentials files

Create a service principal:

```bash
SUBSCRIPTION_ID="$(az account show --query id -o tsv)"

az ad sp create-for-rbac \
  --name "hcp-azure-sp" \
  --role Contributor \
  --scopes "/subscriptions/${SUBSCRIPTION_ID}"
```

Optionally add User Access Administrator role:

```bash
SP_OBJECT_ID="$(az ad sp list --display-name hcp-azure-sp --query '[0].id' -o tsv)"
az role assignment create \
  --assignee-object-id "${SP_OBJECT_ID}" \
  --role "User Access Administrator" \
  --scope "/subscriptions/${SUBSCRIPTION_ID}"
```

Create one credentials file with the required JSON fields:

```bash
cat > azure-creds.json <<EOF
{
  "subscriptionId": "${SUBSCRIPTION_ID}",
  "tenantId": "<TENANT_ID>",
  "clientId": "<APP_ID>",
  "clientSecret": "<CLIENT_SECRET>"
}
EOF
```

Use this same `azure-creds.json` for both:

- `hcp` commands (`--azure-creds ./azure-creds.json`)
- the addon-managed secret payload (`credentials` key)

Quick validation:

```bash
jq -r '.subscriptionId,.tenantId,.clientId' azure-creds.json
cat azure-creds.json
```

## 2) Create Azure private secret on hub (`local-cluster`)

Create/update the secret that addon watches and syncs:

```bash
oc create secret generic hypershift-operator-azure-credentials \
  -n local-cluster \
  --from-file=credentials=./azure-creds.json \
  --dry-run=client -o yaml | oc apply -f -
```

## 3) Configure HyperShift operator install flags

Apply `hypershift-operator-install-flags` in `local-cluster`:

```bash
oc apply -f - <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: hypershift-operator-install-flags
  namespace: local-cluster
data:
  installFlagsToAdd: >-
    --private-platform Azure
    --azure-pls-resource-group=<MC_INFRA_RESOURCE_GROUP>
    --azure-private-secret=hypershift-operator-azure-credentials
  installFlagsToRemove: ""
EOF
```

Optional:

- add `--limit-crd-install=Azure` when your environment requires it

## 4) Verify addon-driven HyperShift operator reinstall

```bash
oc get managedclusteraddons -n local-cluster hypershift-addon
oc get jobs -n open-cluster-management-agent-addon
oc logs -n open-cluster-management-agent-addon job/<hypershift-install-job-name>
oc get secret hypershift-operator-azure-credentials -n hypershift
oc rollout status deployment/operator -n hypershift
```

Expected:

- secret exists in `hypershift` namespace
- operator rollout completes
- operator args include Azure private flags

## 5) Create NAT subnet for private endpoint access

Azure Private Link Service needs a dedicated subnet with network policies disabled.

```bash
MC_RG=$(oc get infrastructure cluster -o jsonpath='{.status.platformStatus.azure.resourceGroupName}')
INFRA_ID=$(oc get infrastructures cluster -o jsonpath='{.status.infrastructureName}')
MC_VNET="${INFRA_ID}-vnet"

az network vnet subnet create \
  --resource-group "${MC_RG}" \
  --vnet-name "${MC_VNET}" \
  --name pls-nat-subnet \
  --address-prefixes 10.1.0.0/24 \
  --disable-private-link-service-network-policies true

NAT_SUBNET_ID=$(az network vnet subnet show \
  --resource-group "${MC_RG}" \
  --vnet-name "${MC_VNET}" \
  --name pls-nat-subnet \
  --query id -o tsv)
```

## 6) Create workload identities

```bash
hcp create iam azure \
  --name <HC_NAME> \
  --infra-id <INFRA_ID> \
  --azure-creds ./azure-creds.json \
  --location <LOCATION> \
  --resource-group-name <RESOURCE_GROUP_NAME> \
  --oidc-issuer-url <OIDC_ISSUER_URL> \
  --output-file workload-identities.json
```

## 7) Create hosted cluster infrastructure

```bash
hcp create infra azure \
  --name <HC_NAME> \
  --infra-id <INFRA_ID> \
  --azure-creds ./azure-creds.json \
  --base-domain <BASE_DOMAIN> \
  --location <LOCATION> \
  --workload-identities-file workload-identities.json \
  --assign-identity-roles \
  --dns-zone-rg-name <DNS_ZONE_RG> \
  --output-file infra-output.json
```

## 8) Create the private Azure hosted cluster

Required private flags:

- `--endpoint-access Private`
- `--endpoint-access-private-nat-subnet-id "${NAT_SUBNET_ID}"`

```bash
hcp create cluster azure \
  --name <HC_NAME> \
  --namespace clusters \
  --azure-creds ./azure-creds.json \
  --location <LOCATION> \
  --node-pool-replicas 2 \
  --base-domain <BASE_DOMAIN> \
  --pull-secret ./pull-secret.json \
  --generate-ssh \
  --infra-json infra-output.json \
  --release-image <RELEASE_IMAGE> \
  --external-dns-domain <EXTERNAL_DNS_DOMAIN> \
  --sa-token-issuer-private-key-path ./serviceaccount-signer.private \
  --oidc-issuer-url <OIDC_ISSUER_URL> \
  --dns-zone-rg-name <DNS_ZONE_RG> \
  --auto-assign-roles \
  --workload-identities-file workload-identities.json \
  --diagnostics-storage-account-type Managed \
  --endpoint-access Private \
  --endpoint-access-private-nat-subnet-id "${NAT_SUBNET_ID}"
```

Do not set `--external-dns-domain` to `<HC_NAME>.<BASE_DOMAIN>`; use a separate domain (for example, `external-dns.<BASE_DOMAIN>`) to avoid DNS shadowing with Azure private DNS zones.

## 9) Optional local console access verification (no VPN)

Use port-forward and local SNI routing if direct private network access is unavailable.

```bash
oc -n clusters get secret <HC_NAME>-admin-kubeconfig \
  -o jsonpath='{.data.kubeconfig}' | base64 -d > /tmp/<HC_NAME>.kubeconfig

oc -n clusters-<HC_NAME> port-forward svc/private-router 16443:443
```

In another terminal:

```bash
oc --kubeconfig /tmp/<HC_NAME>.kubeconfig \
  --server=https://127.0.0.1:16443 \
  --insecure-skip-tls-verify=true whoami
```

## Behavior notes

- Addon checks watched resources on a periodic interval (around 2 minutes) and reinstalls the operator when needed.
- Managed secret name must remain `hypershift-operator-azure-credentials`.
- If private Azure flags are set but required secret/resource-group data is missing, installation fails validation.

## Troubleshooting

- Install job debugging:
  - `oc describe job <job-name> -n open-cluster-management-agent-addon`
  - `oc logs job/<job-name> -n open-cluster-management-agent-addon`
- Confirm prerequisites:
  - secret exists in `local-cluster` with `credentials` key
  - secret is copied to `hypershift`
  - configmap flags are valid
  - `--azure-pls-resource-group` exists and is correct
  - NAT subnet ID points to subnet with private link policies disabled

## Legacy environments (optional)

If your environment does not yet include bundled Azure private-link support in shipped MCE/HyperShift components, you may need custom HyperShift operator image and custom `hcp` binary. Prefer default bundled components unless you specifically hit a version gap.
