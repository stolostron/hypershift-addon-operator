# Getting started with HyperShift CLI

## About the HyperShift CLI

With the HyperShift command-line interface (CLI) as a `oc` command-line [plugin](https://docs.openshift.com/container-platform/4.11/cli_reference/openshift_cli/extending-cli-plugins.html), you can create OpenShift hosted control planes and manage them.

## Installing the HyperShift CLI

**NOTE: This document is unofficial will be updated later. for now download the CLI binary from this page

1. Download the HyperShift CLI binary.

- [HyperShift CLI binary for Linux x86_64](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/hypershift-linux-amd64.tar.gz?raw=true)
- [HyperShift CLI binary for Mac x86_64](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/hypershift-darwin-amd64.tar.gz?raw=true)
- [HyperShift CLI binary for Windows x86_64](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/hypershift-windows-amd64.tar.gz?raw=true)

2. Unpack the archive.

```
    $ tar xvzf <file>
```

3. Rename the file to `oc-hcp` to make it as a `oc` command-line [plugin](https://docs.openshift.com/container-platform/4.11/cli_reference/openshift_cli/extending-cli-plugins.html). `hcp` stands for `Hosted Control Plane`.

4. Move the `oc-hcp` binary to a directory on your PATH.

After you install the HyperShift CLI, you can start using the `oc-hcp` command. For more information on the CLI usage, see [this](https://hypershift-docs.netlify.app/getting-started/)

**Note: `oc hcp` is the command. For example, `oc hcp create cluster ...`