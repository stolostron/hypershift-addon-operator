# Hypershift Operator Configuration Options

If you run `hypershift install --help` [hypershift CLI](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/installing_hypershift_cli.md) command, you can see the hypershift operator installation flags. For example,

```
Installs the HyperShift operator

Usage:
  hypershift install [flags]
  hypershift install [command]

Available Commands:
  render      Render HyperShift Operator manifests to stdout

Flags:
      --additional-trust-bundle string                 Path to a file with user CA bundle
      --aws-private-creds string                       Path to an AWS credentials file with privileges sufficient to manage private cluster resources
      --aws-private-region string                      AWS region where private clusters are supported by this operator
      --aws-private-secret string                      Name of an existing secret containing the AWS private link credentials.
      --aws-private-secret-key string                  Name of the secret key containing the AWS private link credentials. (default "credentials")
      --development                                    Enable tweaks to facilitate local development
      --enable-admin-rbac-generation                   Generate RBAC manifests for hosted cluster admins
      --enable-ci-debug-output                         If extra CI debug output should be enabled
      --enable-conversion-webhook                      Enable webhook for converting hypershift API types (default true)
      --enable-uwm-telemetry-remote-write              If true, HyperShift operator ensures user workload monitoring is enabled and that it is configured to remote write telemetry metrics from control planes
      --enable-validating-webhook                      Enable webhook for validating hypershift API types
      --exclude-etcd                                   Leave out etcd manifests
      --external-dns-credentials string                Credentials to use for managing DNS records using external-dns
      --external-dns-domain-filter string              Restrict external-dns to changes within the specifed domain.
      --external-dns-provider string                   Provider to use for managing DNS records using external-dns
      --external-dns-secret string                     Name of an existing secret containing the external-dns credentials.
      --external-dns-txt-owner-id string               external-dns TXT registry owner ID.
  -h, --help                                           help for install
      --hypershift-image string                        The HyperShift image to deploy (default "quay.io/hypershift/hypershift-operator:latest")
      --image-refs string                              Image references to user in Hypershift installation
      --metrics-set metricsSet                         The set of metrics to produce for each HyperShift control plane. Valid values are: Telemetry, SRE, All (default Telemetry)
      --namespace string                               The namespace in which to install HyperShift (default "hypershift")
      --oidc-storage-provider-s3-bucket-name string    Name of the bucket in which to store the clusters OIDC discovery information. Required for AWS guest clusters
      --oidc-storage-provider-s3-credentials string    Credentials to use for writing the OIDC documents into the S3 bucket. Required for AWS guest clusters
      --oidc-storage-provider-s3-region string         Region of the OIDC bucket. Required for AWS guest clusters
      --oidc-storage-provider-s3-secret string         Name of an existing secret containing the OIDC S3 credentials.
      --oidc-storage-provider-s3-secret-key string     Name of the secret key containing the OIDC S3 credentials. (default "credentials")
      --platform-monitoring PlatformMonitoringOption   Select an option for enabling platform cluster monitoring. Valid values are: None, OperatorOnly, All
      --private-platform string                        Platform on which private clusters are supported by this operator (supports "AWS" or "None") (default "None")
      --rhobs-monitoring                               If true, HyperShift will generate and use the RHOBS version of monitoring resources (ServiceMonitors, PodMonitors, etc)
      --wait-until-available                           If true, pauses installation until hypershift operator has been rolled out and its webhook service is available (if installing the webhook)
```

You optionally create some secrets to [configure a hosting cluster](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/provision_hosted_cluster_on_mce_local_cluster.md#configuring-the-hosting-cluster). The hypershift addon agent uses these secrets to build some of these installation flags.

## OIDC S3 credentials secret

If you create the OIDC S3 credentials secret for the HyperShift operator named `hypershift-operator-oidc-provider-s3-credentials` in `local-cluster` namespace, the hypershift addon agent uses this secret to build the following hypershift operator installation flags.

```
    --oidc-storage-provider-s3-bucket-name string    Name of the bucket in which to store the clusters OIDC discovery information. Required for AWS guest clusters
    --oidc-storage-provider-s3-credentials string    Credentials to use for writing the OIDC documents into the S3 bucket. Required for AWS guest clusters
    --oidc-storage-provider-s3-region string         Region of the OIDC bucket. Required for AWS guest clusters
```

## AWS private link secret

If you create the AWS private link secret named `hypershift-operator-private-link-credentials` in `local-cluster` namespace, the hypershift addon agent uses this secret to build the following hypershift operator installation flags.

```
    --aws-private-creds string                       Path to an AWS credentials file with privileges sufficient to manage private cluster resources
    --aws-private-region string                      AWS region where private clusters are supported by this operator
    --aws-private-secret string                      Name of an existing secret containing the AWS private link credentials.
```

## External DNS secret

If you create the external DNS secret named `hypershift-operator-external-dns-credentials` in `local-cluster` namespace, the hypershift addon agent uses this secret to build the following hypershift operator installation flags.

```
    --external-dns-domain-filter string              Restrict external-dns to changes within the specifed domain.
    --external-dns-provider string                   Provider to use for managing DNS records using external-dns
    --external-dns-secret string                     Name of an existing secret containing the external-dns credentials.
    --external-dns-txt-owner-id string               external-dns TXT registry owner ID.
```

## Other default installation flags

These are other installation flags that the hypershift addon agent sets by default when it installs or upgrades the hypershift operator.

```
--image-refs                            <the image references from MCE installation>
--platform-monitoring                   OperatorOnly
--enable-uwm-telemetry-remote-write
```

## Customizing the hypershift operator installation flags

The installation flags for the `oidc-storage-provider-s3`, `aws-private` and `external-dns` can be updated by updating the corresponding secrets.

You cannot update the `--image-refs`.

If you want to add or remove other installation flags, create a config map named `hypershift-operator-install-flags` in `local-cluster` namespace.

```yaml
kind: ConfigMap
apiVersion: v1
metadata:
  name: hypershift-operator-install-flags
  namespace: local-cluster
data:
  installFlagsToAdd: "--metrics-set SRE --exclude-etcd"
  installFlagsToRemove: "--enable-uwm-telemetry-remote-write"
```

The installation flags and their values specified in `data.installFlagsToAdd` are used when the hypershift addon agent installs the hypershift operator. All flag keys and values are added as a single string delimited by a space.

The installation flags specified in `data.installFlagsToRemove` are removed when the hypershift addon agent installs the hypershift operator. So if you want to remove the default `--enable-uwm-telemetry-remote-write` flag, add it in `data.installFlagsToRemove`. All flag keys are added as a single string delimited by a space and you do not need to add the flag values.

**Note:** The hypershift addon agent checks for any data change `hypershift-operator-install-flags` configmap in `local-cluster` namespace every 2 minutes and re-installs the hypershift operator if there is a change.
