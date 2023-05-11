package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	hyperv1beta1 "github.com/openshift/hypershift/api/v1beta1"

	operatorapiv1 "open-cluster-management.io/api/operator/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type ExternalSecretController struct {
	hubClient client.Client
	log       logr.Logger
}

var ExternalSecretPredicateFunctions = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return true
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		return false
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return false
	},
}

// SetupWithManager sets up the controller with the Manager.
func (c *ExternalSecretController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorapiv1.Klusterlet{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		WithEventFilter(ExternalSecretPredicateFunctions).
		Complete(c)
}

// Reconcile updates the Hypershift addon status based on the Deployment status.
func (c *ExternalSecretController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c.log.Info(fmt.Sprintf("reconciling annotation for %s", req.Name))
	defer c.log.Info(fmt.Sprintf("done reconciling annotation for %s", req.Name))
	if !strings.Contains(req.Name, "klusterlet-") {
		c.log.Info("klusterlet not from a managed cluster")
		return ctrl.Result{}, nil //No need to error
	}

	_, hostedClusterName, _ := strings.Cut(req.Name, "klusterlet-")

	lo := &client.ListOptions{}
	hostedClusters := &hyperv1beta1.HostedClusterList{}

	// List the HostedCluster objects across all namespaces
	if err := c.hubClient.List(ctx, hostedClusters, lo); err != nil {
		c.log.Error(err, "Unable to list hosted clusters in all namespaces")
		return ctrl.Result{}, err
	}

	obj := &hyperv1beta1.HostedCluster{}
	// Loop over the list of HostedCluster objects and find the one with the specified name
	for index, hc := range hostedClusters.Items {
		if hc.Name == hostedClusterName {
			obj = &hc
			break
		}
		if index == len(hostedClusters.Items) {
			errh := errors.New("could not retrieve hosted cluster")
			c.log.Error(errh, fmt.Sprintf("Unable to find hosted clusters with name %s", hostedClusterName))
			return ctrl.Result{}, errh
		}
	}

	if obj.Annotations["create-external-hub-kubeconfig"] == "true" {
		return ctrl.Result{}, nil // object already annotated, nothing to do
	}

	// Add the annotation to the object
	obj.Annotations["create-external-hub-kubeconfig"] = "true"

	// Update the object in the Kubernetes API server
	if err := c.hubClient.Update(ctx, obj); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}
