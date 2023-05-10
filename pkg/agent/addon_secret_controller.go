package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	hyperv1beta1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type AddonSecretController struct {
	spokeClient client.Client
	log         logr.Logger
}

var AddonSecretPredicateFunctions = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return false
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		return false
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return e.Object.GetName() == util.ExternalManagedKubeconfigSecretName && strings.HasPrefix(e.Object.GetNamespace(), util.ExternalManagedKubeconfigSecretNsPrefix)
	},
}

// SetupWithManager sets up the controller with the Manager.
func (c *AddonSecretController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Secret{}).
		WithEventFilter(AddonSecretPredicateFunctions).
		Complete(c)
}

// Reconcile updates the Hypershift addon status based on the Deployment status.
func (c *AddonSecretController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c.log.Info(fmt.Sprintf("reconciling Secret %s", req))
	defer c.log.Info(fmt.Sprintf("done reconcile Secret %s", req))

	// Get hosted cluster by managed cluster name annotation
	managedclusterAnnoValue := req.Namespace[11:]
	c.log.Info("managedclusterAnnoValue=" + managedclusterAnnoValue)

	hc := c.getHostedCluster(ctx, managedclusterAnnoValue)
	if hc != nil {
		// Hosted cluster exists but secret is deleted - trigger hostedcluster reconcile to recreate secret
		if hc.Annotations == nil {
			annotations := make(map[string]string)
			hc.Annotations = annotations
		}
		hc.Annotations[util.HostedClusterRefreshAnnoKey] = strconv.FormatInt(time.Now().UnixMilli(), 10)

		if err := c.spokeClient.Update(ctx, hc, &client.UpdateOptions{}); err != nil {
			c.log.Error(err, fmt.Sprintf("failed to update refresh-time annotation in hc %v/%v", req.Namespace, req.Name))
		}
	} else {
		c.log.Info(fmt.Sprintf("No hosted cluster with name or managedcluster-name annotation value = %v", managedclusterAnnoValue))
	}

	return ctrl.Result{}, nil
}

func (c *AddonSecretController) getHostedCluster(ctx context.Context, managedClusterAnnotationValue string) *hyperv1beta1.HostedCluster {
	hcs := &hyperv1beta1.HostedClusterList{}
	if err := c.spokeClient.List(ctx, hcs, &client.ListOptions{}); err != nil {
		c.log.Error(err, "failed to list the hostedcluster")
		return nil
	}

	for _, hc := range hcs.Items {
		if hc.Annotations[util.ManagedClusterAnnoKey] == managedClusterAnnotationValue {
			return &hc
		}
	}

	for _, hc := range hcs.Items {
		if hc.Name == managedClusterAnnotationValue {
			return &hc
		}
	}

	return nil
}
