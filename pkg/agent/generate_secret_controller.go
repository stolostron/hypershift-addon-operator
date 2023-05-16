package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	hyperv1beta1 "github.com/openshift/hypershift/api/v1beta1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	operatorapiv1 "open-cluster-management.io/api/operator/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	klusterletAnnotationFinalizer = "operator.open-cluster-management.io/hc-secret-annotation"
	hcAnnotation                  = "create-external-hub-kubeconfig"
)

type ExternalSecretController struct {
	hubClient   client.Client
	spokeClient client.Client
	log         logr.Logger
}

var ExternalSecretPredicateFunctions = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		klog.Info("KLOG TRIGERRED BY CREATE")
		return true
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		klusterletOld := e.ObjectOld.(*operatorapiv1.Klusterlet)
		klusterletNew := e.ObjectNew.(*operatorapiv1.Klusterlet)
		if (klusterletOld.ObjectMeta.DeletionTimestamp.IsZero() != klusterletNew.ObjectMeta.DeletionTimestamp.IsZero()) {
			klog.Info("TRIGERRED BY UPDATE KLOG true")
			klog.Info("Old has deletion time stamp?", klusterletOld.ObjectMeta.DeletionTimestamp.IsZero())
			klog.Info("New has deletion time stamp?", klusterletNew.ObjectMeta.DeletionTimestamp.IsZero())
		}
		return klusterletOld.ObjectMeta.DeletionTimestamp.IsZero() != klusterletNew.ObjectMeta.DeletionTimestamp.IsZero()
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

	//Get klusterlet
	klusterletNamespaceNsn := types.NamespacedName{
		Name:      req.Name,
		Namespace: req.Namespace,
	}
	klusterlet := &operatorapiv1.Klusterlet{}
	if err := c.spokeClient.Get(ctx, klusterletNamespaceNsn, klusterlet); err != nil {
		c.log.Error(err, "Unable to get klusterlet")
		return ctrl.Result{}, err
	}

	_, hostedClusterName, _ := strings.Cut(req.Name, "klusterlet-")

	lo := &client.ListOptions{}
	hostedClusters := &hyperv1beta1.HostedClusterList{}

	// List the HostedCluster objects across all namespaces
	if err := c.spokeClient.List(ctx, hostedClusters, lo); err != nil {
		c.log.Error(err, "Unable to list hosted clusters in all namespaces")
		return ctrl.Result{}, err
	}

	hostedClusterObj := &hyperv1beta1.HostedCluster{}
	// Loop over the list of HostedCluster objects and find the one with the specified name
	for index, hc := range hostedClusters.Items {
		if hc.Name == hostedClusterName {
			hostedClusterObj = &hc
			break
		}
		if index == len(hostedClusters.Items) {
			errh := errors.New("could not retrieve hosted cluster")
			c.log.Error(errh, fmt.Sprintf("Unable to find hosted clusters with name %s", hostedClusterName))
			return ctrl.Result{}, errh
		}
	}

	if klusterlet.ObjectMeta.DeletionTimestamp.IsZero() {

		// Add finalizer if it doesn't exist
		if !controllerutil.ContainsFinalizer(klusterlet, klusterletAnnotationFinalizer) {
			controllerutil.AddFinalizer(klusterlet, klusterletAnnotationFinalizer)
			c.log.Info(fmt.Sprintf("Added finalizer to %s", klusterlet.Name))
		}

		// Add the annotation to the hostedcluster if it doesn't exist
		if _, ok := hostedClusterObj.ObjectMeta.Annotations[hcAnnotation]; !ok {
			hostedClusterObj.Annotations[hcAnnotation] = "true"
			c.log.Info(fmt.Sprintf("Annotated %s with %s", hostedClusterObj.Name, hcAnnotation))
		}

	} else {
		//Remove annotation from hosted cluster
		if _, ok := hostedClusterObj.ObjectMeta.Annotations[hcAnnotation]; ok {
			delete(hostedClusterObj.Annotations, hcAnnotation)
			c.log.Info(fmt.Sprintf("Removed annotation %s from %s", hcAnnotation, hostedClusterObj.Name))
		}

		// Remove finalizer
		if controllerutil.ContainsFinalizer(klusterlet, klusterletAnnotationFinalizer) {
			controllerutil.RemoveFinalizer(klusterlet, klusterletAnnotationFinalizer)
			c.log.Info(fmt.Sprintf("Removed finalizer from %s", klusterlet.Name))
		}

	}

	if err := c.spokeClient.Update(ctx, klusterlet); err != nil { //Add/remove klusterlet finalizer
		return ctrl.Result{}, err
	}
	if err := c.spokeClient.Update(ctx, hostedClusterObj); err != nil { //Add/remove hostedcluster annotation
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}
