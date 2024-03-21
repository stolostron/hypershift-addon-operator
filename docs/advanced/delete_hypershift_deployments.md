# Delete hypershift deployments 

The hypershift deployment controller is deprecated and is no longer available from MCE 2.2/ACM 2.7 onwards. The hosted clustered that were deployed using hypershift deployments are not impacted. However, when a hosted cluster is no longer needed, the deletion of the hosted cluster, hypershift deployments and the associated resources require some additional steps, as described below. 

## Clean up hosted clusters that were provisioned using hypershift deployments

Clean up of your hosted cluster involves deleting the managed cluster, hosted cluster, and hypershift deployment resources, and destroying the associated cloud infrastructure resources.

1. See [Destroying your hosted cluster](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/provision_hosted_cluster_on_mce_local_cluster.md#destroying-your-hosted-cluster) on how to delete the managed cluster, hosted cluster and the cloud resources.

2. Delete the AWS IAM resources

    ```bash
    $ hypershift destroy iam aws --aws-creds ${AWS_CREDS_FILE} --infra-id $INFRA_ID
    ```

3. Remove the finalizer from the hypershift deployment resource

    ```bash
    $ oc edit hd $HD_NAME -n $NAMESPACE 
    ```

    ```yaml
    apiVersion: cluster.open-cluster-management.io/v1alpha1
    kind: HypershiftDeployment
    metadata:
      annotations:
        cluster.open-cluster-management.io/createmanagedcluster: "false"
      finalizers:
      - hypershiftdeployment.cluster.open-cluster-management.io/finalizer
      - hypershiftdeployment.cluster.open-cluster-management.io/managedcluster-cleanup
    ```

4. Delete the hypershift deployment resource

    ```bash
    $ oc delete hd $HD_NAME -n $NAMESPACE 
    ```