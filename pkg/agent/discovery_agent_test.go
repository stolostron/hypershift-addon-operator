package agent

import (
	"context"
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	discoveryv1 "github.com/stolostron/discovery/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const managedMCEClusterName = "managed-mce"
const hcNamespace = "clusters"
const hcName = "hc-1"
const hcName2 = "hc-2"
const hcName3 = "hc-3"
const clusterID = "89693e2e-1198-4710-a254-c8277db50779"
const clusterID2 = "89693e2e-2246-4710-a254-c8277db50779"
const clusterID3 = "89693e2e-3397-4710-a254-c8277db50779"

var _ = Describe("Hosted cluster discovery agent", Ordered, func() {
	ctx := context.Background()

	BeforeAll(func() {
		ctx = context.TODO()
		By(fmt.Sprintf("Create the managed cluster namespace for %s", managedMCEClusterName))
		mcNs := corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: managedMCEClusterName,
			},
		}
		Expect(k8sClient.Create(ctx, &mcNs)).Should(Succeed())

		By("Create the hosted cluster namespace")
		hcNs := corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: hcNamespace,
			},
		}
		Expect(k8sClient.Create(ctx, &hcNs)).Should(Succeed())
	})

	Context("When a hosted cluster is created and its kube API server becomes ready", func() {
		It("can find the MCE managed cluster namespace ", func() {
			mcns := corev1.Namespace{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: managedMCEClusterName}, &mcns)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should discover the hosted cluster and create a DiscoveredCluster for it", func() {

			By("creating a hosted cluster with its kube API server not ready yet")

			hc := getHostedCluster(types.NamespacedName{Namespace: hcNamespace, Name: hcName})
			hsStatus := &hyperv1beta1.HostedClusterStatus{
				KubeConfig: &corev1.LocalObjectReference{Name: "kubeconfig"},
				Conditions: []metav1.Condition{{Type: string(hyperv1beta1.HostedClusterAvailable), Status: metav1.ConditionFalse, Reason: hyperv1beta1.AsExpectedReason}},
				Version: &hyperv1beta1.ClusterVersionStatus{
					History: []configv1.UpdateHistory{{State: configv1.CompletedUpdate}},
				},
			}
			hc.Status = *hsStatus
			Expect(k8sClient.Create(ctx, hc)).Should(Succeed())

			discoveredCluster := &discoveryv1.DiscoveredCluster{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: managedMCEClusterName, Name: hcName}, discoveredCluster)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())

			By("setting the hosted cluster's condition to indicate that its kube API server is ready")

			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: hcNamespace, Name: hcName}, hc)
			Expect(err).NotTo(HaveOccurred())

			newHC := hc.DeepCopy()
			newCondition := []metav1.Condition{{
				Type:               string(hyperv1beta1.HostedClusterAvailable),
				Status:             metav1.ConditionTrue,
				Reason:             hyperv1beta1.AsExpectedReason,
				LastTransitionTime: metav1.Time{Time: time.Now()},
			}}
			newHC.Status.Conditions = newCondition
			Expect(k8sClient.Status().Update(ctx, newHC)).Should(Succeed())

			Eventually(func() bool {
				if err := k8sClient.Get(ctx,
					types.NamespacedName{Namespace: managedMCEClusterName, Name: clusterID},
					discoveredCluster); err != nil {
					return false
				}
				return true
			}).Should(BeTrue())
			Expect(discoveredCluster.Spec.DisplayName).To(Equal(managedMCEClusterName + "-" + hcName))

		})
	})

	Context("When the hosted cluster display name prefix is configured", func() {
		It("Should use the prefix to name the discover hosted clusters", func() {
			By("Configuring the name prefix to none")
			os.Setenv("DISCOVERY_PREFIX", "")

			By("Creating a hosted cluster with kube API server ready")

			hc := getHostedCluster(types.NamespacedName{Namespace: hcNamespace, Name: hcName2})
			hc.Spec.ClusterID = clusterID2
			Expect(k8sClient.Create(ctx, hc)).Should(Succeed())

			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: hcNamespace, Name: hcName2}, hc)
			Expect(err).NotTo(HaveOccurred())

			newHC := hc.DeepCopy()
			newCondition := []metav1.Condition{{
				Type:               string(hyperv1beta1.HostedClusterAvailable),
				Status:             metav1.ConditionTrue,
				Reason:             hyperv1beta1.AsExpectedReason,
				LastTransitionTime: metav1.Time{Time: time.Now()},
			}}
			newHC.Status.Conditions = newCondition
			Expect(k8sClient.Status().Update(ctx, newHC)).Should(Succeed())

			discoveredCluster := &discoveryv1.DiscoveredCluster{}
			Eventually(func() bool {
				if err := k8sClient.Get(ctx,
					types.NamespacedName{Namespace: managedMCEClusterName, Name: clusterID2},
					discoveredCluster); err != nil {
					return false
				}
				return true
			}).Should(BeTrue())
			Expect(discoveredCluster.Spec.DisplayName).To(Equal(hcName2))

			By("Configuring the name prefix to something else")
			os.Setenv("DISCOVERY_PREFIX", "abcd")

			By("Creating a hosted cluster with kube API server ready")

			hc = getHostedCluster(types.NamespacedName{Namespace: hcNamespace, Name: hcName3})
			hc.Spec.ClusterID = clusterID3
			Expect(k8sClient.Create(ctx, hc)).Should(Succeed())

			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: hcNamespace, Name: hcName3}, hc)
			Expect(err).NotTo(HaveOccurred())

			newHC = hc.DeepCopy()
			newHC.Status.Conditions = newCondition
			Expect(k8sClient.Status().Update(ctx, newHC)).Should(Succeed())

			discoveredCluster = &discoveryv1.DiscoveredCluster{}
			Eventually(func() bool {
				if err := k8sClient.Get(ctx,
					types.NamespacedName{Namespace: managedMCEClusterName, Name: clusterID3},
					discoveredCluster); err != nil {
					return false
				}
				return true
			}).Should(BeTrue())
			Expect(discoveredCluster.Spec.DisplayName).To(Equal("abcd" + "-" + hcName3))
		})
	})

	Context("When a hosted cluster is updated", func() {
		It("Should find the existing DiscoveredCluster and update it", func() {
			By("setting the hosted cluster's condition to indicate that its kube API server is ready")

			hc := &hyperv1beta1.HostedCluster{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: hcNamespace, Name: hcName}, hc)
			Expect(err).NotTo(HaveOccurred())

			newHC := hc.DeepCopy()
			newHC.Status.ControlPlaneEndpoint.Host = "test.com"
			newHC.Status.ControlPlaneEndpoint.Port = 6444
			newHC.Status.Version = &hyperv1beta1.ClusterVersionStatus{
				History: []configv1.UpdateHistory{{
					State:       configv1.CompletedUpdate,
					Version:     "4.15.13",
					StartedTime: metav1.Time{Time: time.Now()},
				}},
			}
			Expect(k8sClient.Status().Update(ctx, newHC)).Should(Succeed())

			discoveredCluster := &discoveryv1.DiscoveredCluster{}
			Eventually(func() bool {
				if err := k8sClient.Get(ctx,
					types.NamespacedName{Namespace: managedMCEClusterName, Name: clusterID},
					discoveredCluster); err != nil {
					return false
				}

				if discoveredCluster.Spec.OpenshiftVersion != "4.15.13" {
					return false
				}

				if discoveredCluster.Spec.APIURL != "https://test.com:6444" {
					return false
				}

				return discoveredCluster.Name == clusterID
			}).Should(BeTrue())
		})
	})

	Context("When a hosted cluster is deleted", func() {
		It("Should delete the corresponding DiscoveredCluster", func() {
			By("deleting the hosted cluster")

			hc := &hyperv1beta1.HostedCluster{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: hcNamespace, Name: hcName}, hc)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Delete(ctx, hc)
			Expect(err).NotTo(HaveOccurred())

			discoveredCluster := &discoveryv1.DiscoveredCluster{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx,
					types.NamespacedName{Namespace: managedMCEClusterName, Name: clusterID},
					discoveredCluster)

				return apierrors.IsNotFound(err)
			}).Should(BeTrue())
		})
	})

	Context("When the hosted cluster discovery is disabled", func() {
		It("Should not try to discover hosted clusters", func() {
			By("Disabling the hosted cluster discovery")
			os.Setenv("DISABLE_HC_DISCOVERY", "true")

			By("Creating a hosted cluster with kube API server ready")

			hc := getHostedCluster(types.NamespacedName{Namespace: hcNamespace, Name: hcName})
			Expect(k8sClient.Create(ctx, hc)).Should(Succeed())

			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: hcNamespace, Name: hcName}, hc)
			Expect(err).NotTo(HaveOccurred())

			newHC := hc.DeepCopy()
			newCondition := []metav1.Condition{{
				Type:               string(hyperv1beta1.HostedClusterAvailable),
				Status:             metav1.ConditionTrue,
				Reason:             hyperv1beta1.AsExpectedReason,
				LastTransitionTime: metav1.Time{Time: time.Now()},
			}}
			newHC.Status.Conditions = newCondition
			Expect(k8sClient.Status().Update(ctx, newHC)).Should(Succeed())

			discoveredCluster := &discoveryv1.DiscoveredCluster{}
			Consistently(func() bool {
				err := k8sClient.Get(ctx,
					types.NamespacedName{Namespace: managedMCEClusterName, Name: clusterID},
					discoveredCluster)

				return apierrors.IsNotFound(err)
			}, "15s", "3s").Should(BeTrue())

			err = k8sClient.Delete(ctx, hc)
			Expect(err).NotTo(HaveOccurred())

		})
	})

})
