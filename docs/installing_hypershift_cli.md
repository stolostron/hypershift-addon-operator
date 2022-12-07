# Getting started with Hypershift Hosted Control Plane CLI

## About the Hypershift Hosted Control Plane CLI

The Hypershift Hosted Control Plane command-line interface (CLI) is an `oc` command-line [plugin](https://docs.openshift.com/container-platform/4.11/cli_reference/openshift_cli/extending-cli-plugins.html), that allows you to create OpenShift hosted control plane clusters and manage them.

## Installing the Hosted Control Plane CLI

1. [Enable the Hypershift hosted control plane feature](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/provision_hosted_cluster_on_mce_local_cluster.md#configuring-the-hosting-service-cluster)

2. On the OCP console, click on the ? button at the top right hand corner and choose `Command line tools` menu to download the HyperShift CLI binary.

3. Unpack the archive.

```
    $ tar xvzf hypershift.tar.gz
```

4. Rename `hypershift` to `oc-hcp` to make it an `oc` command-line [plugin](https://docs.openshift.com/container-platform/4.11/cli_reference/openshift_cli/extending-cli-plugins.html). `hcp` stands for `Hosted Control Plane`.

```
    $ sudo mv hypershift oc-hcp
```

5. Follow the [oc plugin instructions](https://docs.openshift.com/container-platform/4.11/cli_reference/openshift_cli/extending-cli-plugins.html#cli-installing-plugins_cli-extend-plugins) to bind the `oc-hcp` plugin to a directory in your PATH.

```
    $ chmod +x oc-hcp
    $ sudo mv oc-hcp /usr/local/bin/.
```

After you install the Hypershift Hosted Control Plane CLI, you can start using the `oc hcp ...` command. For more information on the CLI usage, see [this](https://hypershift-docs.netlify.app/getting-started/)

**Note: `oc hcp` is the command. For example, `oc hcp create cluster ...`
