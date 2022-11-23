# Provisioning hosted clusters on MCE (Without Hypershift Deployment)

As per the [Hypershift Docs](https://hypershift-docs.netlify.app/), configuring hosted control planes requires a hosting service cluster and a hosted cluster. By deploying the HyperShift operator on an existing cluster via the Hypershift Add-on, you can make that cluster into a hosting service cluster and start the creation of the hosted cluster. 

Hosted control planes is a Technology Preview feature, so the related components are disabled by default. In Multicluster engine operator (MCE) 2.1, this was done using HypershiftDeployment. As of MCE 2.2, HypershiftDeployment is now obsolete and this guide will show how we can enable the feature follwed by deploying a hosted cluster on Amazon Web Services via MCE using the Hypershift command line binary.

## Configuring the hosting service cluster

You can deploy hosted control planes by configuring an existing cluster to function as a hosting service cluster. The hosting service cluster is the OCP cluster where the control planes are hosted, and can be the hub cluster or one of the OCP managed clusters. In this section, we will use hypershift-addon to install a HyperShift operator onto the hub cluster, otherwise known as the local-cluster in MCE/ACM.

### Prerequisites

You must have the following prerequisites to deploy the hosted cluster:

* MCE v2.2 installed on a OCP cluster
* Openshift `oc` command
* The Hypershift binary as a plugin to `oc`. This binary plugin is required in order to create and manage the hosted cluster in MCE. Get this binary by one of the following ways:
  1. Go to your Openshift cluster's console command line tools page. Select the Hosted Control Plane CLI tool and follow the instructions to set up `oc` plugin.
  2. Go to the [Getting started with Hosted Control Plane (Hypershift) CLI documentation](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/installing_hypershift_cli.md) and follow the instructions to set up `oc` plugin.
  3. Clone and build the binary from the [official Hypershift source](https://github.com/openshift/hypershift).
* MCE has at least one managed OCP cluster. We will make this OCP managed cluster a hypershift management cluster. In this example, we will use the MCE hub cluster as the hypershift management cluster. In MCE 2.2, local-cluster is now imported automatically. You can check the status of your hub cluster using the following `oc` command:

    ```bash
    $ oc get managedclusters local-cluster
    ```

### Creating the Amazon Web Services (AWS) S3 Secret

If you plan to provision hosted clusters on the AWS platform, create an OIDC s3 credentials secret for the HyperShift operator, and name it `hypershift-operator-oidc-provider-s3-credentials`. It should reside in managed cluster namespace (i.e., the namespace of the managed cluster that will be used as the hosting service cluster). As we're using the `local-cluster` as the hosted cluster, we'll create the secret in the `local-cluster` namespace here.

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

### Enabling the Hosted Control Plane feature

Enter the following command to ensure that the hosted control planes feature is enabled, replacing `multiclusterengine` with your MCE's instance name:

  ```bash
  $ oc patch mce multiclusterengine --type=merge -p '{"spec":{"overrides":{"components":[{"name":"hypershift-preview","enabled": true}]}}}'
  ```

**Note:** The AWS S3 secret must be created before creating the add-on. As enabling the preview creates the add-on automatically, be sure [to do that step first](#creating-the-amazon-web-services-aws-s3-secret).

**Note:** Disabling the feature will NOT delete the Hypershift add-on automatically and will cause it to be inoperable. If disabling, first modify or remove the add-on separately.

### Enabling the Hypershift Add-on

The Hypershift add-on is required on the management cluster to install the Hypershift operator. The namespace will be the managed cluster on which you want to install the HyperShift operator on. Again, we're using the MCE hub, `local-cluster`, in this guide which will have the hostingCluster value turned on by default.

The Hypershift add-on will be automatically installed after [enabling the Hosted Control Plane feature](#enabling-the-hosted-control-plane-feature).

Confirm that the `hypershift-addon` is installed by running the following command:
  
    ```bash
    $ oc get managedclusteraddons -n local-cluster hypershift-addon
    NAME               AVAILABLE   DEGRADED   PROGRESSING
    hypershift-addon   True        False
    ```

You run the following wait commands to wait for the addon to reach this state with a timeout:

    ```bash
    $ oc wait --for=condition=Degraded=True managedclusteraddons/hypershift-addon -n local-cluster --timeout=5m
    $ oc wait --for=condition=Available=True managedclusteraddons/hypershift-addon -n local-cluster --timeout=5m
    ```
Once complete, the HyperShift add-on is installed and the management cluster is available to create and manage hosted clusters.

## Provision a hosted cluster on AWS

After setting up the binary and enabling the existing cluster as a hosting service cluster, you can provision a hosted cluster via `oc hcp`

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
    ```
    <b>Note:</b> <i>in order for the cluster to show correctly in the MCE console UI, it is recommended to keep the `CLUSTER_NAME` and `INFRA_ID` the same.</i>

2. Ensure you are logged into your hub cluster.

3. If you do want to create the hypershift infrastructure and IAM pieces separately, refer to the [How do I create the hypershift infrastructure and IAM pieces separately?](#how-do-i-create-the-hypershift-infrastructure-and-iam-pieces-separately) section at the end. Otherwise run the following command to create the hosted cluster:

    ```bash
    $ oc hcp create cluster aws \
        --name $CLUSTER_NAME \
        --infra-id $INFRA_ID \
        --aws-creds $AWS_CREDS \
        --pull-secret $PULL_SECRET \
        --region $REGION \
        --generate-ssh \
        --node-pool-replicas 3 \
        --namespace local-cluster
    ```

For more options when interacting with the CLI, refer to [Hypershift Project Documentation for creating a HostedCluster](https://hypershift-docs.netlify.app/getting-started/#create-a-hostedcluster).

That's all! Your hosted cluster is now created.

Check the status of your hosted cluster via:

    ```bash
    $ oc get hostedclusters -n local-cluster
    ```

## Importing the Hosted cluster into MCE via UI

After creating the hosted cluster, the next step is to import it into MCE.

You can import do this from the MCE Console UI by going to Infrastructure > Clusters and then clicking the your $CLUSTER_NAME in the cluster list and clicking `Import hosted cluster`.

## Importing the Hosted Cluster into MCE via CLI

After creating the hosted cluster, the next step is to import it into MCE.

### How to name your ManagedCluster
* The $CLUSTER_NAME listed below will be the registred name of your hosted cluster in ACM/MCE.  
* If the annotation and `ManagedCluster` name do not match, the console will display the cluster as `Pending import`, and it can not be used by ACM/MCE. The same state will occur when the annotation is not present and the `ManagedCluster` name does not equal the `HostedCluster` Infra-ID value.

1. Add a required annotation to the HostedCluster CR by doing:

    ```bash
    $ cluster.open-cluster-management.io/hypershiftdeployment: local-cluster/$CLUSTER_NAME
    cluster.open-cluster-management.io/managedcluster-name: $CLUSTER_NAME
    ```

2. In order to complete the import process, create the managed cluster resource:
    
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

3. After the hosted cluster is created, it will be imported to the hub automatically, you can check it with:
  
    ```bash
     $ oc get managedcluster $CLUSTER_NAME
     ```

Your hosted cluster is now created and imported to MCE/ACM, which should also be visible from the MCE console.

## Access the hosted cluster

The access secrets are stored in the hypershift-management-cluster namespace.
The formats of the secrets name are:

- kubeconfig secret: `<hostingNamespace>-<name>-admin-kubeconfig` (e.g clusters-hypershift-demo-admin-kubeconfig)
- kubeadmin password secret: `<hostingNamespace>-<name>-kubeadmin-password` (e.g clusters-hypershift-demo-kubeadmin-password)


## Cleaning up the Hosted Control Plane Cluster

**NOTE:** When cleaning up your hosted cluster, you must delete both the hosted cluster and the managed cluster resource on MCE/ACM. Deleting only one can have negative side effects when trying to create the hosted cluster again if you're using the same managed cluster name.

### Destroying your Hosted Cluster

Delete the hosted cluster and its backend resources:

    ```bash
    $ oc hcp destroy cluster aws --name $CLUSTER_NAME --infra-id $INFRA_ID --aws-creds $AWS_CREDS --base-domain $BASE_DOMAIN --destroy-cloud-resources
    ```

### Destroying your Hosted Cluster's managed cluster

Delete the managed cluster resource on MCE:

    ```bash
    $ oc delete managedcluster $CLUSTER_NAME
    ```

### Destroying the Hypershift add-on and Hypershift operator

Delete the hypershift-addon

    ```bash
    $ oc delete managedclusteraddon -n local-cluster hypershift-addon
    ```

**NOTE:** Deleting the hypershift-addon will not destroy existing hosted clusters, nor the hypershift operator.

## Troubleshooting

### How do I manually install the local-cluster on MCE?
Apply the following YAML via `oc`:

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

### How do I manually install or delete the Hypershift Add-on?
Run the following `oc` command:

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
For deleting, do `oc delete` instead.

### How do I create the hypershift infrastructure and IAM pieces separately?
1. Set the additional variables to save each part:

    ```bash
    export INFRA_OUTPUT_FILE=$HOME/hostedcluster_infra.json
    export IAM_OUTPUT_FILE=$HOME/hostedcluster_iam.json
    ```

2. Create the infrastructure:

    ```bash
    $ oc hcp create infra aws --name $CLUSTER_NAME \
        --aws-creds $AWS_CREDS \
        --base-domain $BASE_DOMAIN \
        --infra-id $CLUSTER_NAME \
        --region $REGION \
        --output-file $INFRA_OUTPUT_FILE
    ```

3. In order to create the related IAM pieces, we'll need some info from the infra output.

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

Specically, we'll need the `publicZoneID`, `privateZoneID`, and `localZoneID`. 
You can pipe this data to the next step in order to save time.

4. Create the IAM:

    ```bash
    $ oc hcp create iam aws --infra-id $CLUSTER_NAME \
        --aws-creds $AWS_CREDS \
        --oidc-storage-provider-s3-bucket-name $BUCKET_NAME \
        --oidc-storage-provider-s3-region $BUCKET_REGION \
        --region $REGION \
        --public-zone-id Z00953301HRDK0M0YKQGE \
        --private-zone-id Z02815081BZYZVASX5HII \
        --local-zone-id Z01446842N9Z8MWQL967F \
        --output-file $IAM_OUTPUT_FILE
    ```

5. We can now use the `oc hcp create cluster aws` command to create our hosted cluster. We can specify the infrastructure and IAM pieces as arguments via `--infra-json` and `--iam-json`:

    ```bash
    $ oc hcp create cluster aws \
        --name $CLUSTER_NAME \
        --infra-id $INFRA_ID \
        --infra-json $INFRA_OUTPUT_FILE \
        --iam-json $IAM_OUTPUT_FILE \
        --aws-creds $AWS_CREDS \
        --pull-secret $PULL_SECRET \
        --region $REGION \
        --generate-ssh \
        --node-pool-replicas 3 \
        --namespace local-cluster
    ```

For more options when interacting with the CLI, refer to [Hypershift Project Documentation for creating a HostedCluster](https://hypershift-docs.netlify.app/getting-started/#create-a-hostedcluster).

That's all! Your hosted cluster is now created. Refer to the main section above for importing the cluster into MCE/ACM.

Check the status of your hosted cluster via:

    ```bash
    $ oc get hostedclusters -n local-cluster
    ```