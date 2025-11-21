# HyperShift Addon Operator Documentation

Welcome to the HyperShift Addon Operator documentation. This guide will help you understand, configure, deploy, and manage HyperShift hosted control planes with Red Hat Advanced Cluster Management (ACM) and Multi-Cluster Engine (MCE).

## Table of Contents

### 🚀 [Getting Started](./getting-started/)
Essential guides to get you up and running with HyperShift.

- **[Introduction](./getting-started/intro.md)** - Overview of HyperShift and hosted control planes
- **[Installing HyperShift CLI](./getting-started/installing_hypershift_cli.md)** - Get started with the `hcp` command-line tool

### 🏗️ [Planning](./planning/)
Guides for planning and provisioning HyperShift hosted clusters.

- **[Recommended Patterns](./planning/recommended_pattern.md)** - Best practices for HyperShift deployments
- **[Hosting Cluster Topologies](./planning/hosting_cluster_topologies.md)** - Understanding different deployment topologies
- **[Provision Hosted Cluster on MCE Local Cluster](./planning/provision_hosted_cluster_on_mce_local_cluster.md)** - Complete guide to creating your first hosted cluster

### ⚙️ [Configuration](./configuration/)
Configuration guides for setting up HyperShift components and cloud providers.

- **[HyperShift Operator Configuration](./configuration/hypershift_operator_configuration.md)** - Configure the HyperShift operator
- **[Disable HyperShift Operator Management](./configuration/disable_hypershift_operator_management.md)** - Prevent automatic operator lifecycle management
- **[AWS Cloud Provider Secret](./configuration/cloud_provider_secret_aws.md)** - Set up AWS credentials for hosted clusters
- **[Creating AWS STS Roles](./configuration/creating_role_sts_aws.md)** - Configure AWS Security Token Service roles
- **[ACM to MCE Migration](./configuration/acm_to_mce.md)** - Migrate from ACM to MCE setup

### 📊 [Management](./management/)
Tools and guides for managing HyperShift hosted clusters after deployment.

- **[Discovering Hosted Clusters](./management/discovering_hostedclusters.md)** - Auto-discovery and import of hosted clusters
- **[HyperShift Addon Status](./management/hypershift_addon_status.md)** - Monitor addon health and status
- **[Cluster Capacity Metrics](./management/cluster_capacity_metrics_hcp.md)** - Understanding capacity planning and metrics

### 🔧 [Optional Topics](./optional/)
Optional configuration and operational topics.

- **[AKS Configuration](./optional/aks_configuration.md)** - Azure Kubernetes Service integration
- **[Backup and Restore](./optional/backup_restore.md)** - Backup and disaster recovery procedures
- **[RHOBS Integration](./optional/enable_hypershift_operator_RHOBS.md)** - Red Hat OpenShift Monitoring integration
- **[Prometheus Metrics](./optional/prometheus_metrics.md)** - Custom metrics and monitoring
- **[Provision via ManifestWork](./optional/provision_hypershift_clusters_by_manifestwork.md)** - Advanced provisioning methods
- **[Hosted Mode for MCE/ACM Addons](./optional/running_mce_acm_addons_hostedmode.md)** - Running addons in hosted mode
- **[Hosted Cluster Scheduling](./optional/scheduling_hosted_cluster.md)** - Advanced scheduling strategies
- **[Upgrading HyperShift Operator](./optional/upgrading_hypershift_operator.md)** - Upgrade procedures

### 🔍 [Troubleshooting](./troubleshooting/)
Diagnose and resolve common issues.

- **[Troubleshooting Guide](./troubleshooting/troubleshooting.md)** - Common issues and solutions
- **[Architecture Diagram](./troubleshooting/troubleshooting_diagram.jpg)** - Visual guide to HyperShift components

### 📚 [Reference](./reference/)
Reference materials and version compatibility information.

- **[Supported Versions](./reference/supported_versions.md)** - OCP version compatibility matrix
- **[Resource Requirements](./reference/hcp_resource_minimum_reqs.md)** - Minimum resource requirements
- **[MCE 2.2.z Workaround](./reference/mce2.2.z_workaround.md)** - Version-specific workarounds

### 🖼️ [Images](./images/)
Screenshots and diagrams used throughout the documentation.

## Quick Start

1. **Start Here**: Read the [Introduction](./getting-started/intro.md) to understand HyperShift concepts
2. **Install Tools**: Follow the [HyperShift CLI installation guide](./getting-started/installing_hypershift_cli.md)
3. **Plan**: Review [hosting topologies](./planning/hosting_cluster_topologies.md) and [provisioning guide](./planning/provision_hosted_cluster_on_mce_local_cluster.md)
4. **Configure**: Set up [cloud provider credentials](./configuration/cloud_provider_secret_aws.md) and [operator configuration](./configuration/hypershift_operator_configuration.md)
5. **Manage**: Learn about [cluster discovery](./management/discovering_hostedclusters.md) and [monitoring](./management/cluster_capacity_metrics_hcp.md)

## Support and Community

- **Issues**: Report issues on [GitHub](https://github.com/stolostron/hypershift-addon-operator/issues)
- **Documentation**: This documentation is maintained alongside the project
- **Upstream**: HyperShift upstream documentation is available at [hypershift-docs.netlify.app](https://hypershift-docs.netlify.app/)

## Contributing

Contributions to the documentation are welcome! Please submit pull requests with your improvements and updates.
