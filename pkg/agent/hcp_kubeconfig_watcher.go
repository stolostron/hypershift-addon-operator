package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	adminKubeconfigSuffix = "admin-kubeconfig"
	ownerRefKind          = "HostedCluster"
)

type HcpKubeconfigChangeWatcher struct {
	hubClient   client.Client
	spokeClient client.Client
	log         logr.Logger
}

var HcpKubeconfigChangeWatcherPredicateFunctions = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return false
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		if !strings.HasSuffix(e.ObjectNew.GetName(), adminKubeconfigSuffix) {
			return false
		}

		ownerRefs := e.ObjectNew.GetOwnerReferences()

		for _, owner := range ownerRefs {
			if owner.Kind == ownerRefKind {
				return true
			}
		}

		return false
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return false
	},
	GenericFunc: func(e event.GenericEvent) bool {
		return false
	},
}

// SetupWithManager sets up the controller with the Manager.
func (c *HcpKubeconfigChangeWatcher) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("HcpKubeconfigChangeWatcher").
		For(&corev1.Secret{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 5}).
		WithEventFilter(HcpKubeconfigChangeWatcherPredicateFunctions).
		Complete(c)
}

func (c *HcpKubeconfigChangeWatcher) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c.log.Info(fmt.Sprintf("Hosted Cluster admin kubeconfig %s updated.", req.Name))

	theSecret := &corev1.Secret{}
	err := c.spokeClient.Get(ctx, req.NamespacedName, theSecret)
	if err != nil {
		return ctrl.Result{}, err
	}

	hcFound := false
	hostedClusterObj := &hyperv1beta1.HostedCluster{}
	secretOwners := theSecret.GetOwnerReferences()
	for _, owner := range secretOwners {
		if owner.Kind == ownerRefKind {
			hcNN := types.NamespacedName{Namespace: req.Namespace, Name: owner.Name}
			err = c.spokeClient.Get(ctx, hcNN, hostedClusterObj)
			if err != nil {
				c.log.Error(err, fmt.Sprintf("Failed to find the owning hosted cluster %s.", owner.Name))
				return ctrl.Result{}, err
			}
			hcFound = true
		}
	}

	if !hcFound {
		c.log.Error(err, fmt.Sprintf("Failed to find an owning hosted cluster for this admin kubeconfig %s.", req.Name))
		return ctrl.Result{}, err
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
