package agent

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const testHcNamespace = "hc-test-1"
const testHcName = "hc-test-1"
const adminKubeconfigSecret = testHcName + "-admin-kubeconfig"

var _ = Describe("Hosted cluster kubeconfig secret change watcher", Ordered, func() {
	ctx := context.Background()

	BeforeAll(func() {
		ctx = context.TODO()
		By("Create the hosted cluster namespace")
		hcNs := corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testHcNamespace,
			},
		}
		Expect(k8sClient.Create(ctx, &hcNs)).Should(Succeed())

		By("creating a hosted cluster")

		hc := getHostedCluster(types.NamespacedName{Namespace: testHcNamespace, Name: testHcName})
		hsStatus := &hyperv1beta1.HostedClusterStatus{
			KubeConfig: &corev1.LocalObjectReference{Name: "kubeconfig"},
			Conditions: []metav1.Condition{{Type: string(hyperv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue, Reason: hyperv1beta1.AsExpectedReason}},
			Version: &hyperv1beta1.ClusterVersionStatus{
				History: []configv1.UpdateHistory{{State: configv1.CompletedUpdate}},
			},
		}
		hc.Status = *hsStatus
		Expect(k8sClient.Create(ctx, hc)).Should(Succeed())
	})

	Context("When the hosted cluster admin kubeconfig secret is created", func() {
		It("Should not do add the annotation to the hosted cluster", func() {

			By("creating the admin kubeconfig")

			kubeconfig := getAdminKubeconfigSecret(types.NamespacedName{Namespace: testHcNamespace, Name: adminKubeconfigSecret})
			Expect(k8sClient.Create(ctx, kubeconfig)).Should(Succeed())

			time.Sleep(time.Second * 5)

			hostedCluster := &hyperv1beta1.HostedCluster{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testHcNamespace, Name: testHcName}, hostedCluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(hostedCluster.Annotations).To(BeNil())
		})
	})

	Context("When the hosted cluster admin kubeconfig secret is deleted", func() {
		It("Should not do add the annotation to the hosted cluster", func() {

			By("deleting the admin kubeconfig")

			kubeconfig := &corev1.Secret{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testHcNamespace, Name: adminKubeconfigSecret}, kubeconfig)
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Delete(ctx, kubeconfig)).Should(Succeed())

			time.Sleep(time.Second * 5)

			hostedCluster := &hyperv1beta1.HostedCluster{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: testHcNamespace, Name: testHcName}, hostedCluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(hostedCluster.Annotations).To(BeNil())
		})
	})

	Context("When the hosted cluster admin kubeconfig secret is updated", func() {
		It("Should add the annotation to the hosted cluster", func() {

			By("creating the admin kubeconfig")

			kubeconfig := getAdminKubeconfigSecret(types.NamespacedName{Namespace: testHcNamespace, Name: adminKubeconfigSecret})
			Expect(k8sClient.Create(ctx, kubeconfig)).Should(Succeed())

			time.Sleep(time.Second * 5)

			hostedCluster := &hyperv1beta1.HostedCluster{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testHcNamespace, Name: testHcName}, hostedCluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(hostedCluster.Annotations).To(BeNil())

			newKubeconfig := &corev1.Secret{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: testHcNamespace, Name: adminKubeconfigSecret}, newKubeconfig)
			Expect(err).NotTo(HaveOccurred())

			newKubeconfig.Data = map[string][]byte{
				"kubeadmin": []byte("newkubeconfig"),
			}
			Expect(k8sClient.Update(ctx, newKubeconfig)).Should(Succeed())

			Eventually(func() string {
				if err := k8sClient.Get(ctx,
					types.NamespacedName{Namespace: testHcNamespace, Name: testHcName},
					hostedCluster); err != nil {
					return ""
				}
				return hostedCluster.Annotations[hcAnnotation]
			}).WithTimeout(10 * time.Second).ShouldNot(Equal(""))
		})
	})
})

func getAdminKubeconfigSecret(secretNN types.NamespacedName) *corev1.Secret {
	hostedCluster := &hyperv1beta1.HostedCluster{}
	err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testHcNamespace, Name: testHcName}, hostedCluster)
	Expect(err).NotTo(HaveOccurred())

	hc := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretNN.Name,
			Namespace: secretNN.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "hypershift.openshift.io/v1beta1",
					Kind:       "HostedCluster",
					Name:       testHcName,
					UID:        hostedCluster.UID,
				},
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"kubeadmin": []byte("test"),
		},
	}
	return hc
}
