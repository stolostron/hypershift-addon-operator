# Getting started with Hypershift Hosted Control Plane CLI

## About the Hypershift Hosted Control Plane CLI

The Hypershift Hosted Control Plane command-line interface (hcp CLI) allows you to create and manage OpenShift hosted clusters.

## Installing the hcp CLI

1. [Enable the Hypershift hosted control plane feature](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/provision_hosted_cluster_on_mce_local_cluster.md#configuring-the-hosting-service-cluster)

2. On the OCP console, click on the ? button at the top right hand corner and choose `Command line tools` menu to download the hcp CLI binary.

3. Unpack the archive.

```
    $ tar xvzf hcp.tar.gz
```

4. Copy the `hcp` CLI binary file to a directory in your PATH.

```
    $ chmod +x hcp
    $ sudo mv hcp /usr/local/bin/.
```

After you install the hcp CLI, you can start using the `hcp create cluster` command to create and manage hosted clusters. For more information on the CLI usage, see [this](https://hypershift-docs.netlify.app/getting-started/#create-a-hostedcluster/)


```
hcp create cluster aws --name $CLUSTER_NAME --namespace $NAMESPACE --node-pool-replicas=3 --secret-creds $SECRET_CREDS --region $REGION
```

For all available parameters and their descriptions, run

```
hcp create cluster aws --help
```