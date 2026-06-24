package agent

import (
	"context"
	"strings"
	"time"

	"github.com/go-logr/logr"
	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	c.log.Info("Hosted Cluster admin kubeconfig updated", "secret", req.Name)

	theSecret := &corev1.Secret{}
	err := c.spokeClient.Get(ctx, req.NamespacedName, theSecret)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Secret was deleted between the predicate check and reconcile; nothing to do.
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	hcFound := false
	hostedClusterObj := &hyperv1beta1.HostedCluster{}
	secretOwners := theSecret.GetOwnerReferences()
	for _, owner := range secretOwners {
		if owner.Kind == ownerRefKind {
			hcNN := types.NamespacedName{Namespace: req.Namespace, Name: owner.Name}
			if err = c.spokeClient.Get(ctx, hcNN, hostedClusterObj); err != nil {
				if apierrors.IsNotFound(err) {
					// HostedCluster deleted between predicate check and reconcile; nothing to do.
					c.log.Info("Owning hosted cluster no longer exists, skipping annotation", "hostedCluster", owner.Name)
					return ctrl.Result{}, nil
				}
				c.log.Error(err, "Failed to find the owning hosted cluster", "hostedCluster", owner.Name)
				return ctrl.Result{}, err
			}
			hcFound = true
		}
	}

	if !hcFound {
		// No HostedCluster owner reference found; guard against race conditions.
		// This is a terminal state for this secret — do not requeue.
		c.log.Info("No HostedCluster owner reference found on admin kubeconfig, skipping", "secret", req.Name, "namespace", req.Namespace)
		return ctrl.Result{}, nil
	}

	originalHC := hostedClusterObj.DeepCopy()

	// Add/update the annotation to the hostedcluster
	if hostedClusterObj.ObjectMeta.Annotations == nil {
		hostedClusterObj.ObjectMeta.Annotations = make(map[string]string)
	}

	currentTime := time.Now()
	hostedClusterObj.Annotations[hcAnnotation] = currentTime.Format(time.RFC3339)
	c.log.Info("Annotated HostedCluster with kubeconfig update timestamp", "hostedCluster", hostedClusterObj.Name, "annotation", hcAnnotation)

	if err := c.spokeClient.Patch(ctx, hostedClusterObj, client.MergeFromWithOptions(originalHC)); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}
