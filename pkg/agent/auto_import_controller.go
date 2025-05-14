package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	operatorv1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	agentv1 "github.com/stolostron/klusterlet-addon-controller/pkg/apis/agent/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
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
	autoDetect                = "auto-detect"
)

type AutoImportController struct {
	hubClient        client.Client
	spokeClient      client.Client
	clusterName      string
	localClusterName string
	log              logr.Logger
}

// SetupWithManager sets up the controller with the Manager.
func (c *AutoImportController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(util.AutoImportControllerName).
		For(&hyperv1beta1.HostedCluster{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		WithEventFilter(hostedClusterEventFilters(true)).
		Complete(c)
}

func (c *AutoImportController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// skip auto import if disabled
	if strings.EqualFold(os.Getenv("DISABLE_AUTO_IMPORT"), "true") {
		c.log.Info("auto import is disabled, skip auto importing")
		return ctrl.Result{}, nil
	}

	// if this agent is not for self managed cluster aka local-cluster, skip auto-import
	if !strings.EqualFold(c.clusterName, c.localClusterName) {
		c.log.Info("this is local cluster agent, skip discovering")
		return ctrl.Result{}, nil
	}

	hc := &hyperv1beta1.HostedCluster{}
	if err := c.spokeClient.Get(ctx, req.NamespacedName, hc); err != nil {
		c.log.Error(err, fmt.Sprintf("failed to get the hostedcluster (%s/%s)",
			req.Name, req.Namespace))
		return ctrl.Result{}, nil
	}

	// check if controlplane is available, if not then requeue until it is
	if hc.Status.Conditions == nil || len(hc.Status.Conditions) == 0 || !isHostedControlPlaneAvailable(*hc) {
		c.log.Info(fmt.Sprintf("hostedcluster %s's control plane is not ready yet.", hc.Name))
		return ctrl.Result{}, nil
	}

	// if the hosted cluster is being deleted, ignore the event.
	if !hc.GetDeletionTimestamp().IsZero() {
		c.log.Info(fmt.Sprintf("hostedcluster %s is being deleted.", hc.Name))
		return ctrl.Result{}, nil
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

// creates managed cluster from hosted cluster
func (c *AutoImportController) createManagedCluster(hc hyperv1beta1.HostedCluster, ctx context.Context) error {
	mc := clusterv1.ManagedCluster{}
	mc.Name = hc.Name
	err := c.spokeClient.Get(ctx, types.NamespacedName{Name: mc.Name}, &mc)
	if apierrors.IsNotFound(err) {
		c.log.Info(fmt.Sprintf("creating managed cluster (%s)", mc.Name))
		populateManagedClusterData(&mc, &hc, c.localClusterName)
		if err = c.spokeClient.Create(ctx, &mc, &client.CreateOptions{}); err != nil {
			c.log.Error(err, fmt.Sprintf("failed at creating managed cluster (%s)", mc.Name))
			return err
		}
	}
	return nil
}

// populate managed cluster data for creation
func populateManagedClusterData(mc *clusterv1.ManagedCluster, hc *hyperv1beta1.HostedCluster, hostingClusterName string) {
	mc.Spec.HubAcceptsClient = true
	mc.Spec.LeaseDurationSeconds = 60
	if mc.Labels == nil {
		mc.Labels = make(map[string]string)
	}
	labels := map[string]string{
		"name":          mc.Name,
		"vendor":        autoDetect,
		"cloud":         autoDetect, // Work addon will use this to detect cloud provider, like: GCP,AWS
		clusterSetLabel: "default",
	}

	// sync HostedCluster labels to ManagedCluster. This allows for people to
	// influence how addons are installed into the HCP cluster through
	// the HostedCluster object.
	for key, value := range hc.Labels {
		if v, ok := mc.Labels[key]; !ok || len(v) == 0 {
			mc.Labels[key] = value
		}
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
		hostingClusterNameAnno: hostingClusterName,
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
	kac.Spec.ClusterLabels["cloud"] = autoDetect
	kac.Spec.ClusterLabels["vendor"] = autoDetect

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
