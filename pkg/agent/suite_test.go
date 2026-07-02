package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	discoveryv1 "github.com/stolostron/discovery/api/v1"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"

	"k8s.io/apimachinery/pkg/types"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	appsv1 "k8s.io/api/apps/v1"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
)

const localClusterName = "local-cluster"

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	// Controller reconciliation under envtest can take several seconds in CI.
	// Raise the default so all Eventually() calls have enough time.
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
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: k8sscheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	err = addonv1alpha1.AddToScheme(k8sscheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = appsv1.AddToScheme(k8sscheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = discoveryv1.AddToScheme(k8sscheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = hyperv1beta1.AddToScheme(k8sscheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// Register and start the Foo controller
	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: k8sscheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	err = (&AddonStatusController{
		spokeClient: k8sManager.GetClient(),
		hubClient:   k8sManager.GetClient(),
		log:         zapLogger.WithName("addon-status-controller-test"),
		addonNsn:    types.NamespacedName{Namespace: localClusterName, Name: util.AddonControllerName},
		clusterName: localClusterName,
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&DiscoveryAgent{
		spokeClient: k8sManager.GetClient(),
		hubClient:   k8sManager.GetClient(),
		log:         zapLogger.WithName("addon-status-controller-test"),
		clusterName: managedMCEClusterName,
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&HcpKubeconfigChangeWatcher{
		spokeClient: k8sManager.GetClient(),
		hubClient:   k8sManager.GetClient(),
		log:         zapLogger.WithName("hcp-kubeconfig-watcher-test"),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()

	// Wait for the manager's informer cache to complete its initial list-watch sync
	// before tests run. Without this, a test that creates an object and immediately
	// updates its status can race with the cache's initial list: if the list happens
	// after both the create and the status update, the cache receives the object in
	// its final state with no subsequent update event, so the event filter's UpdateFunc
	// is never called and the reconciler is never triggered.
	By("waiting for manager cache to sync")
	syncCtx, syncCancel := context.WithTimeout(ctx, 30*time.Second)
	defer syncCancel()
	Expect(k8sManager.GetCache().WaitForCacheSync(syncCtx)).To(BeTrue(), "manager cache should sync within 30s")
})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
