package agent

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
)

func reconcileAddonStatus(ctx context.Context, req ctrl.Request) error {
	_, err := addonStatusController.Reconcile(ctx, req)
	return err
}

func reconcileDiscoveryAgent(ctx context.Context, req ctrl.Request) error {
	_, err := discoveryAgentController.Reconcile(ctx, req)
	return err
}

func reconcileHcpKubeconfigWatcher(ctx context.Context, req ctrl.Request) error {
	_, err := hcpKubeconfigWatcher.Reconcile(ctx, req)
	return err
}
