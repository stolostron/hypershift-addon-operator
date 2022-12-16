# Getting started with Hypershift Hosted Control Plane CLI

## About the Hypershift Hosted Control Plane CLI

The Hypershift Hosted Control Plane command-line interface (CLI) is an `oc` command-line [plugin](https://docs.openshift.com/container-platform/4.11/cli_reference/openshift_cli/extending-cli-plugins.html), that allows you to create OpenShift hosted control plane clusters and manage them.

## Installing the Hypershift Hosted Control Plane CLI

1. [Enable the Hypershift hosted control plane feature](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/provision_hosted_cluster_on_mce_local_cluster.md#configuring-the-hosting-service-cluster)

2. On the OCP console, click on the ? button at the top right hand corner and choose `Command line tools` menu to download the HyperShift CLI binary.

3. Unpack the archive.

```
    $ tar xvzf hypershift.tar.gz
```

4. Copy the `hypershift` CLI binary file to a directory in your PATH.

```
    $ chmod +x hypershift
    $ sudo mv hypershift /usr/local/bin/.
```

After you install the Hypershift Hosted Control Plane CLI, you can start using the `hypershift create cluster` command to create and manage hosted clusters. For more information on the CLI usage, see [this](https://hypershift-docs.netlify.app/getting-started/#create-a-hostedcluster/)


```
hypershift create cluster aws --name $CLUSTER_NAME --namespace $NAMESPACE --node-pool-replicas=3 --secret-creds $SECRET_CREDS --region $REGION
```

For all available parameters and their descriptions, run

```
hypershift create cluster aws --help
```