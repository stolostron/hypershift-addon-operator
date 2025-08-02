# Recommended Hosted Control Plane Fleet Management Pattern

## Overview

As your hosted control plane deployment scales, you'll need a strategic approach to manage growing resource requirements and maintain centralized visibility across your entire fleet. This document outlines the recommended architecture pattern for managing hosted control planes at scale.

## The Challenge

ACM (Red Hat Advanced Cluster Management) or MCE (Red Hat OpenShift Multi-Cluster Engine) enables hosted control plane capabilities, typically starting with a single hub cluster. However, as the number of hosted control planes increases:

- Resource requirements grow proportionally
- You must either scale existing clusters or provision new ones
- Managing multiple hosting clusters becomes complex
- Maintaining centralized visibility and control becomes challenging

## Recommended Architecture Pattern

The recommended approach is to implement a **hierarchical management pattern**:

### Central ACM Hub Cluster
- Serves as the primary management and visibility layer
- Provides a single console for end-to-end fleet management
- Manages operational policies across the entire infrastructure
- Offers comprehensive monitoring and governance capabilities

### MCE Clusters as Hosting Infrastructure
- Deploy multiple MCE clusters to host the actual control planes
- MCE provides hosted control plane components with a smaller footprint than full ACM
- Includes management console and BareMetal infrastructure operators
- Optimized for hosting workloads rather than comprehensive management

### Benefits of This Pattern

1. **Centralized Management**: Single pane of glass for your entire fleet
2. **Scalable Architecture**: Add MCE clusters as needed for capacity
3. **Resource Optimization**: MCE clusters focus on hosting, ACM focuses on management
4. **Operational Efficiency**: Unified policy management and governance
5. **Cost Effectiveness**: Right-size each component for its specific role

## Implementation Considerations

- The central ACM hub manages both MCE clusters and their hosted control planes
- This creates a unified view across all cluster types in your fleet
- Policies and governance can be applied consistently across the entire infrastructure
- Monitoring and alerting can be centralized while maintaining distributed hosting capacity

This pattern enables you to scale your hosted control plane infrastructure efficiently while maintaining the operational benefits of centralized management and visibility.

## Implementation

### Setting Up the Architecture

To implement this recommended pattern, follow these key steps:

1. **Deploy your central ACM hub cluster** - This will serve as your primary management interface
2. **Deploy MCE clusters** as hosting infrastructure for your control planes
3. **Configure discovery** to ensure the ACM hub can manage both MCE clusters and their hosted control planes

### Discovery Configuration

For detailed instructions on configuring your central ACM hub to discover and manage hosted clusters from MCE clusters, refer to:
[Discovering Hosted Clusters from MCE Clusters](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/discovering_hostedclusters.md)

### Migrating from Multiple ACM Hubs

**Important limitation**: ACM cannot manage another ACM cluster.

If you've already deployed multiple ACM hub clusters for hosting control planes and want to adopt this recommended pattern, you'll need to:

1. **Convert existing ACM hosting clusters to MCE**: Uninstall ACM and reinstall MCE on clusters that will serve as hosting infrastructure
2. **Preserve existing hosted control planes**: Ensure your conversion process maintains all existing hosted control planes
3. **Designate one ACM cluster** as your central management hub

For the ACM-to-MCE conversion process, refer to:
[How to Convert existing ACM hosting clusters to MCE](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/acm_to_mce.md)
