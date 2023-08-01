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
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	autoImportAnnotation      = "auto-imported"
	addOnDeploymentConfigName = "hypershift-addon-deploy-config"
	hostingClusterNameAnno    = "import.open-cluster-management.io/hosting-cluster-name"
	klueterletDeployMode      = "import.open-cluster-management.io/klusterlet-deploy-mode"
	createdViaAnno            = "open-cluster-management/created-via"
	clusterSetLabel           = "cluster.open-cluster-management.io/clusterset"
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
		return true
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return false
	},
}

// SetupWithManager sets up the controller with the Manager.
func (c *AutoImportController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1beta1.HostedCluster{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		WithEventFilter(AutoImportPredicateFunctions).
		Complete(c)
}

func (c *AutoImportController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	//Check if addon deployment exists, if auto import is disabled, skip

	autoImportDisabled := false
	adc := &addonv1alpha1.AddOnDeploymentConfig{}
	adcNsn := types.NamespacedName{Namespace: "multicluster-engine", Name: addOnDeploymentConfigName}
	if err := c.spokeClient.Get(ctx, adcNsn, adc); err != nil {
		c.log.Error(err, fmt.Sprintf("Could not get AddonDeploymentConfig (%s/%s)", adcNsn.Name, adcNsn.Namespace))
	} else {
		autoImportDisabled = containsFlag("autoImportDisabled", adc.Spec.CustomizedVariables) == "true"
	}

	if autoImportDisabled {
		c.log.Info("Auto import is disabled, skip auto importing")
		return ctrl.Result{}, nil
	}

	hc := &hyperv1beta1.HostedCluster{}
	if err := c.spokeClient.Get(ctx, req.NamespacedName, hc); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		c.log.Error(err, fmt.Sprintf("failed to get the hostedcluster %s/%s", req.NamespacedName.Name, req.NamespacedName.Namespace))
		return ctrl.Result{}, nil
	}

	//Over here, check if controlplane is available, if not then requeue until it is
	if hc.Status.Conditions == nil || len(hc.Status.Conditions) == 0 || !c.isHostedControlPlaneAvailable(hc.Status) {
		// Wait for cluster to become available, check again in a minute
		c.log.Info(fmt.Sprintf("Hosted control plane of %s is unavailable, retrying in 1 minute", req.NamespacedName))
		return ctrl.Result{Requeue: true, RequeueAfter: time.Duration(1) * time.Minute}, nil
	}
	//Once available, create managed cluster
	c.createManagedCluster(*hc, ctx)

	return ctrl.Result{}, nil
}

// Returns string if flag is found in list, otherwise ""
func containsFlag(flagToFind string, list []addonv1alpha1.CustomizedVariable) string {
	for _, flag := range list {
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

// ensureManagedCluster creates the managed cluster
func (c *AutoImportController) createManagedCluster(hc hyperv1beta1.HostedCluster, ctx context.Context) error {
	mc := clusterv1.ManagedCluster{}
	mc.Name = hc.Name
	err := c.spokeClient.Get(ctx, types.NamespacedName{Name: mc.Name}, &mc)
	if apierrors.IsNotFound(err) {
		c.log.Info(fmt.Sprintf("Creating managed cluster %s", mc.Name))
		
		populateManagedClusterData(&mc)
		fmt.Println(mc)
		if err = c.spokeClient.Create(ctx, &mc, &client.CreateOptions{}); err != nil {
			c.log.Error(err, fmt.Sprintf("Failed at creating managed cluster %s", mc.Name))
			return err
		}
		// ensureManagedClusterObjectMeta(&mc, hydNamespaceName, managedClusterSetName, managementClusterName)
		// if err = r.Create(ctx, &mc, &client.CreateOptions{}); err != nil {
		// 	log.V(ERROR).Info("Could not create ManagedCluster resource", "error", err)
		// 	return nil, err
		// }

		// return &mc, nil
	}
	return nil
}

func populateManagedClusterData(mc *clusterv1.ManagedCluster) error {
	mc.Spec.HubAcceptsClient = true
	mc.Spec.LeaseDurationSeconds = 60
	if mc.Labels == nil {
		mc.Labels = make(map[string]string)
	}
	labels := map[string]string{
		"name":   mc.Name,
		"vendor": "OpenShift",   // This is always true
		"cloud":  "auto-detect", // Work addon will use this to detect cloud provider, like: GCP,AWS
		//clusterSetLabel:         "default",
	}
	for key, value := range labels {
		if v, ok := mc.Labels[key]; !ok || len(v) == 0 {
			mc.Labels[key] = value
		}
	}

	if mc.Annotations == nil {
		mc.Annotations = make(map[string]string)
	}
	annotations := map[string]string{
		klueterletDeployMode:   "Hosted",
		hostingClusterNameAnno: "local-cluster",
		createdViaAnno:         "other", // maybe change for auto-import?
		//"auto-import-time": time.Now().Format(time.RFC3339),
	}

	for key, value := range annotations {
		if v, ok := mc.Annotations[key]; !ok || len(v) == 0 {
			mc.Annotations[key] = value
		}
	}

	return nil
}
