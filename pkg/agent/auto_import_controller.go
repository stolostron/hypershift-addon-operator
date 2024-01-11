package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	hyperv1beta1 "github.com/openshift/hypershift/api/v1beta1"
	operatorv1 "github.com/operator-framework/api/pkg/operators/v1"
	agentv1 "github.com/stolostron/klusterlet-addon-controller/pkg/apis/agent/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	acmOperatorNamePrefix     = "advanced-cluster-management."
	autoImportAnnotation      = "auto-imported"
	addOnDeploymentConfigName = "hypershift-addon-deploy-config"
	hostingClusterNameAnno    = "import.open-cluster-management.io/hosting-cluster-name"
	klueterletDeployMode      = "import.open-cluster-management.io/klusterlet-deploy-mode"
	createdViaAnno            = "open-cluster-management/created-via"
	clusterSetLabel           = "cluster.open-cluster-management.io/clusterset"
	createdViaHypershift      = "hypershift"
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
	GenericFunc: func(e event.GenericEvent) bool {
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
	// skip auto import if disabled
	if strings.EqualFold(os.Getenv("DISABLE_AUTO_IMPORT"), "true") {
		c.log.Info("auto import is disabled, skip auto importing")
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
	}

	return ctrl.Result{}, nil
}

// check if hosted control plane is available
func (c *AutoImportController) isHostedControlPlaneAvailable(status hyperv1beta1.HostedClusterStatus) bool {
	for _, condition := range status.Conditions {
		if condition.Reason == hyperv1beta1.AsExpectedReason && condition.Status == metav1.ConditionTrue &&
			condition.Type == string(hyperv1beta1.HostedClusterAvailable) {
			return true
		}
	}
	return false
}

// creates managed cluster from hosted cluster
func (c *AutoImportController) createManagedCluster(hc hyperv1beta1.HostedCluster, ctx context.Context) error {
	mc := clusterv1.ManagedCluster{}
	mc.Name = hc.Name
	err := c.spokeClient.Get(ctx, types.NamespacedName{Name: mc.Name}, &mc)
	if apierrors.IsNotFound(err) {
		c.log.Info(fmt.Sprintf("creating managed cluster (%s)", mc.Name))
		populateManagedClusterData(&mc)
		if err = c.spokeClient.Create(ctx, &mc, &client.CreateOptions{}); err != nil {
			c.log.Error(err, fmt.Sprintf("failed at creating managed cluster (%s)", mc.Name))
			return err
		}
	}
	return nil
}

// populate managed cluster data for creation
func populateManagedClusterData(mc *clusterv1.ManagedCluster) {
	mc.Spec.HubAcceptsClient = true
	mc.Spec.LeaseDurationSeconds = 60
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

}

// check if acm is installed by looking for operator
func (c *AutoImportController) isACMInstalled(ctx context.Context) bool {
	listopts := &client.ListOptions{}
	operatorList := &operatorv1.OperatorList{}
	if err := c.spokeClient.List(context.TODO(), operatorList, listopts); err != nil {
		c.log.Error(err, "could not get operator list")
		return false
	}

	for _, operator := range operatorList.Items {
		if strings.HasPrefix(operator.Name, acmOperatorNamePrefix) {
			c.log.Info(fmt.Sprintf("found the ACM operator %s", operator.Name))
			return true
		}
	}
	return false
}

// populate and create klusterletaddonconfig
func (c *AutoImportController) createKlusterletAddonConfig(hcName string, ctx context.Context) error {
	kac := agentv1.KlusterletAddonConfig{ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcName}}
	kac.Spec.ClusterName = hcName
	kac.Spec.ClusterNamespace = hcName
	if kac.Spec.ClusterLabels == nil {
		kac.Spec.ClusterLabels = make(map[string]string)
	}
	kac.Spec.ClusterLabels["cloud"] = "Amazon"
	kac.Spec.ClusterLabels["vendor"] = "Openshift"

	kac.Spec.ApplicationManagerConfig.Enabled = true
	kac.Spec.SearchCollectorConfig.Enabled = true
	kac.Spec.CertPolicyControllerConfig.Enabled = true
	kac.Spec.PolicyController.Enabled = true
	kac.Spec.IAMPolicyControllerConfig.Enabled = true

	c.log.Info(fmt.Sprintf("creating KlusterletAddonConfig (%s)", hcName))

	return c.spokeClient.Create(ctx, &kac, &client.CreateOptions{})

}

// a blocking call that waits for namespace to exist until timeout
func (c *AutoImportController) waitForNamespace(namespace string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	clusterNS := &corev1.Namespace{}
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for namespace (%s) to exist", namespace)
		default:
			err := c.spokeClient.Get(ctx, types.NamespacedName{Name: namespace}, clusterNS)
			if err == nil {
				return nil
			} else if !apierrors.IsNotFound(err) {
				return err
			}
			// Sleep for a short duration before checking again
			c.log.Info(fmt.Sprintf("waiting for namespace (%s) to exist", namespace))
			time.Sleep(5 * time.Second)
		}
	}
}
