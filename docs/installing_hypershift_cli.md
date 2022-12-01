# Getting started with Hosted Control Plane (HyperShift) CLI

## About the Hosted Control Plane CLI

The Hosted Control Plane command-line interface (CLI) is an `oc` command-line [plugin](https://docs.openshift.com/container-platform/4.11/cli_reference/openshift_cli/extending-cli-plugins.html), that allows you to create OpenShift hosted control plane clusters and manage them.

## Installing the Hosted Control Plane CLI

1. [Enable the Hypershift feature](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/provision_hosted_cluster_on_mce_local_cluster.md#configuring-the-hosting-service-cluster)

2. On the OCP console, click on the ? button at the top right hand corner and choose `Command line tools` menu to download the HyperShift CLI binary.

(unofficial: you can download one of these as well)
- [HyperShift CLI binary for Linux x86_64](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/hypershift-linux-amd64.tar.gz?raw=true)
- [HyperShift CLI binary for Mac x86_64](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/hypershift-darwin-amd64.tar.gz?raw=true)
- [HyperShift CLI binary for Windows x86_64](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/hypershift-windows-amd64.tar.gz?raw=true)

3. Unpack the archive.

```
    $ tar xvzf <file>
```

4. Rename the file to `oc-hcp` to make it an `oc` command-line [plugin](https://docs.openshift.com/container-platform/4.11/cli_reference/openshift_cli/extending-cli-plugins.html). `hcp` stands for `Hosted Control Plane`.

5. Follow the [oc plugin instructions](https://docs.openshift.com/container-platform/4.11/cli_reference/openshift_cli/extending-cli-plugins.html#cli-installing-plugins_cli-extend-plugins) to bind the `oc-hcp` plugin to a directory in your PATH.

After you install the Hosted Control Plane CLI, you can start using the `oc hcp ...` command. For more information on the CLI usage, see [this](https://hypershift-docs.netlify.app/getting-started/)

**Note: `oc hcp` is the command. For example, `oc hcp create cluster ...`
