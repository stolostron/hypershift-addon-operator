package e2e_test

import (
	"context"
	"fmt"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	addonapi "open-cluster-management.io/api/addon/v1alpha1"
	addonv1alpha1client "open-cluster-management.io/api/client/addon/clientset/versioned"

	"github.com/stolostron/hypershift-addon-operator/pkg/util"
)

func createHypershiftAddon(ctx context.Context, client addonv1alpha1client.Interface, namespace, installNamespace string) error {
	ginkgo.By(fmt.Sprintf("Create hypershift managed cluster addon for %s", namespace))
	addon := &addonapi.ManagedClusterAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon",
			Namespace: namespace,
		},
		Spec: addonapi.ManagedClusterAddOnSpec{
			InstallNamespace: installNamespace,
		},
	}

	_, err := client.AddonV1alpha1().ManagedClusterAddOns(namespace).Create(ctx, addon, metav1.CreateOptions{})
	return err
}

func deleteHypershiftAddon(ctx context.Context, client addonv1alpha1client.Interface, namespace string) error {
	ginkgo.By(fmt.Sprintf("Delete hypershift managed cluster addon for %s", namespace))
	return client.AddonV1alpha1().ManagedClusterAddOns(namespace).Delete(ctx, "hypershift-addon", metav1.DeleteOptions{})
}

func createOIDCProviderSecret(ctx context.Context, client kubernetes.Interface, namespace string) error {
	ginkgo.By(fmt.Sprintf("Create hypershift OIDC provider secret for %s", namespace))
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "hypershift-operator-oidc-provider-s3-credentials",
		},
		Data: map[string][]byte{
			"bucket": []byte("bucket"),
			"credentials": []byte(`[default]
			aws_access_key_id     = ABCDEFGHIJKLMNOPQRST
			aws_secret_access_key = ABCDEFGHIJKLMNOPQRSTUVWXYZabcd1234567890`),
			"region": []byte("us-east-1"),
		},
	}

	_, err := client.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	return err
}

func deleteOIDCProviderSecret(ctx context.Context, client kubernetes.Interface, namespace string) error {
	ginkgo.By(fmt.Sprintf("Delete  hypershift OIDC provider secret for %s", namespace))
	return client.CoreV1().Secrets(namespace).Delete(ctx, "hypershift-operator-oidc-provider-s3-credentials", metav1.DeleteOptions{})
}

var _ = ginkgo.Describe("Install", func() {
	var ctx context.Context
	ginkgo.BeforeEach(func() {
		ctx = context.TODO()
		err := createOIDCProviderSecret(ctx, kubeClient, defaultManagedCluster)
		if err != nil {
			gomega.Expect(apierrors.IsAlreadyExists(err)).Should(gomega.BeTrue())
		} else {
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
		}
	})

	ginkgo.AfterEach(func() {
		err := deleteOIDCProviderSecret(ctx, kubeClient, defaultManagedCluster)
		gomega.Expect(err).ToNot(gomega.HaveOccurred())
	})

	ginkgo.Context("install hypershift operator", func() {
		ginkgo.BeforeEach(func() {
			err := createHypershiftAddon(ctx, addonClient, defaultManagedCluster, defaultInstallNamespace)
			if err != nil {
				gomega.Expect(apierrors.IsAlreadyExists(err)).Should(gomega.BeTrue())
			} else {
				gomega.Expect(err).ToNot(gomega.HaveOccurred())
			}
		})

		ginkgo.AfterEach(func() {
			// set deleting addon here to ensure this can be executed even if the test failed
			err := deleteHypershiftAddon(ctx, addonClient, defaultManagedCluster)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			ginkgo.By("Check the hypershift operator deletion")
			gomega.Eventually(func() bool {
				_, err := kubeClient.AppsV1().Deployments(util.HypershiftOperatorNamespace).Get(ctx, util.HypershiftOperatorName, metav1.GetOptions{})
				return apierrors.IsNotFound(err)
			}, eventuallyTimeout, eventuallyInterval).Should(gomega.BeTrue())

			ginkgo.By("Check the addon agent deletion")
			gomega.Eventually(func() bool {
				_, err := kubeClient.AppsV1().Deployments(defaultInstallNamespace).Get(ctx, util.AgentDeploymentName, metav1.GetOptions{})
				return apierrors.IsNotFound(err)
			}, eventuallyTimeout, eventuallyInterval).Should(gomega.BeTrue())
		})

		ginkgo.It("did not exist", func() {
			ginkgo.By("Check the addon agent installation")
			gomega.Eventually(func() bool {
				deployment, err := kubeClient.AppsV1().Deployments(defaultInstallNamespace).Get(ctx, util.AgentDeploymentName, metav1.GetOptions{})
				if err != nil {
					return false
				}

				if deployment.Status.AvailableReplicas <= 0 {
					return false
				}

				return true
			}, eventuallyTimeout, eventuallyInterval).Should(gomega.BeTrue())

			ginkgo.By("Check the hypershift operator installation")
			gomega.Eventually(func() bool {
				deployment, err := kubeClient.AppsV1().Deployments(util.HypershiftOperatorNamespace).Get(ctx, util.HypershiftOperatorName, metav1.GetOptions{})
				if err != nil {
					return false
				}

				if deployment.Status.AvailableReplicas <= 0 {
					return false
				}

				return true
			}, eventuallyTimeout, eventuallyInterval).Should(gomega.BeTrue())

		})
	})

})
