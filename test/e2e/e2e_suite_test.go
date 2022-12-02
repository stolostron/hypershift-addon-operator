package e2e_test

import (
	"context"
	"os"
	"testing"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	addonv1alpha1client "open-cluster-management.io/api/client/addon/clientset/versioned"

	"github.com/stolostron/hypershift-addon-operator/test/e2e/util"
)

func TestE2e(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "E2e Suite")
}

const (
	eventuallyTimeout  = 300
	eventuallyInterval = 2
)

var (
	dynamicClient           dynamic.Interface
	kubeClient              kubernetes.Interface
	addonClient             addonv1alpha1client.Interface
	defaultManagedCluster   string
	defaultInstallNamespace string
	isOcp                   = false
)

// This suite is sensitive to the following environment variables:
//
// - KUBECONFIG is the location of the kubeconfig file to use
// - MANAGED_CLUSTER_NAME is the name of managed cluster that is deployed by registration-operator
var _ = ginkgo.BeforeSuite(func() {
	var err error

	defaultManagedCluster = os.Getenv("MANAGED_CLUSTER_NAME")
	if defaultManagedCluster == "" {
		defaultManagedCluster = "local-cluster"
	}

	defaultInstallNamespace = "open-cluster-management-agent-addon"

	dynamicClient, err = util.NewDynamicClient()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	kubeClient, err = util.NewKubeClient()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	cfg, err := util.NewKubeConfig()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	addonClient, err = addonv1alpha1client.NewForConfig(cfg)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	ginkgo.By("Check the addon manager was installed")
	gomega.Eventually(func() error {
		_, err = kubeClient.AppsV1().Deployments("multicluster-engine").Get(context.TODO(), "hypershift-addon-manager", metav1.GetOptions{})
		return err
	}, eventuallyTimeout, eventuallyInterval).ShouldNot(gomega.HaveOccurred())

	ginkgo.By("Check if the managed cluster is OCP")
	gomega.Eventually(func() error {
		_, err := util.GetResource(dynamicClient, util.InfrastructuresGVR, "", "cluster")
		if err != nil {
			return err
		}
		isOcp = true
		return nil
	}, eventuallyTimeout, eventuallyInterval).ShouldNot(gomega.HaveOccurred())

	_, err = kubeClient.CoreV1().Namespaces().Get(context.TODO(), defaultInstallNamespace, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: defaultInstallNamespace,
			},
		}
		_, err = kubeClient.CoreV1().Namespaces().Create(context.TODO(), namespace, metav1.CreateOptions{})
		gomega.Expect(err).ToNot(gomega.HaveOccurred())
	}
})
