package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	corev1informers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	corev1lister "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"

	"open-cluster-management.io/addon-framework/pkg/lease"
	"open-cluster-management.io/addon-framework/pkg/version"
	addonv1alpha1client "open-cluster-management.io/api/client/addon/clientset/versioned"
)

type syncFuncName string

const (
	// addOnAgentInstallationNamespace is the namespace on the managed cluster to install the addon agent.
	AgentInstallationNamespace              = "default"
	addonAgentName                          = "hypershift-addon-agent-controller"
	FromHub                    syncFuncName = "fromHub"
	ToHub                      syncFuncName = "toHub"

	hypershiftSecretAnnotationKey = "hypershift.openshift.io/cluster"

	hypershiftBucketSecretName  = "hypershift-operator-oidc-provider-s3-credentials"
	hypershiftOperatorNamespace = "hypershift"
)

func NewAgentCommand(addonName string, logger logr.Logger) *cobra.Command {
	o := NewAgentOptions(addonName, logger)
	cmd := controllercmd.
		NewControllerCommandConfig(addonName, version.Get(), o.runSecretSyncAgent).
		NewCommand()
	cmd.Use = "agent"
	cmd.Short = fmt.Sprintf("Start the %s's agent", addonName)

	o.AddFlags(cmd)
	return cmd
}

// AgentOptions defines the flags for workload agent
type AgentOptions struct {
	Log               logr.Logger
	HubKubeconfigFile string
	SpokeClusterName  string
	AddonName         string
	AddonNamespace    string
}

// NewWorkloadAgentOptions returns the flags with default value set
func NewAgentOptions(addonName string, logger logr.Logger) *AgentOptions {
	return &AgentOptions{AddonName: addonName, Log: logger}
}

func (o *AgentOptions) AddFlags(cmd *cobra.Command) {
	flags := cmd.Flags()
	// This command only supports reading from config
	flags.StringVar(&o.HubKubeconfigFile, "hub-kubeconfig", o.HubKubeconfigFile, "Location of kubeconfig file to connect to hub cluster.")
	flags.StringVar(&o.SpokeClusterName, "cluster-name", o.SpokeClusterName, "Name of spoke cluster.")
	flags.StringVar(&o.AddonNamespace, "addon-namespace", o.AddonNamespace, "Installation namespace of addon.")
}

// RunAgent starts the controllers on agent to process work from hub.
func (o *AgentOptions) runSecretSyncAgent(ctx context.Context, controllerContext *controllercmd.ControllerContext) error {
	// build kubeclient of managed cluster
	spokeKubeClient, err := kubernetes.NewForConfig(controllerContext.KubeConfig)
	if err != nil {
		return err
	}

	// build kubeinformerfactory of hub cluster
	hubRestConfig, err := clientcmd.BuildConfigFromFlags("" /* leave masterurl as empty */, o.HubKubeconfigFile)
	if err != nil {
		return err
	}

	hubKubeClient, err := kubernetes.NewForConfig(hubRestConfig)
	if err != nil {
		return err
	}

	addonClient, err := addonv1alpha1client.NewForConfig(hubRestConfig)
	if err != nil {
		return err
	}

	spokeInformerFactory := informers.NewSharedInformerFactoryWithOptions(
		spokeKubeClient,
		10*time.Minute,
	)

	// create an agent contoller
	agent := newAgentController(
		hubKubeClient,
		addonClient,
		spokeInformerFactory.Core().V1().Secrets(),
		o.SpokeClusterName,
		o.AddonName,
		o.AddonNamespace,
		controllerContext.EventRecorder,
		o.Log,
	)

	// create a lease updater
	leaseUpdater := lease.NewLeaseUpdater(
		spokeKubeClient,
		o.AddonName,
		o.AddonNamespace,
	)

	go spokeInformerFactory.Start(ctx.Done())
	go agent.Run(ctx, 1)

	go leaseUpdater.Start(ctx)

	<-ctx.Done()
	return nil
}

type agentController struct {
	hubKubeClient  kubernetes.Interface
	addonClient    addonv1alpha1client.Interface
	lister         corev1lister.SecretLister
	clusterName    string
	addonName      string
	addonNamespace string
	recorder       events.Recorder
	log            logr.Logger
}

func newAgentController(
	hubKubeClient kubernetes.Interface,
	addonClient addonv1alpha1client.Interface,
	secretInformers corev1informers.SecretInformer,
	clusterName string,
	addonName string,
	addonNamespace string,
	recorder events.Recorder,
	loggr logr.Logger,

) factory.Controller {
	c := &agentController{
		hubKubeClient:  hubKubeClient,
		addonClient:    addonClient,
		clusterName:    clusterName,
		addonName:      addonName,
		addonNamespace: addonNamespace,
		lister:         secretInformers.Lister(),
		recorder:       recorder,
		log:            loggr,
	}

	keyF := func(obj runtime.Object) string {
		metaObj, err := apimeta.Accessor(obj)
		if err != nil {
			c.log.Error(err, "failed to run Accessor")
			return ""
		}

		an := metaObj.GetAnnotations()

		if len(an) == 0 || len(an[hypershiftSecretAnnotationKey]) == 0 {
			return ""
		}

		key, _ := cache.MetaNamespaceKeyFunc(obj)
		return key
	}

	return factory.New().WithInformersQueueKeyFunc(
		keyF, secretInformers.Informer()).
		WithSync(c.sync).ToController(addonAgentName, recorder)
}

func (c *agentController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	key := syncCtx.QueueKey()
	c.log.Info(fmt.Sprintf("Reconciling addon deploy %q", key))

	ns, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		// ignore addon whose key is not in format: namespace/name
		return nil
	}

	se, err := c.lister.Secrets(ns).Get(name)

	switch {
	case errors.IsNotFound(err):
		return nil
	case err != nil:
		return err
	}

	addon, err := c.addonClient.AddonV1alpha1().ManagedClusterAddOns(c.clusterName).Get(ctx, c.addonName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if !addon.DeletionTimestamp.IsZero() {
		return nil
	}

	seTmp := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      se.Name,
			Namespace: c.clusterName,
			Labels:    map[string]string{"synced-from-spoke": ""},
		},
		Data: se.Data,
	}

	_, _, err = resourceapply.ApplySecret(ctx, c.hubKubeClient.CoreV1(), c.recorder, seTmp)
	return err
}
