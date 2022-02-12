package agent

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/go-logr/logr"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/spf13/cobra"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"

	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

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

const (
	// addOnAgentInstallationNamespace is the namespace on the managed cluster to install the addon agent.
	AgentInstallationNamespace = "default"
	addonAgentName             = "hypershift-addon-agent"

	hypershiftSecretAnnotationKey = "hypershift.openshift.io/cluster"

	hypershiftBucketSecretName = "hypershift-operator-oidc-provider-s3-credentials"
)

func NewAgentCommand(addonName string, logger logr.Logger) *cobra.Command {
	o := NewAgentOptions(addonName, logger)

	cmd := controllercmd.
		NewControllerCommandConfig(addonName, version.Get(), o.runAgent).
		NewCommand()
	cmd.Use = "agent"
	cmd.Short = fmt.Sprintf("Start the %s's agent", addonName)

	o.AddFlags(cmd)
	return cmd
}

// AgentOptions defines the flags for workload agent
type AgentOptions struct {
	Log                   logr.Logger
	HubKubeconfigFile     string
	SpokeClusterName      string
	AddonName             string
	AddonNamespace        string
	BucketSecretNamespace string
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
	flags.StringVar(&o.BucketSecretNamespace, "hypershfit-bucket-namespace", o.BucketSecretNamespace, "Namespace that holds the hypershift bucket")
}

// RunAgent starts the controllers on agent to process work from hub.
func (o *AgentOptions) runAgent(ctx context.Context, controllerContext *controllercmd.ControllerContext) error {
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
	controllerObj, secretSyncAgentController := newAgentController(
		hubKubeClient,
		spokeKubeClient,
		addonClient,
		spokeInformerFactory.Core().V1().Secrets(),
		controllerContext.EventRecorder,
		o,
	)

	// create a lease updater
	leaseUpdater := lease.NewLeaseUpdater(
		spokeKubeClient,
		o.AddonName,
		o.AddonNamespace,
	)

	go spokeInformerFactory.Start(ctx.Done())

	go secretSyncAgentController.Run(ctx, 1)

	// retry 5 times, in case something wrong with creating the hypershift install job
	go retry(5, time.Second*10, controllerObj.runHypershiftInstallJob)

	go leaseUpdater.Start(ctx)

	<-ctx.Done()
	return nil
}

type agentController struct {
	hubKubeClient         kubernetes.Interface
	spokeKubeClient       kubernetes.Interface
	addonClient           addonv1alpha1client.Interface
	lister                corev1lister.SecretLister
	recorder              events.Recorder
	log                   logr.Logger
	clusterName           string
	addonName             string
	addonNamespace        string
	bucketSecretNamespace string
}

func newAgentController(
	hubKubeClient kubernetes.Interface,
	spokeKubeClient kubernetes.Interface,
	addonClient addonv1alpha1client.Interface,
	secretInformers corev1informers.SecretInformer,
	recorder events.Recorder,
	agentOption *AgentOptions,
) (*agentController, factory.Controller) {
	c := &agentController{
		hubKubeClient:   hubKubeClient,
		spokeKubeClient: spokeKubeClient,
		addonClient:     addonClient,
		lister:          secretInformers.Lister(),
		recorder:        recorder,
		clusterName:     agentOption.SpokeClusterName,
		addonName:       agentOption.AddonName,
		addonNamespace:  agentOption.AddonNamespace,

		bucketSecretNamespace: agentOption.BucketSecretNamespace,
		log:                   agentOption.Log,
	}

	keyF := func(obj runtime.Object) string {
		key, _ := cache.MetaNamespaceKeyFunc(obj)
		return key
	}

	filter := func(obj interface{}) bool {
		metaObj, ok := obj.(metav1.ObjectMetaAccessor)
		if !ok {
			c.log.Error(fmt.Errorf("failed to run Accessor"), "")
			return false
		}

		an := metaObj.GetObjectMeta().GetAnnotations()

		if len(an) == 0 || len(an[hypershiftSecretAnnotationKey]) == 0 {
			return false
		}

		return true
	}

	return c, factory.New().WithFilteredEventsInformersQueueKeyFunc(
		keyF, filter, secretInformers.Informer()).
		WithSync(c.sync).ToController(addonAgentName, recorder)
}

func (c *agentController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	key := syncCtx.QueueKey()
	c.log.Info(fmt.Sprintf("Reconciling addon deploy %q", key))
	defer c.log.Info(fmt.Sprintf("Done reconcile addon deploy %q", key))

	ns, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		// ignore addon whose key is not in format: namespace/name
		return nil
	}

	se, err := c.lister.Secrets(ns).Get(name)

	switch {
	case apierrors.IsNotFound(err):
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
			Labels:    map[string]string{"synced-from-spoke": "true"},
		},
		Data: se.Data,
	}

	_, _, err = resourceapply.ApplySecret(ctx, c.hubKubeClient.CoreV1(), c.recorder, seTmp)
	return err
}

func retry(attempts int, sleep time.Duration, f func() error) {
	if err := f(); err != nil {
		if attempts--; attempts > 0 {
			// Add some randomness to prevent creating a Thundering Herd
			jitter := time.Duration(rand.Int63n(int64(sleep)))
			sleep = sleep + jitter/2

			time.Sleep(sleep)

			retry(attempts, 2*sleep, f)
			return
		}

		return
	}

	return
}

func (c *agentController) runHypershiftInstallJob() error {
	c.log.Info("entry runHypershiftInstallJob")
	defer c.log.Info("exit runHypershiftInstallJob")

	ctx := context.TODO()
	mgmAddon, err := c.addonClient.AddonV1alpha1().ManagedClusterAddOns(c.clusterName).Get(ctx, c.addonName, metav1.GetOptions{})
	if err != nil {
		c.log.Error(err, "failed to get managedclusterAddon CR from hub")
		return nil
	}

	if !mgmAddon.DeletionTimestamp.IsZero() {
		return nil
	}

	se, err := c.hubKubeClient.CoreV1().Secrets(c.bucketSecretNamespace).Get(ctx, hypershiftBucketSecretName, metav1.GetOptions{})
	if err != nil {
		c.log.Error(err, fmt.Sprintf("failed to get bucket secret from hub at %s/%s", c.clusterName, hypershiftBucketSecretName))

		return err
	}

	out := func(in *corev1.Secret) *corev1.Secret {
		out := &corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Secret",
				APIVersion: corev1.SchemeGroupVersion.String(),
			},
		}

		out.SetName(in.GetName())
		out.SetNamespace(c.addonNamespace)
		out.Data = in.Data

		return out
	}(se)

	_, err = c.spokeKubeClient.CoreV1().Secrets(c.addonNamespace).Create(ctx, out, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		c.log.Error(err, "failed to create bucket's secret on spoke cluster")
		return nil
	}

	awsMountPath := "/var/.aws"
	secretAWSCredKey := "credentials"

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-hypershift-install-job", c.addonName),
			Namespace: c.addonNamespace,
		},

		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-hypershift-install-pod", c.addonName),
					Namespace: c.addonNamespace,
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyOnFailure,
					Volumes: []corev1.Volume{
						corev1.Volume{
							Name: hypershiftBucketSecretName,
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: hypershiftBucketSecretName,
									Items: []corev1.KeyToPath{
										corev1.KeyToPath{Key: secretAWSCredKey, Path: secretAWSCredKey},
									},
								},
							},
						},
					},
					Containers: []corev1.Container{
						corev1.Container{
							Name:    "hypershift-installer",
							Image:   util.DefaultHypershiftImage,
							Command: []string{"/bin/sh"},
							Args:    []string{"-c", "sleep 600;"},
							VolumeMounts: []corev1.VolumeMount{
								corev1.VolumeMount{
									Name:      hypershiftBucketSecretName,
									ReadOnly:  true,
									MountPath: awsMountPath,
								},
							},
							Env: []corev1.EnvVar{
								corev1.EnvVar{
									Name: "BUCKET_NAME",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{Name: hypershiftBucketSecretName},
											Key:                  "bucket",
										},
									},
								},
								corev1.EnvVar{
									Name: "REGION",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{Name: hypershiftBucketSecretName},
											Key:                  "region",
										},
									},
								},

								corev1.EnvVar{
									Name:  "AWS_CREDS",
									Value: fmt.Sprintf("%s/%s", awsMountPath, secretAWSCredKey),
								},
							},
						},
					},
				},
			},
		},
	}

	_, err = c.spokeKubeClient.BatchV1().Jobs(c.addonNamespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		c.log.Error(err, "failed to run the hypershift install job")
		return nil
	}

	return nil
}
