package agent

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

var (
	operatorDeploymentNsn    = types.NamespacedName{Namespace: util.HypershiftOperatorNamespace, Name: util.HypershiftOperatorName}
	externalDNSDeploymentNsn = types.NamespacedName{Namespace: util.HypershiftOperatorNamespace, Name: util.HypershiftOperatorExternalDNSName}
)

// AddonStatusController reconciles Hypershift addon status
type AddonStatusController struct {
	spokeClient client.Client
	hubClient   client.Client
	log         logr.Logger
	addonNsn    types.NamespacedName
	clusterName string
}

// AddonStatusPredicateFunctions defines which Deployment this controller should watch
var AddonStatusPredicateFunctions = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		deployment := e.Object.(*appsv1.Deployment)
		return containsHypershiftAddonDeployment(*deployment)
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldDeployment := e.ObjectOld.(*appsv1.Deployment)
		newDeployment := e.ObjectNew.(*appsv1.Deployment)
		return containsHypershiftAddonDeployment(*oldDeployment) && containsHypershiftAddonDeployment(*newDeployment)
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		deployment := e.Object.(*appsv1.Deployment)
		return containsHypershiftAddonDeployment(*deployment)
	},
}

// SetupWithManager sets up the controller with the Manager.
func (c *AddonStatusController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(util.AddonStatusControllerName).
		For(&appsv1.Deployment{}).
		WithEventFilter(AddonStatusPredicateFunctions).
		Complete(c)
}

// Reconcile updates the Hypershift addon status based on the Deployment status.
func (c *AddonStatusController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c.log.Info(fmt.Sprintf("reconciling Deployment %s", req))
	defer c.log.Info(fmt.Sprintf("done reconcile Deployment %s", req))

	checkExtDNS, err := c.shouldCheckExternalDNSDeployment(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	operatorDeployment, err := c.getDeployment(ctx, operatorDeploymentNsn)
	if err != nil {
		return ctrl.Result{}, err
	}

	var externalDNSDeployment *appsv1.Deployment
	if checkExtDNS {
		externalDNSDeployment, err = c.getDeployment(ctx, externalDNSDeploymentNsn)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	// generate addon status condition based on the deployments
	addonCondition := checkDeployments(checkExtDNS, operatorDeployment, externalDNSDeployment)

	// update the addon status
	updated, err := c.updateStatus(
		ctx, updateConditionFn(&addonCondition))
	if err != nil {
		c.log.Error(err, "failed to update the addon status")
		return ctrl.Result{}, err
	}

	if updated {
		c.log.Info("updated ManagedClusterAddOnStatus")
	} else {
		c.log.V(4).Info("skip updating updated ManagedClusterAddOnStatus")
	}

	return ctrl.Result{}, nil
}

func (c *AddonStatusController) UpdateInitialStatus(ctx context.Context) error {
	hypershiftAddon := &addonv1alpha1.ManagedClusterAddOn{}
	err := c.hubClient.Get(ctx, c.addonNsn, hypershiftAddon)

	if err != nil {
		return err
	}

	oldStatus := &hypershiftAddon.Status

	initializeStatus := true
	for _, condition := range oldStatus.Conditions {
		if condition.Reason == degradedReasonHypershiftDeployed &&
			(condition.Status == metav1.ConditionTrue || condition.Status == metav1.ConditionFalse) &&
			condition.Type == string(addonv1alpha1.ManagedClusterAddOnConditionDegraded) {
			initializeStatus = false
		}
	}

	if initializeStatus {
		// If ManagedClusterAddOnConditionDegraded condition type has no status value, initialize it with Degraded=true
		initialAddonStatus := metav1.Condition{
			Type:    addonv1alpha1.ManagedClusterAddOnConditionDegraded,
			Status:  metav1.ConditionTrue,
			Reason:  degradedReasonHypershiftDeployed,
			Message: degradedReasonOperatorNotFound,
		}

		_, err = c.updateStatus(ctx, updateConditionFn(&initialAddonStatus))
		if err != nil {
			return fmt.Errorf("unable to update initial addon status: err: %w", err)
		}
	}

	return nil
}

func (c *AddonStatusController) shouldCheckExternalDNSDeployment(ctx context.Context) (bool, error) {
	extDNSSecretKey := types.NamespacedName{Name: util.HypershiftExternalDNSSecretName, Namespace: c.clusterName}
	sExtDNS := &corev1.Secret{}
	err := c.hubClient.Get(ctx, extDNSSecretKey, sExtDNS)
	if err != nil {
		if apierrors.IsNotFound(err) {
			c.log.V(4).Info(fmt.Sprintf("external dns secret(%s) was not found", extDNSSecretKey))
			return false, nil
		}

		c.log.Error(err, fmt.Sprintf("failed to get the external dns secret(%s)", extDNSSecretKey))
		return false, err
	}

	c.log.Info(fmt.Sprintf("found external dns secret(%s)", extDNSSecretKey))
	return true, nil
}

func (c *AddonStatusController) getDeployment(ctx context.Context, nsn types.NamespacedName) (
	*appsv1.Deployment, error) {
	deployment := &appsv1.Deployment{}
	err := c.spokeClient.Get(ctx, nsn, deployment)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, err
		}
		return nil, nil
	}

	return deployment, nil
}

type UpdateStatusFunc func(status *addonv1alpha1.ManagedClusterAddOnStatus)

func (c *AddonStatusController) updateStatus(ctx context.Context, updateFuncs ...UpdateStatusFunc) (
	bool, error) {
	updated := false
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		hypershiftAddon := &addonv1alpha1.ManagedClusterAddOn{}
		err := c.hubClient.Get(ctx, c.addonNsn, hypershiftAddon)
		if apierrors.IsNotFound(err) {
			return nil
		}

		if err != nil {
			return err
		}

		oldStatus := &hypershiftAddon.Status

		newStatus := oldStatus.DeepCopy()
		for _, update := range updateFuncs {
			update(newStatus)
		}

		if equality.Semantic.DeepEqual(oldStatus, newStatus) {
			return nil
		}

		hypershiftAddon.Status = *newStatus
		err = c.hubClient.Status().Update(ctx, hypershiftAddon, &client.SubResourceUpdateOptions{})
		if err != nil {
			return err
		}

		updated = err == nil

		return err
	})

	return updated, err
}

func updateConditionFn(cond *metav1.Condition) UpdateStatusFunc {
	return func(oldStatus *addonv1alpha1.ManagedClusterAddOnStatus) {
		meta.SetStatusCondition(&oldStatus.Conditions, *cond)
	}
}
