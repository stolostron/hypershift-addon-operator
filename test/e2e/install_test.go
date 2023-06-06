package e2e_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

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
	ginkgo.By(fmt.Sprintf("Create hypershift managed cluster addon for %s, installNamespace:%s", namespace, installNamespace))
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

func getPodLogs(pod *corev1.Pod, container string) (string, error) {
	logOption := &corev1.PodLogOptions{}
	if container != "" {
		logOption = &corev1.PodLogOptions{Container: "hypershift-addon-agent"}
	}
	podLogs := kubeClient.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, logOption)
	r, err := podLogs.Stream(context.TODO())

	if err == nil {
		defer r.Close()

		buf := new(bytes.Buffer)
		_, _ = io.Copy(buf, r)
		return buf.String(), nil
	} else {
		return "", err
	}
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
					ginkgo.By("Addon agent not found, checking manager logs")

					var addonManagerPod *corev1.Pod
					podList, err := kubeClient.CoreV1().Pods("multicluster-engine").List(ctx, metav1.ListOptions{})
					if err != nil {
						ginkgo.By("Error getting addon manager pods: " + err.Error())
						return false
					}

					for _, p := range podList.Items {
						if strings.HasPrefix(p.Name, "hypershift-addon-manager") {
							addonManagerPod = &p
							ginkgo.By("Found addon manager pod" + p.Name)

							break
						}
					}

					if addonManagerPod != nil {
						log, err := getPodLogs(addonManagerPod, "")
						if err != nil {
							ginkgo.By(fmt.Sprintf("Error reading pod logs: %v", err.Error()))
						} else {
							ginkgo.By(fmt.Sprintf("Addon manager logs: %v", string(log)))
						}
					}

					return false
				}

				ginkgo.By(fmt.Sprintf("Addon deployment: %v", deployment.String()))
				if deployment.Status.AvailableReplicas <= 0 {

					var addonAgentPod *corev1.Pod
					podList, err := kubeClient.CoreV1().Pods(defaultInstallNamespace).List(ctx, metav1.ListOptions{})
					if err != nil {
						ginkgo.By("Error getting addon agent pods: " + err.Error())
						return false
					}

					for _, p := range podList.Items {
						if strings.HasPrefix(p.Name, "hypershift-addon-agent") {
							addonAgentPod = &p
							ginkgo.By("Found addon agent pod" + p.Name)

							break
						}
					}

					if addonAgentPod != nil {
						log, err := getPodLogs(addonAgentPod, "hypershift-addon-agent")
						if err != nil {
							ginkgo.By(fmt.Sprintf("Error reading agent pod logs: %v", err.Error()))
						} else {
							ginkgo.By(fmt.Sprintf("Addon agent logs: %v", string(log)))
						}
					}

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

					var addonAgentPod *corev1.Pod
					podList, err := kubeClient.CoreV1().Pods(defaultInstallNamespace).List(ctx, metav1.ListOptions{})
					if err != nil {
						ginkgo.By("Error getting addon agent pods: " + err.Error())
						return false
					}

					for _, p := range podList.Items {
						if strings.HasPrefix(p.Name, "hypershift-addon-agent") {
							addonAgentPod = &p
							ginkgo.By("Found addon agent pod" + p.Name)

							break
						}
					}

					if addonAgentPod != nil {
						log, err := getPodLogs(addonAgentPod, "hypershift-addon-agent")
						if err != nil {
							ginkgo.By(fmt.Sprintf("Error reading agent pod logs: %v", err.Error()))
						} else {
							ginkgo.By(fmt.Sprintf("Addon agent logs: %v", string(log)))
						}
					}

					return false
				}

				return true
			}, eventuallyTimeout, eventuallyInterval).Should(gomega.BeTrue())

		})
	})

})
