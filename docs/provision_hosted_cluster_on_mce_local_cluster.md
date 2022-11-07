# Provisioning hosted clusters on MCE (Without Hypershift Deployment)

Configuring hosted control planes requires a hosting service cluster and a hosted cluster. By deploying the HyperShift operator on an existing cluster, you can make that cluster into a hosting service cluster and start the creation of the hosted cluster. 

Hosted control planes is a Technology Preview feature, so the related components are disabled by default. Enable the feature by editing the `multiclusterengine` custom resource to set the `spec.overrides.components[?(@.name=='hypershift-preview')].enabled` to `true`. 

Enter the following command to ensure that the hosted control planes feature is enabled, replacing `multiclusterengine` with your MCE's instance name:

```bash
$ oc patch mce multiclusterengine --type=merge -p '{"spec":{"overrides":{"components":[{"name":"hypershift-preview","enabled": true}]}}}'
```

## Configuring the hosting service cluster

You can deploy hosted control planes by configuring an existing cluster to function as a hosting service cluster. The hosting service cluster is the OCP cluster where the control planes are hosted, and can be the hub cluster or one of the OCP managed clusters. In this section, we will use hypershift-addon to install a HyperShift operator onto the hub cluster, otherwise known as the local-cluster in MCE/ACM.

### Prerequisites

You must have the following prerequisites to configure a hosting service cluster: 

- Multicluster engine operator (MCE) installed on OCP cluster.

- MCE has at least one managed OCP cluster. We will make this OCP managed cluster a hypershift management cluster. In this example, we will use the MCE hub cluster as the hypershift management cluster. This requires importing the hub cluster as an OCP managed cluster called `local-cluster`:

  ```bash
  $ oc apply -f - <<EOF
  apiVersion: cluster.open-cluster-management.io/v1
  kind: ManagedCluster
  metadata:
    labels:
      local-cluster: "true"
      cloud: auto-detect
      vendor: auto-detect
    name: local-cluster
  spec:
    hubAcceptsClient: true
    leaseDurationSeconds: 60
  EOF
  ```

### Configuring the hosting service cluster

Complete the following steps on the cluster where the multicluster engine operator is installed to enable an {ocp-short} managed cluster as a hosting service cluster:

1. If you plan to provision hosted clusters on the AWS platform, create an OIDC s3 credentials secret for the HyperShift operator, and name it `hypershift-operator-oidc-provider-s3-credentials`. It should reside in managed cluster namespace (i.e., the namespace of the managed cluster that will be used as the hosting service cluster). If you used `local-cluster`, then create the secret in the `local-cluster` namespace

The secret must contain 3 fields:

- `bucket`: An S3 bucket with public access to host OIDC discovery documents for your HyperShift clusters
- `credentials`: A reference to a file that contains the credentials of the `default` profile that can access the bucket. By default, HyperShift only uses the `default` profile to operate the `bucket`.
- `region`: Region of the S3 bucket

See [Getting started](https://hypershift-docs.netlify.app/getting-started). in the HyperShift documentation for more information about the secret. The following example shows a sample AWS secret creation CLI template:

```bash
$ oc create secret generic hypershift-operator-oidc-provider-s3-credentials --from-file=credentials=$HOME/.aws/credentials --from-literal=bucket=<s3-bucket-for-hypershift> --from-literal=region=<region> -n local-cluster
```

**Note:** Disaster recovery backup for the secret is not automatically enabled. Run the following command to add the label that enables the `hypershift-operator-oidc-provider-s3-credentials` secret to be backed up for disaster recovery:

```bash
$ oc label secret hypershift-operator-oidc-provider-s3-credentials -n local-cluster cluster.open-cluster-management.io/backup=true
```

### Enabling External DNS
If you plan to use service-level DNS for Control Plane Service, create an external DNS credential secret for the HyperShift operator, and name it `hypershift-operator-external-dns-credentials`. It should reside in the managed cluster namespace (i.e., the namespace of the managed cluster that will be used as the hosting service cluster). If you used `local-cluster`, then create the secret in the `local-cluster` namespace

The secret must contain 3 fields:

- `provider`: DNS provider that manages the service-level DNS zone (example: aws)
- `domain-filter`: The service-level domain
- `credentials`: *(Optional, only when using aws keys) - For all external DNS types, a credential file is supported
- `aws-access-key-id`: *OPTIONAL* - When using AWS DNS service, credential access key id
- `aws-secret-access-key`: *OPTIONAL* - When using AWS DNS service, credential access key secret

For details, please check: [HyperShift Project Documentation](https://hypershift-docs.netlify.app/how-to/external-dns/). For convenience, you can create this secret using the CLI by:

```bash
$ oc create secret generic hypershift-operator-external-dns-credentials --from-literal=provider=aws --from-literal=domain-filter=service.my.domain.com --from-file=credentials=<credentials-file> -n local-cluster
```

Add the special label to the `hypershift-operator-external-dns-credentials` secret so that the secret is backed up for disaster recovery.

  ```bash
  $ oc label secret hypershift-operator-external-dns-credentials -n local-cluster cluster.open-cluster-management.io/backup=true
  ```

### Enable on a HostedCluster

1. Install the HyperShift add-on. The cluster that hosts the HyperShift operator is the management cluster. This step uses the `ManagedClusterAddon` hypershift-addon to install the HyperShift operator on a managed cluster. The namespace will be the managed cluster on which you want to install the HyperShift operator on. In this case, we will use the MCE hub cluster, so we'll set `local-cluster` for this value:
  
    ```bash
    $ oc apply -f - <<EOF
    apiVersion: addon.open-cluster-management.io/v1alpha1
    kind: ManagedClusterAddOn
    metadata:
      name: hypershift-addon
      namespace: local-cluster
    spec:
      installNamespace: open-cluster-management-agent-addon
    EOF
    ```

2. Confirm that the `hypershift-addon` is installed by running the following command:
  
    ```bash
    $ oc get managedclusteraddons -n local-cluster hypershift-addon
    NAME               AVAILABLE   DEGRADED   PROGRESSING
    hypershift-addon   True
    ```

Your HyperShift add-on is installed and the management cluster is available to manage HyperShift hosted clusters.

## Provision a HyperShift hosted cluster on AWS

After installing the HyperShift operator and enabling an existing cluster as a hosting service cluster, you can provision a hypershift hosted cluster via the hypershift CLI.

1. Set the following environment variables

    ```bash
    export REGION=us-east-1
    export CLUSTER_NAME=clc-dhu-hs1
    export INFRA_ID=clc-dhu-hs1
    export BASE_DOMAIN=dev09.red-chesterfield.com
    export AWS_CREDS=$HOME/dhu-aws
    export PULL_SECRET=/Users/dhuynh/dhu-pull-secret.txt
    export BUCKET_NAME=acmqe-hypershift
    export BUCKET_REGION=us-east-1
    export INFRA_OUTPUT_FILE=$HOME/hostedcluster_infra.json
    export IAM_OUTPUT_FILE=$HOME/hostedcluster_iam.json
    ```
    <b>Note:</b> <i>in order for the cluster to show correctly in the MCE console UI, it is recommended to keep the `CLUSTER_NAME` and `INFRA_ID` the same.</i>

2. Ensure you are logged into your hub cluster.

3. If you do not want to create the infrastructure and IAM pieces separately, skip to step 6. Otherwise run the following command to create the infrastructure first:

    ```bash
    $ hypershift create infra aws --name $CLUSTER_NAME \
        --aws-creds $AWS_CREDS \
        --base-domain $BASE_DOMAIN \
        --infra-id $CLUSTER_NAME \
        --region $REGION \
        --output-file $INFRA_OUTPUT_FILE
    ```

4. In order to create the related IAM pieces, we'll need some info from the infra output.

    ```bash
    $ cat $INFRA_OUTPUT_FILE                                                                                          
    {
      "region": "us-east-1",
      "zone": "",
      "infraID": "clc-dhu-hs1",
      "machineCIDR": "10.0.0.0/16",
      "vpcID": "vpc-07541ba034d5a26a1",
      "zones": [
        {
          "name": "us-east-1a",
          "subnetID": "subnet-0e17ee93963c8bf34"
        }
      ],
      "securityGroupID": "sg-0da668fbe75efeb43",
      "Name": "clc-dhu-hs1",
      "baseDomain": "dev09.red-chesterfield.com",
      "publicZoneID": "Z00953301HRDK0M0YKQGE",
      "privateZoneID": "Z02815081BZYZVASX5HII",
      "localZoneID": "Z01446842N9Z8MWQL967F",
      "proxyAddr": ""
    }
    ```
Specically, we'll need the `publicZoneID`, `privateZoneID`, and `localZoneID`

5. Create the IAM:

    ```bash
    $ hypershift create iam aws --infra-id $CLUSTER_NAME \
        --aws-creds $AWS_CREDS \
        --oidc-storage-provider-s3-bucket-name $BUCKET_NAME \
        --oidc-storage-provider-s3-region $BUCKET_REGION \
        --region $REGION \
        --public-zone-id Z00953301HRDK0M0YKQGE \
        --private-zone-id Z02815081BZYZVASX5HII \
        --local-zone-id Z01446842N9Z8MWQL967F \
        --output-file $IAM_OUTPUT_FILE
    ```

6. We can use the `hypershift create cluster aws` command to create our hosted cluster. If you created the infrastructure and IAM pieces separately, we can specify them as arguments via `--infra-json` and `--iam-json`:

    ```bash
    $ hypershift create cluster aws \
        --name $CLUSTER_NAME \
        --infra-id $INFRA_ID \
        --infra-json $INFRA_OUTPUT_FILE \
        --iam-json $IAM_OUTPUT_FILE \
        --aws-creds $AWS_CREDS \
        --pull-secret $PULL_SECRET \
        --region $REGION \
        --generate-ssh \
        --node-pool-replicas 3 \
        --namespace local-cluster \
        --render > hosted-cluster-cr-render.yaml
    ```

Note: This command will also perform what `hypershift create infra` and `hypershift create iam` does if those fields are not specified, however we'll also need to provide the required arguments for those portions (such as the S3 bucket name)

7. Edit the `hosted-cluster-cr-render.yaml` above by adding this annotation to the HostedCluster CR:

    ```yaml
      annotations:
        cluster.open-cluster-management.io/managedcluster-name: CLUSTER_NAME
    ```
If not included, the name used when importing the cluster is the `InfraID` value.

8. Now we can apply the CR to the hub:

    ```bash
    $ oc apply -f hosted-cluster-cr-render.yaml
    ```

9. In order to complete the import process, we also need to create the managed cluster resource:
    ## How to name your ManagedCluster
    * The CLUSTER_NAME listed below will be the registred name of your hosted cluster in ACM/MCE.  
    * When the name is provided in the `HostedCluster` annotation `cluster.open-cluster-management.io/managedcluster-name`, it must match CLUSTER_NAME when creating the `ManagedCluster` resource.
    * When the annotation is not present, the CLUSTER_NAME for the `ManagedCluster` must be the `InfraID` of the `HostedCluster`.
    * If the annotation and `ManagedCluster` name do not match, the console will display the cluster as `Pending import`, and it can not be used by ACM/MCE. The same state will occur when the annotation is not present and the `ManagedCluster` name does not equal the `HostedCluster` Infra-ID value.
    
    ```bash
    $ cat <<EOF | oc apply -f -
    apiVersion: cluster.open-cluster-management.io/v1
    kind: ManagedCluster
    metadata:  
      annotations:    
        import.open-cluster-management.io/hosting-cluster-name: local-cluster    
        import.open-cluster-management.io/klusterlet-deploy-mode: Hosted
        open-cluster-management/created-via: other  
      labels:    
        cloud: auto-detect    
        cluster.open-cluster-management.io/clusterset: default    
        name: $CLUSTER_NAME   
        vendor: OpenShift  
      name: $CLUSTER_NAME
    spec:  
      hubAcceptsClient: true  
      leaseDurationSeconds: 60
    EOF
    ```


10. Check the status of your hosted cluster via:

    ```bash
    $ oc get hostedclusters -n local-cluster
    ```

11.  After the hosted cluster is created, it will be imported to the hub automatically, you can check it with:
  
      ```bash
     $ oc get managedcluster $INFRA_ID
     ```

Your hosted cluster is now created and imported to MCE/ACM, which should also be visible from the MCE console.

## Access the hosted cluster

The access secrets are stored in the hypershift-management-cluster namespace.
The formats of the secrets name are:

- kubeconfig secret: `<hostingNamespace>-<name>-admin-kubeconfig` (e.g clusters-hypershift-demo-admin-kubeconfig)
- kubeadmin password secret: `<hostingNamespace>-<name>-kubeadmin-password` (e.g clusters-hypershift-demo-kubeadmin-password)

## Destroying your hypershift Hosted cluster

Delete the hypershift cluster:

```bash
$ hypershift destroy cluster aws --name $CLUSTER_NAME --infra-id $INFRA_ID --aws-creds $AWS_CREDS --base-domain $BASE_DOMAIN
```

## Destroying your hypershift managed cluster

Delete the managed cluster resource:

```bash
$ oc delete managedcluster $INFRA_ID
```

## Destroying hypershift operator

Delete the hypershift-addon

```bash
$ oc delete managedclusteraddon -n local-cluster hypershift-addon
```