# Provisioning hosted clusters on MCE (Without Hypershift Deployment)

As per the [Hypershift Docs](https://hypershift-docs.netlify.app/), configuring hosted control planes requires a hosting cluster and a hosted cluster. By deploying the HyperShift operator on an existing managed cluster via the `hypershift-addon` managed cluster addon, you can turn that cluster into a hosting cluster and start the creation of the hosted cluster. MCE 2.2 only supports the default `local-cluster` managed cluster, the hub cluster, to be the hosting cluster. 

Hosted control planes is a Technology Preview feature, so the related components are disabled by default. In Multicluster engine operator (MCE) 2.1, this was done using HypershiftDeployment. As of MCE 2.2, HypershiftDeployment is now obsolete and this guide will show how we can enable the feature followed by deploying a hosted cluster on Amazon Web Services via MCE using the Hypershift command line.

## Configuring the hosting cluster

You can deploy hosted control planes by configuring an existing cluster to function as a hosting cluster. The hosting cluster is the OCP cluster where the control planes are hosted, and can be the hub cluster or one of the OCP managed clusters. In this section, we will use hypershift-addon to install a HyperShift operator onto the hub cluster, otherwise known as the local-cluster in MCE/ACM.

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

### Prerequisites for creating hosted clusters on AWS cloud platform

If you are planning to create hosted clusters on AWS cloud platform, you must have the following prerequisites before configuring the hosting clsuter

* An [AWS credentials file](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-files.html) with permissions to create infrastructure for the cluster.
* A Route53 public zone for cluster DNS records. To create a public zone: 

    ```bash
    $ BASE_DOMAIN=www.example.com
    $ aws route53 create-hosted-zone --name $BASE_DOMAIN --caller-reference $(whoami)-$(date --rfc-3339=date)
    ```
    **Important:** To access applications in your guest clusters, the public zone must be routable. If the public zone exists, skip this step. Otherwise, the public zone will affect the existing functions.

* An S3 bucket with public access to host OIDC discovery documents for your clusters.

  To create the bucket (in us-east-1):

  ```bash
    $ BUCKET_NAME=your-bucket-name
    $ aws s3api create-bucket --bucket $BUCKET_NAME
  ```

  To create the bucket in a region other than us-east-1:

  ```bash
  $ BUCKET_NAME=your-bucket-name
  $ REGION=us-east-2
  $ aws s3api create-bucket --acl public-read --bucket $BUCKET_NAME \
    --create-bucket-configuration LocationConstraint=$REGION \
    --region $REGION
  ```

  To set ACL on the bucket

  ```bash
    $ export BUCKET_NAME=your-bucket-name
    $ aws s3api delete-public-access-block --bucket $BUCKET_NAME
    $ echo '{
        "Version": "2012-10-17",
        "Statement": [
          {
            "Effect": "Allow",
            "Principal": "*",
            "Action": "s3:GetObject",
            "Resource": "arn:aws:s3:::${BUCKET_NAME}/*"
          }
        ]
      }' | envsubst > policy.json
    $ aws s3api put-bucket-policy --bucket $BUCKET_NAME --policy file://policy.json
  ```

* OIDC S3 credentials secret for the HyperShift operator named `hypershift-operator-oidc-provider-s3-credentials` in `local-cluster` namespace. When the hypershift feature is enabled, the hypershift-addon agent uses this secret for installing the hypershift operator. If this secret is created after enabling the hosted control plane feature, the hypershift-addon agent automatically re-installs the hypershift operator with this OIDC S3 option.

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

* Optinally if you plan to use service-level DNS (external DNS) for Control Plane Service, create an external DNS credential secret named `hypershift-operator-external-dns-credentials` in `local-cluster` namespace. When the hosted control plane feature is enabled, the hypershift-addon agent uses this secret for installing the hypershift operator. If this secret is created after enabling the hosted control plane feature, the hypershift-addon agent automatically re-installs the hypershift operator with this external DNS option.

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

* Optionally if you plan to provision hosted clusters on the AWS platform with Private Link, create an AWS credential secret for the HyperShift operator named `hypershift-operator-private-link-credentials` in `local-cluster` namespace. If this secret is created after enabling the hosted control plane feature, the hypershift-addon agent automatically re-installs the hypershift operator with this private link option.

  The secret must contain 3 fields:

  - `aws-access-key-id`: AWS credential access key id
  - `aws-secret-access-key`: AWS credential access key secret
  - `region`: Region for use with Private Link

  For details, please check: [HyperShift Project Documentation](https://hypershift-docs.netlify.app/how-to/aws/deploy-aws-private-clusters/). For convenience, you can create this secret using the CLI by:

  ```bash
  $ oc create secret generic hypershift-operator-private-link-credentials --from-literal=aws-access-key-id=<aws-access-key-id> --from-literal=aws-secret-access-key=<aws-secret-access-key> --from-literal=region=<region> -n <managed-cluster-used-as-hosting-service-cluster>
  ```

   Add the special label to the `hypershift-operator-private-link-credentials` secret so that the secret is backed up for disaster recovery.

  ```bash
  $ oc label secret hypershift-operator-private-link-credentials -n local-cluster cluster.open-cluster-management.io/backup=true
  ```

### Disconnected Environment Configuration

The `hypershift-addon` managed cluster addon enables the `--enable-uwm-telemetry-remote-write` option in the hypershift operator. This ensures user workload monitoring is enabled and that it is configured to remote write telemetry metrics from control planes. If you installed the multicluster engine operator on Red Hat OpenShift Container Platform clusters that are not connected to the Internet, the user workload monitoring feature of the hypershift operator will fail with the following error.

```
$ oc get events -n hypershift
LAST SEEN   TYPE      REASON           OBJECT                MESSAGE
4m46s       Warning   ReconcileError   deployment/operator   Failed to ensure UWM telemetry remote write: cannot get telemeter client secret: Secret "telemeter-client" not found
```

You need to disable the user workload monitoring option by creating the following configmap in `local-cluster` namespace before enabling the `hypershift-addon` managed cluster addon. You can also create the configmap after enbling the addon. The addon agent will re-configure the hypershift operator.

```yaml
kind: ConfigMap
apiVersion: v1
metadata:
  name: hypershift-operator-install-flags
  namespace: local-cluster
data:
  installFlagsToAdd: ""
  installFlagsToRemove: "--enable-uwm-telemetry-remote-write"
```

For more information on the usage of this configmap, see [Hypershift Operator configuration Options](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/hypershift_operator_configuration.md).

### Verifying the Hosted Control Plane feature is healthy

By default, the Hosted Control Plane feature is enabled starting in MCE 2.4.

If the feature is disabled, run the following command to ensure that the hosted control planes feature is enabled, replacing `multiclusterengine` with your MCE's instance name:

  ```bash
  oc patch mce multiclusterengine --type=merge -p '{"spec":{"overrides":{"components":[{"name":"hypershift","enabled": true}]}}}'
  ```

By enabling this feature, the `hypershift-addon` managed cluster addon is installed on the `local-cluster` managed cluster, and then the addon agent installs the hypershift operator on the MCE hub cluster.

Confirm that the `hypershift-addon` is installed by running the following command:
  
    ```bash
    oc get managedclusteraddons -n local-cluster hypershift-addon
    
    NAME               AVAILABLE   DEGRADED   PROGRESSING
    hypershift-addon   True        False
    ```

You run the following wait commands to wait for the addon to reach this state with a timeout:

    ```bash
    oc wait --for=condition=Degraded=True managedclusteraddons/hypershift-addon -n local-cluster --timeout=5m
    oc wait --for=condition=Available=True managedclusteraddons/hypershift-addon -n local-cluster --timeout=5m
    ```
Once complete, the `hypershift-addon` and the hypershift operator are installed and `local-cluster` is available to host and manage hosted clusters.

By default, no node placement preference is specified for the `hypershift-addon` managed cluster addons. It may be desirable to have the addons run on the infrastructure nodes. The benefits to isolate infrastructure workloads include the prevention of incurring billing costs against subscription counts, and to separate maintenance and management. See [Troubleshooting](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/provision_hosted_cluster_on_mce_local_cluster.md#troubleshooting) for details on how to configure the `hypershift-addon` managed cluster addon to run on the infrastructure nodes.

See [Hypershift addon status](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/hypershift_addon_status.md) for more details on checking the status of `hypershift-addon managed` cluster addon.

## Provision a hosted cluster on AWS

After setting up the hypershift command line and enabling `local-cluster` cluster as the hosting cluster, you can provision a hosted cluster via `hypershift` command line.

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

    For description of each variable, run:

    ```bash
    hypershift create cluster aws --help
    ```

2. Ensure you are logged into your hub (`local-cluster`) cluster.

3. If you want to create the hypershift infrastructure and IAM pieces separately, refer to the [How do I create the hypershift infrastructure and IAM pieces separately?](#how-do-i-create-the-hypershift-infrastructure-and-iam-pieces-separately) section at the end. Otherwise run the following command to create the hosted cluster:

    ```bash
    $ hypershift create cluster aws \
        --name $CLUSTER_NAME \
        --infra-id $INFRA_ID \
        --aws-creds $AWS_CREDS \
        --pull-secret $PULL_SECRET \
        --region $REGION \
        --generate-ssh \
        --node-pool-replicas 3 \
    ```

    By default, all `HostedCluster` and `NodePool` custom resources are created in `clusters` namespace. If you specify `--namespace ANOTHER_NAMESPACE` parameter, `HostedCluster` and `NodePool` custom resources are created in the specified namespace.

For more options when interacting with the CLI, refer to [Hypershift Project Documentation for creating a HostedCluster](https://hypershift-docs.netlify.app/getting-started/#create-a-hostedcluster).

That's all! Your hosted cluster is now created.

Check the status of your hosted cluster via:

    ```bash
    oc get hostedclusters -n local-cluster
    ```

## Importing the Hosted cluster into MCE

Hosted clusters are automatically imported into MCE once the control plane becomes available. All ACM addons are also enabled if ACM is installed.

### Disabling automatic import

1. On the hub cluster, edit the `AddonDeploymentConfig` resource  `hypershift-addon-deploy-config` in the `hypershift` namespace.

    ```bash
    oc edit addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine
    ```

2. Under `spec.customizedVariables`, add the `autoImportDisabled` variable with value `"true"`. Leave the other customized variables intact and save.

    ```yaml
    apiVersion: addon.open-cluster-management.io/v1alpha1
    kind: AddOnDeploymentConfig
    metadata:
      name: hypershift-addon-deploy-config
      namespace: multicluster-engine
    spec:
      customizedVariables:
      - name: hcMaxNumber
        value: "80"
      - name: hcThresholdNumber
        value: "60"
      - name: autoImportDisabled
        value: "true"
    ```
  
Alternatively, if the value does not exist yet in the `AddonDeploymentConfig` resource, you can patch it with this command:

    ```bash
    oc patch addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine --type=json -p='[{"op": "add", "path": "/spec/customizedVariables/-","value":{"name":"autoImportDisabled","value":"true"}}]'
    ```

**NOTE:** Only newly created Hosted clusters will not be automatically imported. Hosted clusters that have already been imported will not be affected. Hosted clusters can still be manually imported via the UI or CLI.

To re-enable auto-import, simply remove the `autoImportDisabled` variable in the resource `AddonDeploymentConfig` resource.

### Importing the Hosted cluster into MCE via UI

After creating the hosted cluster, import the hosted cluster into MCE so that MCE can manage the life-cycle of the hosted cluster.

You can import do this from the MCE Console UI:
  
  1. Navigate to Infrastructure > Clusters
  2. Click the $CLUSTER_NAME in the cluster list
  3. Click `Import hosted cluster` link

### Importing the Hosted Cluster into MCE via CLI

You can also import the hosted cluster into MCE using custom resources in command line.

1. Create the managed cluster resource.

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

2. Create the `KlusterletAddonConfig` resource to enable all ACM addons. This is applicable only in ACM hub. If you have installed MCE only, this is not applicable.

    ```bash
    $ cat <<EOF | oc apply -f -
    apiVersion: agent.open-cluster-management.io/v1
    kind: KlusterletAddonConfig
    metadata:
      name: $CLUSTER_NAME
      namespace: $CLUSTER_NAME
    spec:
      clusterName: $CLUSTER_NAME
      clusterNamespace: $CLUSTER_NAME
      clusterLabels:
        cloud: auto-detect
        vendor: auto-detect
      applicationManager:
        enabled: true
      certPolicyController:
        enabled: true
      iamPolicyController:
        enabled: true
      policyController:
        enabled: true
      searchCollector:
        enabled: false
    EOF
    ```

3. After the hosted cluster is created, it will be imported into MCE. You can check the status by running
  
    ```bash
     oc get managedcluster $CLUSTER_NAME
     ```

    ```bash
    NAME                               HUB ACCEPTED   MANAGED CLUSTER URLS                                                  JOINED   AVAILABLE   AGE
    $CLUSTER_NAME                      true           https://api.app-aws-411ga-hub-bhbj8.dev06.red-chesterfield.com:6443   True     True        25h
    ```

    ```bash
     oc get managedclusteraddon -n $CLUSTER_NAME
     ```

    ```bash
    NAME                          AVAILABLE   DEGRADED   PROGRESSING
    application-manager           True                   
    cert-policy-controller        True                   
    cluster-proxy                 True                   
    config-policy-controller      True                   
    governance-policy-framework   True                   
    iam-policy-controller         True                   
    work-manager                  True                   
    ```

Your hosted cluster is now created and imported to MCE/ACM, which should also be visible from the MCE console.

## Access the hosted cluster

The access secrets are stored in the hypershift-management-cluster namespace.
The formats of the secrets name are:

- kubeconfig secret: `<hostingNamespace>-<name>-admin-kubeconfig` (e.g clusters-hypershift-demo-admin-kubeconfig)
- kubeadmin password secret: `<hostingNamespace>-<name>-kubeadmin-password` (e.g clusters-hypershift-demo-kubeadmin-password)

You can also use the following command to generate a kubeconfig file for a specific hosted cluster.

  ```bash
  hypershift create kubeconfig --name $CLUSTER_NAME
  ```

  Use `--namespace` parameter if you specified it when creating the hosted cluster.

### Destroying your Hosted Cluster

**NOTE:** When cleaning up your hosted cluster, you must delete both the hosted cluster and the managed cluster resource on MCE/ACM. Deleting only one can have negative side effects when trying to create the hosted cluster again if you're using the same managed cluster name.

1. Delete the managed cluster resource on MCE:

    ```bash
    oc delete managedcluster $CLUSTER_NAME
    ```

2. Delete the hosted cluster and its cloud resources

    ```bash
    hypershift destroy cluster aws --name $CLUSTER_NAME --infra-id $INFRA_ID --aws-creds $AWS_CREDS --base-domain $BASE_DOMAIN --destroy-cloud-resources
    ```

### Disabling the hypershift-addon and uninstalling the hypershift operator

If you want to uninstall the hypershift operator and disable the hypershift-addon managed cluster addon from `local-cluster`, ensure that there is no hosted cluster first by running

  ```bash
  oc get hostedcluster -A
  ```

If there is a hosted cluster, the hypershift operator will not be uninstalled even if the hypershift-addon managed cluster addon is disabled.

Disable the hypershift-addon managed cluster addon.

  ```bash
  oc patch mce multiclusterengine --type=merge -p '{"spec":{"overrides":{"components":[{"name":"hypershift-local-hosting","enabled": false}]}}}'
  ```

### Disabling the hosted control plane feature from MCE

Before disabling the hosted control plane feature from MCE, ensure that the hypershift-addon is disabled first.

  ```bash
  oc patch mce multiclusterengine --type=merge -p '{"spec":{"overrides":{"components":[{"name":"hypershift","enabled": false}]}}}'
  ```
