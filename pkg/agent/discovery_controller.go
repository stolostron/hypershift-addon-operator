package agent

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	v1 "github.com/openshift/api/config/v1"
	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	discoveryv1 "github.com/stolostron/discovery/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"github.com/hashicorp/go-version"
)

type DiscoveryController struct {
	hubClient   client.Client
	spokeClient client.Client
	clusterName string
	log         logr.Logger
}

var DiscoveryPredicateFunctions = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return true
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		return true
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return true
	},
	GenericFunc: func(e event.GenericEvent) bool {
		return false
	},
}

// SetupWithManager sets up the controller with the Manager.
func (c *DiscoveryController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1beta1.HostedCluster{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		WithEventFilter(DiscoveryPredicateFunctions).
		Complete(c)
}

func (c *DiscoveryController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	hc := &hyperv1beta1.HostedCluster{}
	err := c.spokeClient.Get(ctx, req.NamespacedName, hc)

	hcDeleted := false

	if err != nil {
		if apierrors.IsNotFound(err) {
			c.log.Info(fmt.Sprintf("hosted cluster %s/%s is deleted.", req.NamespacedName, req.Name))
			hcDeleted = true
		} else {
			c.log.Error(err, fmt.Sprintf("failed to get the hostedcluster (%s)",
				req.NamespacedName))
			return ctrl.Result{}, err
		}
	}

	if hcDeleted {
		c.log.Info(fmt.Sprintf("deleting the discovered cluster for hosted cluster %s", req.NamespacedName))

	} else {
		// check if controlplane is available, if not then requeue until it is
		if hc.Status.Conditions == nil || len(hc.Status.Conditions) == 0 || !isHostedControlPlaneAvailable(hc.Status) {
			// wait for HCP API server to become available
			c.log.Info(fmt.Sprintf("hosted control plane of (%s) is unavailable", req.NamespacedName))
			return ctrl.Result{}, nil
		}

		c.log.Info(fmt.Sprintf("creating a discovered cluster for hosted cluster %s", req.NamespacedName))

		if err := c.createDiscoveredCluster(*hc, ctx); err != nil {
			c.log.Error(err, fmt.Sprintf("could not create discovered cluster for hosted cluster (%s)", hc.Name))

			if !apierrors.IsAlreadyExists(err) {
				return ctrl.Result{}, nil
			}
		}
	}

	// skip hosted cluster discovery if disabled
	/*if !strings.EqualFold(os.Getenv("ENABLE_HC_DISCOVERY"), "true") {
		c.log.Info("hosted cluster discovery is disabled")
		return ctrl.Result{}, nil
	}

	hc := &hyperv1beta1.HostedCluster{}
	if err := c.spokeClient.Get(ctx, req.NamespacedName, hc); err != nil {
		c.log.Error(err, fmt.Sprintf("failed to get the hostedcluster (%s/%s)",
			req.Name, req.Namespace))
		return ctrl.Result{}, nil
	}

	// check if controlplane is available, if not then requeue until it is
	if hc.Status.Conditions == nil || len(hc.Status.Conditions) == 0 || !c.isHostedControlPlaneAvailable(hc.Status) {
		// wait for cluster to become available, check again in a minute
		c.log.Info(fmt.Sprintf("hosted control plane of (%s) is unavailable, retrying in 1 minute", req.NamespacedName))
		return ctrl.Result{Requeue: true, RequeueAfter: time.Duration(1) * time.Minute}, nil
	}
	// once available, create managed cluster
	if err := c.createManagedCluster(*hc, ctx); err != nil {
		c.log.Error(err, fmt.Sprintf("could not create managed cluster for hosted cluster (%s)", hc.Name))

		// continue with klusterletaddonconfig creation if managedcluster already exists
		if !apierrors.IsAlreadyExists(err) {
			return ctrl.Result{}, nil
		}

	}

	if !c.isACMInstalled(ctx) {
		c.log.Info("ACM is not installed, skipping klusterletaddonconfig creation")
		return ctrl.Result{}, nil
	}

	// wait for NS to be created to create KAC
	if err := c.waitForNamespace(hc.Name, 60*time.Second); err == nil {
		if err := c.createKlusterletAddonConfig(hc.Name, ctx); err != nil {
			c.log.Error(err, fmt.Sprintf("could not create KlusterletAddonConfig (%s)", hc.Name))
		}
	} else {
		c.log.Error(err, "timed out waiting for namespace (%s)", hc.Name)
	}*/

	return ctrl.Result{}, nil
}

// creates managed cluster from hosted cluster
func (c *DiscoveryController) createDiscoveredCluster(hc hyperv1beta1.HostedCluster, ctx context.Context) error {
	dc := &discoveryv1.DiscoveredCluster{}
	dc.Name = hc.Name
	dc.Namespace = c.clusterName
	err := c.hubClient.Get(ctx, types.NamespacedName{Namespace: c.clusterName, Name: dc.Name}, dc)
	if err != nil {
		if apierrors.IsNotFound(err) {
			c.log.Info(fmt.Sprintf("creating discovered cluster (%s)", dc.Name))
			c.populateDiscoveredClusterData(dc)
			if err = c.hubClient.Create(ctx, dc, &client.CreateOptions{}); err != nil {
				c.log.Error(err, fmt.Sprintf("failed to create discovered cluster (%s)", dc.Name))
				return err
			}
		} else {
			return err
		}
	}
	return nil
}

// populate discovered cluster data for creation
func (c *DiscoveryController) populateDiscoveredClusterData(dc *discoveryv1.DiscoveredCluster, hcStatus hyperv1beta1.HostedClusterStatus) {
	spec := &discoveryv1.DiscoveredClusterSpec{
		APIURL:           getAPIServerURL(hcStatus),
		DisplayName:      c.clusterName + "-" + dc.Name,
		IsManagedCluster: false,
		Type:             "MultiClusterEngineHCP",
		//OpenshiftVersion: ,
	}
	dc.Spec = *spec
	/*	mc.Spec.LeaseDurationSeconds = 60
		if mc.Labels == nil {
			mc.Labels = make(map[string]string)
		}
		labels := map[string]string{
			"name":          mc.Name,
			"vendor":        "OpenShift",   // This is always true
			"cloud":         "auto-detect", // Work addon will use this to detect cloud provider, like: GCP,AWS
			clusterSetLabel: "default",
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
			createdViaAnno:         createdViaHypershift,
		}
		for key, value := range annotations {
			if v, ok := mc.Annotations[key]; !ok || len(v) == 0 {
				mc.Annotations[key] = value
			}
		}
	*/
}

func getAPIServerURL(status hyperv1beta1.HostedClusterStatus) string {
	return fmt.Sprintf("https://%s:%s", status.ControlPlaneEndpoint.Host, fmt.Sprint(status.ControlPlaneEndpoint.Port))
}

func getOCPVersion(status hyperv1beta1.HostedClusterStatus) string {
	version := version.NewVersion("0.0")
	if len(status.Version.History) > 0 {
		for _, history := range status.Version.History {
			if history.State == v1.CompletedUpdate {
				history.Version = 
			}
		}
	}
	return ""
}
