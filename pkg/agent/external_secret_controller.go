package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"

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

		// Only for hosted cluster klusterlets (both SingletonHosted and Hosted modes)
		return newKlusterlet.Spec.DeployOption.Mode == operatorapiv1.InstallModeSingletonHosted ||
			newKlusterlet.Spec.DeployOption.Mode == operatorapiv1.InstallModeHosted
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
		Named(util.ExternalSecretControllerName).
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

	_, managedClusterName, _ := strings.Cut(req.Name, "klusterlet-")

	discoveredHostedClusterName := ""
	discoveredKlusterletPrefix := "klusterlet-" + c.clusterName + "-"
	if !strings.EqualFold(c.clusterName, c.localClusterName) &&
		strings.HasPrefix(req.Name, discoveredKlusterletPrefix) {
		discoveredHostedClusterName = strings.TrimPrefix(req.Name, discoveredKlusterletPrefix)
	}

	lo := &client.ListOptions{}
	hostedClusters := &hyperv1beta1.HostedClusterList{}

	// List the HostedCluster objects across all namespaces
	if err := c.spokeClient.List(ctx, hostedClusters, lo); err != nil {
		c.log.Error(err, "unable to list hosted clusters in all namespaces")
		return ctrl.Result{}, err
	}

	hostedClusterObj := findHostedClusterForKlusterlet(
		hostedClusters, managedClusterName, discoveredHostedClusterName)

	//Could not find hosted cluster
	if hostedClusterObj == nil {
		c.log.Info(fmt.Sprintf("unable to find hosted cluster for managed cluster %s", managedClusterName))
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

// findHostedClusterForKlusterlet finds the HostedCluster represented by a hosted
// Klusterlet. The managedcluster-name annotation is authoritative because ROSA
// HostedClusters can have a generated name that differs from both the ManagedCluster
// name and the Klusterlet suffix. InfraID and name matching preserve compatibility
// with HostedClusters that predate the annotation.
func findHostedClusterForKlusterlet(
	hostedClusters *hyperv1beta1.HostedClusterList,
	managedClusterName string,
	discoveredHostedClusterName string,
) *hyperv1beta1.HostedCluster {
	for i := range hostedClusters.Items {
		hc := &hostedClusters.Items[i]
		if hc.Annotations[util.ManagedClusterAnnoKey] == managedClusterName {
			return hc
		}
	}

	for i := range hostedClusters.Items {
		hc := &hostedClusters.Items[i]
		if hc.Annotations[util.ManagedClusterAnnoKey] != "" {
			continue
		}
		if hc.Spec.InfraID == managedClusterName {
			return hc
		}
	}

	nameCandidates := []string{managedClusterName, discoveredHostedClusterName}
	for _, name := range nameCandidates {
		if name == "" {
			continue
		}
		for i := range hostedClusters.Items {
			hc := &hostedClusters.Items[i]
			if hc.Annotations[util.ManagedClusterAnnoKey] != "" {
				continue
			}
			if hc.Name == name {
				return hc
			}
		}
	}

	return nil
}
