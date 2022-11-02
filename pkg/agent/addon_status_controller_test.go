package agent

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
)

var _ = Describe("Hypershift ManagedClusterAddon Status controller", func() {
	ctx := context.Background()
	Context("When Hypershift operator deployment is created/updated/deleted", func() {
		It("Should update Hypershift ManagedClusterAddon Status", func() {
			By("Creating the hypershift namespace")
			hypershiftNs := corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: util.HypershiftOperatorNamespace,
				},
			}
			Expect(k8sClient.Create(ctx, &hypershiftNs)).Should(Succeed())

			By("Creating the local-cluster ManagedCluster namespace")
			managedClusterNs := corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: localClusterName,
				},
			}
			Expect(k8sClient.Create(ctx, &managedClusterNs)).Should(Succeed())

			By("Creating the Hypershift ManagedClusterAddon")
			addon := addonv1alpha1.ManagedClusterAddOn{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: localClusterName,
					Name:      util.AddonControllerName,
				},
			}
			Expect(k8sClient.Create(ctx, &addon)).Should(Succeed())

			By("Creating the Hypershift operator deployment")
			operatorDeployment := appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      util.HypershiftOperatorName,
					Namespace: util.HypershiftOperatorNamespace,
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "hypershift"}},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": "hypershift"},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: "hypershift", Image: "nginx:1.14.2"}}, // fake deployment
						},
					},
				},
			}
			operatorDeployment.Spec.Replicas = new(int32)
			*operatorDeployment.Spec.Replicas = 1
			Expect(k8sClient.Create(ctx, &operatorDeployment)).Should(Succeed())

			operatorDeployment = appsv1.Deployment{}
			Expect(k8sClient.Get(ctx,
				types.NamespacedName{Namespace: util.HypershiftOperatorNamespace, Name: util.HypershiftOperatorName},
				&operatorDeployment)).Should(Succeed())

			operatorDeployment.Status.AvailableReplicas = 1
			operatorDeployment.Status.ReadyReplicas = 1
			operatorDeployment.Status.Replicas = 1
			Expect(k8sClient.Status().Update(ctx, &operatorDeployment)).Should(Succeed())

			addon = addonv1alpha1.ManagedClusterAddOn{}
			Eventually(func() bool {
				if err := k8sClient.Get(ctx,
					types.NamespacedName{Namespace: localClusterName, Name: util.AddonControllerName},
					&addon); err != nil {
					return false
				}
				if len(addon.Status.Conditions) == 0 {
					return false
				}
				return addon.Status.Conditions[0].Reason == degradedReasonHypershiftDeployed
			}).Should(BeTrue())

			By("Creating the external dns secret")
			secret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      util.HypershiftExternalDNSSecretName,
					Namespace: localClusterName,
				},
			}
			Expect(k8sClient.Create(ctx, &secret)).Should(Succeed())

			operatorDeployment.Status.AvailableReplicas = 0
			operatorDeployment.Status.ReadyReplicas = 0
			Expect(k8sClient.Status().Update(ctx, &operatorDeployment)).Should(Succeed())

			addon = addonv1alpha1.ManagedClusterAddOn{}
			Eventually(func() bool {
				if err := k8sClient.Get(ctx,
					types.NamespacedName{Namespace: localClusterName, Name: util.AddonControllerName},
					&addon); err != nil {
					return false
				}
				if len(addon.Status.Conditions) == 0 {
					return false
				}
				return addon.Status.Conditions[0].Reason == degradedReasonOperatorNotAllAvailableReplicas+","+degradedReasonExternalDNSNotFound
			}).Should(BeTrue())

			By("Creating the Hypershift external dns deployment")
			externalDNSDeployment := appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      util.HypershiftOperatorExternalDNSName,
					Namespace: util.HypershiftOperatorNamespace,
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "hypershift"}},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": "hypershift"},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: "hypershift", Image: "nginx:1.14.2"}}, // fake deployment
						},
					},
				},
			}
			externalDNSDeployment.Spec.Replicas = new(int32)
			*externalDNSDeployment.Spec.Replicas = 3
			Expect(k8sClient.Create(ctx, &externalDNSDeployment)).Should(Succeed())

			externalDNSDeployment = appsv1.Deployment{}
			Expect(k8sClient.Get(ctx,
				types.NamespacedName{Namespace: util.HypershiftOperatorNamespace, Name: util.HypershiftOperatorExternalDNSName},
				&externalDNSDeployment)).Should(Succeed())

			externalDNSDeployment.Status.AvailableReplicas = 2
			externalDNSDeployment.Status.ReadyReplicas = 2
			externalDNSDeployment.Status.Replicas = 3
			Expect(k8sClient.Status().Update(ctx, &externalDNSDeployment)).Should(Succeed())

			addon = addonv1alpha1.ManagedClusterAddOn{}
			Eventually(func() bool {
				if err := k8sClient.Get(ctx,
					types.NamespacedName{Namespace: localClusterName, Name: util.AddonControllerName},
					&addon); err != nil {
					return false
				}
				if len(addon.Status.Conditions) == 0 {
					return false
				}
				return addon.Status.Conditions[0].Reason == degradedReasonOperatorNotAllAvailableReplicas+","+degradedReasonExternalDNSNotAllAvailableReplicas
			}).Should(BeTrue())

			By("Adding finalizers to the Hypershift operator and external dns deployments")
			operatorDeployment.Finalizers = []string{"hypershift.io/hypershift"}
			Expect(k8sClient.Update(ctx, &operatorDeployment)).Should(Succeed())
			externalDNSDeployment.Finalizers = []string{"hypershift.io/hypershift"}
			Expect(k8sClient.Update(ctx, &externalDNSDeployment)).Should(Succeed())

			By("Deleting the Hypershift operator and external dns deployments")

			Expect(k8sClient.Delete(ctx, &operatorDeployment)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, &externalDNSDeployment)).Should(Succeed())

			addon = addonv1alpha1.ManagedClusterAddOn{}
			Eventually(func() bool {
				if err := k8sClient.Get(ctx,
					types.NamespacedName{Namespace: localClusterName, Name: util.AddonControllerName},
					&addon); err != nil {
					return false
				}
				if len(addon.Status.Conditions) == 0 {
					return false
				}
				return addon.Status.Conditions[0].Reason == degradedReasonOperatorDeleted+","+degradedReasonExternalDNSDeleted
			}).Should(BeTrue())
		})
	})
})
