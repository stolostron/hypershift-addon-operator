package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
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
	autoImportAnnotation = "auto-imported"
	addOnDeploymentConfigName = "hypershift-addon-deploy-config"
)

type AutoImportController struct {
	hubClient   client.Client
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
	}
	//  else if string(adc.Spec.CustomizedVariables["autoImportDisabled"]) != "" {
	// 	autoImportDisabled := adc.Spec.CustomizedVariables["autoImportDisabled"].Value == "true"
		
	// }
	

	
	if autoImportDisabled {
		c.log.Info("Auto import is disabled, skip auto importing")
		return ctrl.Result{Requeue: true, RequeueAfter: time.Duration(1) * time.Minute}, nil
	}
	return ctrl.Result{}, nil
}

