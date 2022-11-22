# Getting started with Hosted Control Plane (HyperShift) CLI

## About the Hosted Control Plane CLI

The Hosted Control Plane command-line interface (CLI) is an `oc` command-line [plugin](https://docs.openshift.com/container-platform/4.11/cli_reference/openshift_cli/extending-cli-plugins.html), that allows you to create OpenShift hosted control plane clusters and manage them.

## Installing the Hosted Control Plane CLI

**NOTE: This document is unofficial will be updated later. For now download the CLI binary from this page

1. Download the HyperShift CLI binary.

- [HyperShift CLI binary for Linux x86_64](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/hypershift-linux-amd64.tar.gz?raw=true)
- [HyperShift CLI binary for Mac x86_64](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/hypershift-darwin-amd64.tar.gz?raw=true)
- [HyperShift CLI binary for Windows x86_64](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/hypershift-windows-amd64.tar.gz?raw=true)

2. Unpack the archive.

```
    $ tar xvzf <file>
```

3. Rename the file to `oc-hcp` to make it an `oc` command-line [plugin](https://docs.openshift.com/container-platform/4.11/cli_reference/openshift_cli/extending-cli-plugins.html). `hcp` stands for `Hosted Control Plane`.

4. Follow the [oc plugin instructions](https://docs.openshift.com/container-platform/4.11/cli_reference/openshift_cli/extending-cli-plugins.html#cli-installing-plugins_cli-extend-plugins) to bind the `oc-hcp` plugin to a directory in your PATH.

After you install the Hosted Control Plane CLI, you can start using the `oc hcp ...` command. For more information on the CLI usage, see [this](https://hypershift-docs.netlify.app/getting-started/)

**Note: `oc hcp` is the command. For example, `oc hcp create cluster ...`
