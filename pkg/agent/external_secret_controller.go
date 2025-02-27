package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	operatorapiv1 "open-cluster-management.io/api/operator/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	hcAnnotation = "create-external-hub-kubeconfig"
)

type ExternalSecretController struct {
	hubClient        client.Client
	spokeClient      client.Client
	clusterName      string
	localClusterName string
	log              logr.Logger
}

var ExternalSecretPredicateFunctions = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		newKlusterlet, newOK := e.Object.(*operatorapiv1.Klusterlet)

		if !newOK {
			return false
		}

		// Only for hosted cluster klusterlets
		return newKlusterlet.Spec.DeployOption.Mode == operatorapiv1.InstallModeSingletonHosted
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
	c.log.Info(fmt.Sprintf("reconciling klusterlet: %s", req.Name))
	defer c.log.Info(fmt.Sprintf("done reconciling klusterlet: %s", req.Name))

	if !strings.Contains(req.Name, "klusterlet-") {
		c.log.Info("klusterlet not from a hosted cluster")
		return ctrl.Result{}, nil //No need to error
	}

	klusterlet := &operatorapiv1.Klusterlet{}
	if err := c.spokeClient.Get(ctx, req.NamespacedName, klusterlet); err != nil {
		c.log.Error(err, "unable to find the klusterlet")
		return ctrl.Result{Requeue: false}, err
	}

	if klusterlet.Spec.DeployOption.Mode != operatorapiv1.InstallModeSingletonHosted {
		c.log.Info("this klusterlet's install mode is not SingletonHosted. Skip reconciling.")
		return ctrl.Result{}, nil
	}

	_, hostedClusterName, _ := strings.Cut(req.Name, "klusterlet-")

	_, discoveredHostedClusterName, _ := strings.Cut(req.Name, "klusterlet-"+c.clusterName+"-")

	lo := &client.ListOptions{}
	hostedClusters := &hyperv1beta1.HostedClusterList{}

	// List the HostedCluster objects across all namespaces
	if err := c.spokeClient.List(ctx, hostedClusters, lo); err != nil {
		c.log.Error(err, "unable to list hosted clusters in all namespaces")
		return ctrl.Result{}, err
	}

	hostedClusterObj := &hyperv1beta1.HostedCluster{}
	// Loop over the list of HostedCluster objects and find the one with the specified name
	for index, hc := range hostedClusters.Items {
		if hc.Name == hostedClusterName {
			hostedClusterObj = &hostedClusters.Items[index]
			break
		}
	}

	if hostedClusterObj.Name == "" && !strings.EqualFold(c.clusterName, c.localClusterName) {
		// Loop over the list of HostedCluster objects and find the one with the specified name
		for index, hc := range hostedClusters.Items {
			if hc.Name == discoveredHostedClusterName {
				hostedClusterObj = &hostedClusters.Items[index]
				break
			}
		}
	}

	//Could not find hosted cluster
	if hostedClusterObj.Name == "" {
		c.log.Info(fmt.Sprintf("unable to find hosted cluster with name %s", hostedClusterName))
		return ctrl.Result{RequeueAfter: time.Duration(2) * time.Minute}, nil
	}

	originalHC := hostedClusterObj.DeepCopy()

	// Add/update the annotation to the hostedcluster
	if hostedClusterObj.ObjectMeta.Annotations == nil { // Create the annotation map if it doesn't exist
		hostedClusterObj.ObjectMeta.Annotations = make(map[string]string)
	}

	currentTime := time.Now()
	hostedClusterObj.Annotations[hcAnnotation] = currentTime.Format(time.RFC3339)
	c.log.Info(fmt.Sprintf("Annotated %s with %s", hostedClusterObj.Name, hcAnnotation))

	if err := c.spokeClient.Patch(ctx, hostedClusterObj, client.MergeFromWithOptions(originalHC)); err != nil { //Add/update hostedcluster annotation
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}
