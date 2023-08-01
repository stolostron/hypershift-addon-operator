package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	hyperv1beta1 "github.com/openshift/hypershift/api/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	operatorapiv1 "open-cluster-management.io/api/operator/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	autoImportAnnotation      = "auto-imported"
	addOnDeploymentConfigName = "hypershift-addon-deploy-config"
)

type AutoImportController struct {
	//hubClient   client.Client
	spokeClient client.Client
	log         logr.Logger
}

var AutoImportPredicateFunctions = predicate.Funcs{
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
func (c *AutoImportController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorapiv1.Klusterlet{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		WithEventFilter(ExternalSecretPredicateFunctions).
		Complete(c)
}

func (c *AutoImportController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	//Check if addon deployment exists, if auto import is disabled, skip

	autoImportDisabled := false
	adc := &addonv1alpha1.AddOnDeploymentConfig{}
	adcNsn := types.NamespacedName{Namespace: "multicluster-engine", Name: addOnDeploymentConfigName}
	if err := c.spokeClient.Get(ctx, adcNsn, adc); err != nil {
		c.log.Info(fmt.Sprintf("Could not get AddonDeploymentConfig (%s/%s)", adcNsn.Name, adcNsn.Namespace))
	} else if containsFlag("autoImportDisabled", adc.Spec.CustomizedVariables) == "true" {
		autoImportDisabled = true
		c.log.Info("FOUND FLAG")
	}

	if autoImportDisabled {
		c.log.Info("Auto import is disabled, skip auto importing")
		return ctrl.Result{}, nil
	}

	//Over here, check if controlplane is available, if not requeue
	hc := &hyperv1beta1.HostedCluster{}
	if err := c.spokeClient.Get(ctx, req.NamespacedName, hc); err != nil {
		if apierrors.IsNotFound(err) {
			//Delete managed cluster over here if it exists

			return ctrl.Result{}, nil
		}

		c.log.Error(err, "failed to get the hostedcluster")
		return ctrl.Result{}, nil
	}

	if hc.Status.Conditions == nil || len(hc.Status.Conditions) == 0 || !c.isHostedControlPlaneAvailable(hc.Status) {
		// Wait for cluster to become available, check again in a minute
		return ctrl.Result{Requeue: true, RequeueAfter: time.Duration(1) * time.Minute}, nil
	}

	return ctrl.Result{}, nil
}

// Returns string if flag is found in list, otherwise ""
func containsFlag(flagToFind string, list []addonv1alpha1.CustomizedVariable) string {
	for _, flag := range list {
		fmt.Printf("Checking flag %s", flag.Name)
		if flag.Name == flagToFind {

			return flag.Value
		}
	}
	return ""
}

func (c *AutoImportController) isHostedControlPlaneAvailable(status hyperv1beta1.HostedClusterStatus) bool {
	for _, condition := range status.Conditions {
		if condition.Reason == hyperv1beta1.AsExpectedReason && condition.Status == metav1.ConditionTrue && condition.Type == string(hyperv1beta1.HostedClusterAvailable) {
			return true
		}
	}
	return false
}
