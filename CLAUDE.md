# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

### Build and Development
- `make build` - Build the hypershift-addon binary
- `make run` - Run the controller locally (applies fmt/vet first)
- `make docker-build` - Build Docker image
- `make vendor` - Update Go modules and vendor directory

### Testing
- `make test` - Run unit tests (applies fmt/vet/envtest first)
- `make build-e2e` - Build e2e test binary
- `make test-e2e` - Run end-to-end tests (requires deployment)

### Code Quality
- `make fmt` - Format Go code with `go fmt`
- `make vet` - Run `go vet` for static analysis

### Deployment
- `make deploy-addon-manager` - Deploy addon manager to multicluster-engine namespace
- `make deploy-ocm` - Install Open Cluster Management (OCM)
- `make quickstart` - Run quickstart setup script

## Architecture

This is a Kubernetes addon operator for HyperShift that manages hypershift-operator deployments on managed clusters in an Open Cluster Management (OCM) environment.

### Core Components

**Manager**: Runs on the hub cluster and manages addon installations across multiple managed clusters. The manager component handles:
- Addon lifecycle management
- Coordination with OCM framework
- CLI download and installation functionality

**Agent**: Deployed to managed clusters to run the actual hypershift-operator and related controllers:
- `addon_status_controller.go` - Reports addon health status back to hub
- `auto_import_controller.go` - Automatically imports hosted clusters
- `discovery_agent.go` - Discovers cluster resources and capabilities
- `external_secret_controller.go` - Manages external secrets integration
- `hcp_capacity_calculation.go` - Calculates hosted control plane capacity
- `hcp_kubeconfig_watcher.go` - Watches kubeconfig changes for hosted clusters

### Key Packages

- `pkg/manager/` - Hub cluster management logic
- `pkg/agent/` - Managed cluster agent functionality
- `pkg/install/` - HyperShift installation and upgrade logic
- `pkg/metrics/` - Prometheus metrics collection
- `pkg/util/` - Shared utilities and constants

### Entry Points

The main binary supports three modes via cobra commands:
- `hypershift-addon manager` - Hub cluster manager mode
- `hypershift-addon agent` - Managed cluster agent mode
- `hypershift-addon cleanup` - Cleanup operations

### Dependencies

Built on the OCM addon-framework and integrates with:
- HyperShift for hosted control planes
- Open Cluster Management for multi-cluster operations
- Kubernetes controller-runtime for reconciliation loops
- Prometheus for metrics collection

### Testing Strategy

- Unit tests use Ginkgo/Gomega framework
- Tests include controller logic, status helpers, and capacity calculations
- E2E tests verify full deployment scenarios
- Tests require `KUBEBUILDER_ASSETS` environment for envtest