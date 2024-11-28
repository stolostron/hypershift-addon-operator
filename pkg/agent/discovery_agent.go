package agent

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	"github.com/hashicorp/go-version"
	configv1 "github.com/openshift/api/config/v1"
	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	discoveryv1 "github.com/stolostron/discovery/api/v1"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type DiscoveryAgent struct {
	hubClient        client.Client
	spokeClient      client.Client
	clusterName      string
	localClusterName string
	log              logr.Logger
}

// This predicate is used as an event filter
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
	//GenericEvent is an event where the operation type is unknown in which case, do not request reconciliation
	GenericFunc: func(e event.GenericEvent) bool {
		return false
	},
}

// SetupWithManager sets up the controller with the Manager.
func (c *DiscoveryAgent) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1beta1.HostedCluster{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		WithEventFilter(DiscoveryPredicateFunctions).
		Complete(c)
}

func (c *DiscoveryAgent) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// skip discovery if disabled
	if strings.EqualFold(os.Getenv("DISABLE_HC_DISCOVERY"), "true") {
		c.log.Info("hosted cluster discovery is disabled, skip discovering")
		return ctrl.Result{}, nil
	}

	// if this agent is for self managed cluster aka local-cluster, skip discovery
	if strings.EqualFold(c.clusterName, c.localClusterName) {
		c.log.Info("this is local cluster agent, skip discovering")
		return ctrl.Result{}, nil
	}

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
		if err := c.deleteDiscoveredCluster(req.Name, req.Namespace, ctx); err != nil {
			c.log.Error(err, fmt.Sprintf("could not delete discovered cluster for hosted cluster (%s)", hc.Name))
			return ctrl.Result{}, err
		}
	} else {
		// check if controlplane is available, if not then requeue until it is
		if hc.Status.Conditions == nil || len(hc.Status.Conditions) == 0 || !isHostedControlPlaneAvailable(*hc) {
			// wait for HCP API server to become available
			c.log.Info(fmt.Sprintf("hosted control plane of (%s) is unavailable", req.NamespacedName))
			return ctrl.Result{}, nil
		}

		c.log.Info(fmt.Sprintf("creating or updating a discovered cluster for hosted cluster %s", req.NamespacedName))

		if err := c.createUpdateDiscoveredCluster(*hc, ctx); err != nil {
			c.log.Error(err, fmt.Sprintf("could not create or update discovered cluster for hosted cluster (%s)", hc.Name))
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// creates discovered cluster in the hub cluster
func (c *DiscoveryAgent) createUpdateDiscoveredCluster(hc hyperv1beta1.HostedCluster, ctx context.Context) error {
	dcList, err := c.getDiscoveredClusterList(hc, ctx)
	if err != nil {
		return err
	}

	newDc := c.getDiscoveredCluster(hc)

	if len(dcList.Items) == 0 {
		c.log.Info(fmt.Sprintf("creating discovered cluster for hosted cluster (%s/%s)", hc.Namespace, hc.Name))

		if err = c.hubClient.Create(ctx, newDc, &client.CreateOptions{}); err != nil {
			c.log.Error(err, fmt.Sprintf("failed to create discovered cluster for hosted cluster (%s/%s)", hc.Namespace, hc.Name))
			return err
		}
	} else if len(dcList.Items) == 1 {
		oldDc := dcList.Items[0]

		// As a hosted cluster gets updated by the hypershift operator, the API URL and the Openshift version
		// are the only ones in DiscoveredCluster spec that could be changed.
		if oldDc.Spec.APIURL != newDc.Spec.APIURL || oldDc.Spec.OpenshiftVersion != newDc.Spec.OpenshiftVersion {
			c.log.Info(fmt.Sprintf("updating discovered cluster for hosted cluster (%s/%s)", hc.Namespace, hc.Name))

			dc := oldDc.DeepCopy()
			dc.Spec.APIURL = newDc.Spec.APIURL
			dc.Spec.OpenshiftVersion = newDc.Spec.OpenshiftVersion
			if err = c.hubClient.Update(ctx, dc); err != nil {
				c.log.Error(err, fmt.Sprintf("failed to update discovered cluster for hosted cluster (%s/%s)", hc.Namespace, hc.Name))
				return err
			}
		}
	} else {
		return fmt.Errorf("there are %s discovered clusters for hosted cluster (%s/%s)", strconv.Itoa(len(dcList.Items)), hc.Namespace, hc.Name)
	}

	return nil
}

// populate discovered cluster data for creation
func (c *DiscoveryAgent) getDiscoveredCluster(hc hyperv1beta1.HostedCluster) *discoveryv1.DiscoveredCluster {
	dc := &discoveryv1.DiscoveredCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "DiscoveredCluster",
			APIVersion: "discovery.open-cluster-management.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      hc.Spec.ClusterID,
			Namespace: c.clusterName,
			Labels: map[string]string{
				util.HostedClusterNameLabel:      hc.Name,
				util.HostedClusterNamespaceLabel: hc.Namespace,
			},
		},
		Spec: discoveryv1.DiscoveredClusterSpec{
			APIURL:                 getAPIServerURL(hc.Status),
			DisplayName:            getDiscoveredClusterName(c.clusterName, hc.Name),
			Name:                   hc.Spec.ClusterID,
			IsManagedCluster:       false,
			ImportAsManagedCluster: false,
			Type:                   "MultiClusterEngineHCP",
			OpenshiftVersion:       c.getOCPVersion(hc.Status),
			CreationTimestamp:      &hc.CreationTimestamp,
			CloudProvider:          strings.ToLower(string(hc.Spec.Platform.Type)),
			Status:                 "Active",
		},
	}

	return dc
}

// deletes discovered cluster from the hub cluster
func (c *DiscoveryAgent) deleteDiscoveredCluster(hcName string, hcNamespace string, ctx context.Context) error {
	hc := hyperv1beta1.HostedCluster{}
	hc.Name = hcName
	hc.Namespace = hcNamespace
	dcList, err := c.getDiscoveredClusterList(hc, ctx)
	if err != nil {
		return err
	}

	if len(dcList.Items) == 0 {
		c.log.Info(fmt.Sprintf("no discovered cluster to delete for hosted cluster (%s/%s)", hc.Namespace, hc.Name))
		return nil
	} else if len(dcList.Items) == 1 {
		dc := dcList.Items[0].DeepCopy()

		if err = c.hubClient.Delete(ctx, dc); err != nil {
			c.log.Error(err, fmt.Sprintf("failed to delete the discovered cluster for hosted cluster (%s/%s)", hc.Namespace, hc.Name))
			return err
		}
	} else {
		// Only one matching DiscoveredCluster is expected to exist for a hosted cluster. If there happens to be more than one DiscoveredCluster
		// resources with the same annotations, this is an error case. Do not make any assumption because removing a DiscoveredCluster could
		// remove the managed cluster. Log the error and do nothing.
		return fmt.Errorf("there are %s discovered clusters for hosted cluster (%s/%s)", strconv.Itoa(len(dcList.Items)), hc.Namespace, hc.Name)
	}

	return nil
}

// deletes discovered cluster from the hub cluster
func (c *DiscoveryAgent) getDiscoveredClusterList(hc hyperv1beta1.HostedCluster, ctx context.Context) (*discoveryv1.DiscoveredClusterList, error) {
	dcList := &discoveryv1.DiscoveredClusterList{}

	// Create label selector
	labelSelector := labels.SelectorFromSet(labels.Set{util.HostedClusterNameLabel: hc.Name, util.HostedClusterNamespaceLabel: hc.Namespace})

	err := c.hubClient.List(ctx, dcList, &client.ListOptions{
		LabelSelector: labelSelector,
		Namespace:     c.clusterName,
	})
	if err != nil {
		return nil, err
	}

	return dcList, nil
}

func getAPIServerURL(status hyperv1beta1.HostedClusterStatus) string {
	return fmt.Sprintf("https://%s:%s", status.ControlPlaneEndpoint.Host, fmt.Sprint(status.ControlPlaneEndpoint.Port))
}

func (c *DiscoveryAgent) getOCPVersion(status hyperv1beta1.HostedClusterStatus) string {
	v1, err := version.NewVersion("0")
	if err != nil {
		c.log.Error(err, "failed to create a new version")
		return ""
	}

	if status.Version != nil && len(status.Version.History) > 0 {
		for _, history := range status.Version.History {
			if history.State == configv1.CompletedUpdate {
				v2, err := version.NewVersion(history.Version)
				if err != nil {
					c.log.Error(err, fmt.Sprintf("failed to create a new version from history %v", history))
					return ""
				}
				if v1.LessThan(v2) {
					v1 = v2
				}
			}
		}
		if v1.String() != "0" {
			return v1.String()
		}
	}
	return ""
}

func getDiscoveredClusterName(spokeClusterName, hcName string) string {
	return spokeClusterName + "-" + hcName
}
