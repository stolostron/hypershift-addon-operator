# Azure Public Hosted Cluster Setup (Addon + HCP)

This document is the end-to-end guide for creating a public HyperShift HostedCluster on a
self-managed OCP cluster running on Azure (non-AKS), using workload identity and pre-created
infrastructure artifacts.

## Prerequisites

Install the following tools:

- `hcp` — HyperShift CLI ([installing the HyperShift CLI](../getting-started/installing_hypershift_cli.md))
- `oc` — OpenShift CLI
- `az` — Azure CLI (`az login` authenticated)
- `ccoctl` — Cloud Credential Operator CLI (from the OCP release image)
- `jq` / `yq` — JSON/YAML processors

### Required Azure permissions

Your Azure service principal should have:

- **Subscription level**
  - `Contributor`
  - `User Access Administrator`
- **Microsoft Graph API**
  - `Application.ReadWrite.OwnedBy` (often requires DPTP approval in managed environments)

## 1) Create Azure credentials file

Create `azure-creds.json`:

```json
{
  "subscriptionId": "your-subscription-id",
  "tenantId": "your-tenant-id",
  "clientId": "your-service-principal-client-id",
  "clientSecret": "your-service-principal-client-secret"
}
```

Keep this file outside of version control.

## 2) Configure OIDC issuer

HyperShift uses OIDC for workload identity. Create the storage account and OIDC keys once per
environment:

```bash
OIDC_STORAGE_ACCOUNT="youroidcstorageacct"  # globally unique Azure storage account name
OIDC_ISSUER_URL="https://${OIDC_STORAGE_ACCOUNT}.blob.core.windows.net/oidc"

# Generate signing keys
mkdir -p oidc-keys
ccoctl azure create-key-pair --output-dir oidc-keys

# Create the Azure storage account and upload the OIDC discovery document
ccoctl azure create-oidc-issuer \
  --storage-account-name "${OIDC_STORAGE_ACCOUNT}" \
  --output-dir oidc-keys \
  --credentials-requests-dir "" \
  --region centralus
```

> **Tip:** The OIDC issuer only needs to be created once. Reuse it for multiple HostedClusters.

## 3) Create workload identities

```bash
HCP_NAME="my-public-hcp"
LOCATION="centralus"
AZURE_CREDS="./azure-creds.json"
WORKLOAD_IDENTITIES_FILE="./workload-identities.json"

hcp create iam azure \
  --name "${HCP_NAME}" \
  --azure-creds "${AZURE_CREDS}" \
  --location "${LOCATION}" \
  --oidc-issuer-url "${OIDC_ISSUER_URL}" \
  --output-file "${WORKLOAD_IDENTITIES_FILE}"
```

## 4) Create Azure infrastructure

> **Important — INFRA_ID pitfall:** Do **not** reuse the management cluster's infra ID (e.g. from
> `oc get infrastructures cluster -o jsonpath='{.status.infrastructureName}'`). Using the
> management cluster's ID causes a name collision in Azure: the `PUT` request for the new load
> balancer will conflict with the existing load balancer and result in an
> `InvalidResourceReference` error because the existing LB has sub-resources (probes, rules)
> that the new request does not include.
>
> Always generate a unique INFRA_ID scoped to the hosted cluster:
>
> ```bash
> INFRA_ID="${HCP_NAME}-$(LC_ALL=C tr -dc 'a-z0-9' </dev/urandom | head -c 5)"
> ```

```bash
INFRA_OUTPUT_FILE="./infra-output.json"
RESOURCE_GROUP_NAME="<management-cluster-resource-group>"   # e.g. full-size-azure-4-22-xyz-rg
BASE_DOMAIN="acm-dev04.azure.devcluster.openshift.com"
DNS_ZONE_RG_NAME="os4-common"                               # RG containing the base-domain DNS zone
PULL_SECRET="./pull-secret.json"

hcp create infra azure \
  --name "${HCP_NAME}" \
  --azure-creds "${AZURE_CREDS}" \
  --location "${LOCATION}" \
  --infra-id "${INFRA_ID}" \
  --base-domain "${BASE_DOMAIN}" \
  --resource-group-name "${RESOURCE_GROUP_NAME}" \
  --output-file "${INFRA_OUTPUT_FILE}"
```

## 5) Create the HostedCluster

```bash
hcp create cluster azure \
  --name "${HCP_NAME}" \
  --azure-creds "${AZURE_CREDS}" \
  --location "${LOCATION}" \
  --infra-id "${INFRA_ID}" \
  --base-domain "${BASE_DOMAIN}" \
  --pull-secret "${PULL_SECRET}" \
  --oidc-issuer-url "${OIDC_ISSUER_URL}" \
  --infra-json "${INFRA_OUTPUT_FILE}" \
  --workload-identities-file "${WORKLOAD_IDENTITIES_FILE}" \
  --namespace clusters \
  --node-pool-replicas 2 \
  --release-image "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64"
```

## 6) DNS records

HyperShift publishes hosted cluster services as Routes admitted by the management cluster's
ingress router. Each service hostname needs a DNS A record pointing to the **correct** router IP.

> **Important — find the right router IP:** The management cluster may have multiple ingress
> routers. Use `openssl s_client` to verify which IP actually serves the passthrough TLS route
> for your hosted cluster before creating DNS records:
>
> ```bash
> openssl s_client -connect <ip>:443 -servername "api-${HCP_NAME}.${BASE_DOMAIN}" </dev/null 2>&1 \
>   | grep 'subject='
> ```
>
> The IP whose certificate includes `CN=api-${HCP_NAME}.${BASE_DOMAIN}` is the correct one.

### Option A — Automated (ExternalDNS)

Configure ExternalDNS once. After that, DNS records are created automatically for every new
HostedCluster.

**Step 1 — Create the Azure credentials JSON for ExternalDNS**

```bash
cat > /tmp/external-dns-azure-creds.json << 'EOF'
{
  "tenantId": "<your-tenant-id>",
  "subscriptionId": "<your-subscription-id>",
  "resourceGroup": "<dns-zone-resource-group>",
  "aadClientId": "<your-service-principal-client-id>",
  "aadClientSecret": "<your-service-principal-client-secret>"
}
EOF
```

> `resourceGroup` is the resource group that **contains your Azure DNS zone** (not the cluster RG).

**Step 2 — Create the secret in `local-cluster`**

```bash
oc create secret generic hypershift-operator-external-dns-credentials \
  -n local-cluster \
  --from-literal=provider=azure \
  --from-literal=domain-filter="${BASE_DOMAIN}" \
  --from-literal=txt-owner-id="hypershift" \
  --from-file=credentials=/tmp/external-dns-azure-creds.json

rm /tmp/external-dns-azure-creds.json
```

**Step 3 — Wait for the HyperShift operator to restart (~2 min)**

The `hypershift-addon` controller detects the secret automatically and reinstalls the HyperShift
operator with ExternalDNS enabled.

```bash
oc get pods -n hypershift -w
```

**Step 4 — Verify ExternalDNS is running**

```bash
oc get deployment external-dns -n hypershift
```

### Option B — Manual

If ExternalDNS is not configured, add the 4 A records manually after the HostedCluster is
created.

Find the router IP:

```bash
oc get svc router-default -n openshift-ingress \
  -o jsonpath='{.status.loadBalancer.ingress[0].ip}'
```

> Verify this is the correct IP using the `openssl s_client` check described above.

Create A records for each service:

```bash
for name in api ignition konnectivity oauth; do
  az network dns record-set a add-record \
    --resource-group "${DNS_ZONE_RG_NAME}" \
    --zone-name "${BASE_DOMAIN}" \
    --record-set-name "${name}-${HCP_NAME}" \
    --ipv4-address "<router-ip>" \
    --ttl 60
done
```

## 7) Verify the cluster

Wait for the hosted control plane to become available:

```bash
oc get hostedcluster "${HCP_NAME}" -n clusters -w
```

Check cluster operators once the API server is reachable:

```bash
oc get clusteroperators --kubeconfig <guest-kubeconfig>
```

## 8) Destroy the HostedCluster

```bash
INFRA_ID="$(yq -p yaml -r '.infraID' ./infra-output.json)"

hcp destroy cluster azure \
  --name "${HCP_NAME}" \
  --namespace clusters \
  --azure-creds "${AZURE_CREDS}" \
  --location "${LOCATION}" \
  --infra-id "${INFRA_ID}" \
  --dns-zone-rg-name "${DNS_ZONE_RG_NAME}" \
  --cluster-grace-period 45m
```

> Read `INFRA_ID` from `infra-output.json` — do not pass a hard-coded value or you risk leaving
> orphaned Azure resources.

## Common pitfalls

| Symptom | Root cause | Fix |
|---------|-----------|-----|
| `InvalidResourceReference` on LB create | `INFRA_ID` collides with management cluster LB | Generate a unique `INFRA_ID` (see step 4) |
| `ExternalDNSReachable: no such host` | DNS records missing or wrong IP | Create/fix A records; verify IP with `openssl s_client` |
| `OSProvisioningTimedOut` on worker VMs | Ignition endpoint unreachable at VM boot time | Fix DNS records first, then delete failed `Machine` objects to trigger reprovisioning |
| DNS records exist but cluster stays unreachable | Records point to wrong router IP | Identify the correct ingress router IP using `openssl s_client` |
| Worker VMs keep timing out after DNS fix | VMs were created before DNS was correct | Delete the failed `Machine` objects: `oc delete machine.cluster.x-k8s.io -n clusters-<hcp-name> --all` |
