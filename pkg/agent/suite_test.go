package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	discoveryv1 "github.com/stolostron/discovery/api/v1"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"

	"k8s.io/apimachinery/pkg/types"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
)

const localClusterName = "local-cluster"

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc

	addonStatusController    *AddonStatusController
	discoveryAgentController *DiscoveryAgent
	hcpKubeconfigWatcher     *HcpKubeconfigChangeWatcher
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(30 * time.Second)
	SetDefaultEventuallyPollingInterval(100 * time.Millisecond)

	zapLogger := zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true))
	logf.SetLogger(zapLogger)
	ctx, cancel = context.WithCancel(context.TODO())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "hack", "crds")},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = addonv1alpha1.AddToScheme(k8sscheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = appsv1.AddToScheme(k8sscheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = corev1.AddToScheme(k8sscheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = metav1.AddMetaToScheme(k8sscheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = discoveryv1.AddToScheme(k8sscheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = hyperv1beta1.AddToScheme(k8sscheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = configv1.AddToScheme(k8sscheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: k8sscheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	addonStatusController = &AddonStatusController{
		spokeClient: k8sClient,
		hubClient:   k8sClient,
		log:         zapLogger.WithName("addon-status-controller-test"),
		addonNsn:    types.NamespacedName{Namespace: localClusterName, Name: util.AddonControllerName},
		clusterName: localClusterName,
	}
	discoveryAgentController = &DiscoveryAgent{
		spokeClient: k8sClient,
		hubClient:   k8sClient,
		log:         zapLogger.WithName("discovery-agent-test"),
		clusterName: managedMCEClusterName,
	}
	hcpKubeconfigWatcher = &HcpKubeconfigChangeWatcher{
		spokeClient: k8sClient,
		hubClient:   k8sClient,
		log:         zapLogger.WithName("hcp-kubeconfig-watcher-test"),
	}
})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
