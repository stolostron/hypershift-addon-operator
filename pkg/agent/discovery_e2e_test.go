package agent

import (
	"context"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	discoveryv1 "github.com/stolostron/discovery/api/v1"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
)

// Simulated end-to-end test that exercises the full documented flow from
// docs/management/discovering_hostedclusters.md without requiring real clusters.
//
// Architecture simulated:
//   - ACM Hub Cluster: represented by the hub namespace (managedMCEClusterName)
//   - MCE Hosting Cluster (spoke): represented by the envtest API server
//   - Hosted Clusters: HostedCluster CRs created in the spoke namespace
//
// The DiscoveryAgent controller (wired in suite_test.go) handles the real
// controller logic. External components (ACM policy engine, discovery operator)
// are simulated by direct API calls in the test steps.

const (
	e2eHCNamespace         = "e2e-clusters"
	e2eHC1Name             = "e2e-hosted-1"
	e2eClusterID1          = "e2e-aaa11111-1111-2222-3333-444444444444"
	e2eMultiHC1Name        = "e2e-multi-hc-1"
	e2eMultiHC2Name        = "e2e-multi-hc-2"
	e2eMultiClusterID1     = "bbb11111-1111-2222-3333-444444444444"
	e2eMultiClusterID2     = "ccc55555-5555-6666-7777-888888888888"
	previouslyImportedAnno = "discovery.open-cluster-management.io/previously-auto-imported"
)

var _ = Describe("Simulated E2E: Hosted cluster discovery and import", Ordered, func() {
	ctx := context.Background()

	BeforeAll(func() {
		ctx = context.TODO()

		os.Unsetenv("DISABLE_HC_DISCOVERY")
		os.Unsetenv("DISCOVERY_PREFIX")

		By("Ensuring the managed MCE cluster namespace exists on the hub")
		mcNs := corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: managedMCEClusterName}}
		if err := k8sClient.Create(ctx, &mcNs); err != nil && !apierrors.IsAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}

		By("Creating a separate namespace for E2E hosted clusters on the spoke")
		hcNs := corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: e2eHCNamespace}}
		if err := k8sClient.Create(ctx, &hcNs); err != nil && !apierrors.IsAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}
	})

	// ---------------------------------------------------------------------------
	// Scenario 1: Full lifecycle — discover, import, upgrade, detach, delete
	// ---------------------------------------------------------------------------
	Context("Full lifecycle: create hosted cluster, discover, import, update, detach, delete", Ordered, func() {

		It("Step 1: A HostedCluster is created on the spoke with its API server unavailable", func() {
			By("Creating the HostedCluster resource")
			hc := newE2EHostedCluster(e2eHC1Name, e2eHCNamespace, e2eClusterID1)
			Expect(k8sClient.Create(ctx, hc)).Should(Succeed())

			By("Verifying no DiscoveredCluster is created while the HCP is unavailable")
			Consistently(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: managedMCEClusterName,
					Name:      e2eClusterID1,
				}, &discoveryv1.DiscoveredCluster{})
				return apierrors.IsNotFound(err)
			}, "5s", "1s").Should(BeTrue())
		})

		It("Step 2: The hosted control plane becomes available and a DiscoveredCluster is created on the hub", func() {
			hc := &hyperv1beta1.HostedCluster{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: e2eHCNamespace, Name: e2eHC1Name,
			}, hc)).Should(Succeed())

			By("Updating the HostedCluster status to indicate the HCP is available")
			updated := hc.DeepCopy()
			updated.Status.Conditions = []metav1.Condition{{
				Type:               string(hyperv1beta1.HostedClusterAvailable),
				Status:             metav1.ConditionTrue,
				Reason:             hyperv1beta1.AsExpectedReason,
				LastTransitionTime: metav1.Time{Time: time.Now()},
			}}
			updated.Status.ControlPlaneEndpoint = hyperv1beta1.APIEndpoint{
				Host: "api.e2e-hosted-1.example.com",
				Port: 6443,
			}
			updated.Status.Version = &hyperv1beta1.ClusterVersionStatus{
				History: []configv1.UpdateHistory{{
					State:       configv1.CompletedUpdate,
					Version:     "4.16.0",
					StartedTime: metav1.Time{Time: time.Now()},
				}},
			}
			Expect(k8sClient.Status().Update(ctx, updated)).Should(Succeed())

			By("Waiting for the DiscoveryAgent to create a DiscoveredCluster")
			dc := &discoveryv1.DiscoveredCluster{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Namespace: managedMCEClusterName,
					Name:      e2eClusterID1,
				}, dc)
			}, "30s", "1s").Should(Succeed())

			By("Verifying the DiscoveredCluster fields match the HostedCluster")
			Expect(dc.Spec.DisplayName).To(Equal(managedMCEClusterName + "-" + e2eHC1Name))
			Expect(dc.Spec.APIURL).To(Equal("https://api.e2e-hosted-1.example.com:6443"))
			Expect(dc.Spec.Type).To(Equal("MultiClusterEngineHCP"))
			Expect(dc.Spec.Status).To(Equal("Active"))
			Expect(dc.Spec.CloudProvider).To(Equal("aws"))
			Expect(dc.Spec.OpenshiftVersion).To(Equal("4.16.0"))
			Expect(dc.Spec.ImportAsManagedCluster).To(BeFalse())
			Expect(dc.Spec.IsManagedCluster).To(BeFalse())
			Expect(dc.Labels[util.HostedClusterNameLabel]).To(Equal(e2eHC1Name))
			Expect(dc.Labels[util.HostedClusterNamespaceLabel]).To(Equal(e2eHCNamespace))
		})

		It("Step 3: ACM auto-import policy triggers import by setting importAsManagedCluster", func() {
			By("Reading the DiscoveredCluster (simulates policy template lookup)")
			dc := &discoveryv1.DiscoveredCluster{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: managedMCEClusterName,
				Name:      e2eClusterID1,
			}, dc)).Should(Succeed())

			By("Verifying the policy filter criteria: type=MultiClusterEngineHCP, status=Active")
			Expect(dc.Spec.Type).To(Equal("MultiClusterEngineHCP"))
			Expect(dc.Spec.Status).To(Equal("Active"))

			By("Simulating the policy: set importAsManagedCluster to true")
			patched := dc.DeepCopy()
			patched.Spec.ImportAsManagedCluster = true
			Expect(k8sClient.Update(ctx, patched)).Should(Succeed())

			updatedDC := &discoveryv1.DiscoveredCluster{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: managedMCEClusterName,
				Name:      e2eClusterID1,
			}, updatedDC)).Should(Succeed())
			Expect(updatedDC.Spec.ImportAsManagedCluster).To(BeTrue())
		})

		It("Step 4: Hub discovery operator imports the cluster as a ManagedCluster", func() {
			dc := &discoveryv1.DiscoveredCluster{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: managedMCEClusterName,
				Name:      e2eClusterID1,
			}, dc)).Should(Succeed())

			By("Simulating the hub discovery operator: creating a ManagedCluster from the DiscoveredCluster")
			mc := managedClusterFromDiscoveredCluster(dc, managedMCEClusterName)
			Expect(k8sClient.Create(ctx, mc)).Should(Succeed())

			By("Verifying the ManagedCluster was created with correct metadata")
			gotMC := &clusterv1.ManagedCluster{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: dc.Spec.DisplayName}, gotMC)).Should(Succeed())
			Expect(gotMC.Spec.HubAcceptsClient).To(BeTrue())
			Expect(gotMC.Spec.LeaseDurationSeconds).To(Equal(int32(60)))
			Expect(gotMC.Annotations["import.open-cluster-management.io/klusterlet-deploy-mode"]).To(Equal("Hosted"))
			Expect(gotMC.Annotations["import.open-cluster-management.io/hosting-cluster-name"]).To(Equal(managedMCEClusterName))
			Expect(gotMC.Annotations["open-cluster-management/created-via"]).To(Equal("hypershift"))
			Expect(gotMC.Labels["cluster.open-cluster-management.io/clusterset"]).To(Equal("default"))

			By("Marking the DiscoveredCluster as managed")
			dcUpdated := dc.DeepCopy()
			dcUpdated.Spec.IsManagedCluster = true
			Expect(k8sClient.Update(ctx, dcUpdated)).Should(Succeed())
		})

		It("Step 5: HostedCluster version upgrade propagates to DiscoveredCluster", func() {
			hc := &hyperv1beta1.HostedCluster{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: e2eHCNamespace, Name: e2eHC1Name,
			}, hc)).Should(Succeed())

			By("Upgrading the HostedCluster from 4.16.0 to 4.16.3")
			updated := hc.DeepCopy()
			updated.Status.Version = &hyperv1beta1.ClusterVersionStatus{
				History: []configv1.UpdateHistory{
					{
						State:       configv1.CompletedUpdate,
						Version:     "4.16.3",
						StartedTime: metav1.Time{Time: time.Now()},
					},
					{
						State:       configv1.CompletedUpdate,
						Version:     "4.16.0",
						StartedTime: metav1.Time{Time: time.Now().Add(-24 * time.Hour)},
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, updated)).Should(Succeed())

			By("Verifying the DiscoveredCluster reflects the new version")
			dc := &discoveryv1.DiscoveredCluster{}
			Eventually(func() string {
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: managedMCEClusterName,
					Name:      e2eClusterID1,
				}, dc); err != nil {
					return ""
				}
				return dc.Spec.OpenshiftVersion
			}, "30s", "1s").Should(Equal("4.16.3"))
		})

		It("Step 6: Detach — delete ManagedCluster and mark DiscoveredCluster to prevent re-import", func() {
			dc := &discoveryv1.DiscoveredCluster{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: managedMCEClusterName,
				Name:      e2eClusterID1,
			}, dc)).Should(Succeed())

			By("Deleting the ManagedCluster (detach)")
			mc := &clusterv1.ManagedCluster{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: dc.Spec.DisplayName}, mc)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, mc)).Should(Succeed())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: dc.Spec.DisplayName}, &clusterv1.ManagedCluster{})
				return apierrors.IsNotFound(err)
			}, "30s", "1s").Should(BeTrue())

			By("Simulating hub marking the DiscoveredCluster as previously auto-imported")
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: managedMCEClusterName,
				Name:      e2eClusterID1,
			}, dc)).Should(Succeed())

			patched := dc.DeepCopy()
			if patched.Annotations == nil {
				patched.Annotations = make(map[string]string)
			}
			patched.Annotations[previouslyImportedAnno] = "true"
			patched.Spec.IsManagedCluster = false
			patched.Spec.ImportAsManagedCluster = false
			Expect(k8sClient.Update(ctx, patched)).Should(Succeed())

			By("Verifying the previously-auto-imported annotation prevents re-import")
			updatedDC := &discoveryv1.DiscoveredCluster{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: managedMCEClusterName,
				Name:      e2eClusterID1,
			}, updatedDC)).Should(Succeed())
			Expect(updatedDC.Annotations[previouslyImportedAnno]).To(Equal("true"))
			Expect(updatedDC.Spec.ImportAsManagedCluster).To(BeFalse())
		})

		It("Step 7: Delete HostedCluster and verify DiscoveredCluster is cleaned up", func() {
			hc := &hyperv1beta1.HostedCluster{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: e2eHCNamespace, Name: e2eHC1Name,
			}, hc)).Should(Succeed())

			By("Deleting the HostedCluster from the spoke")
			Expect(k8sClient.Delete(ctx, hc)).Should(Succeed())

			By("Verifying the DiscoveredCluster is removed from the hub")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: managedMCEClusterName,
					Name:      e2eClusterID1,
				}, &discoveryv1.DiscoveredCluster{})
				return apierrors.IsNotFound(err)
			}, "30s", "1s").Should(BeTrue())
		})

		AfterAll(func() {
			hc := &hyperv1beta1.HostedCluster{}
			if err := k8sClient.Get(ctx, types.NamespacedName{
				Namespace: e2eHCNamespace, Name: e2eHC1Name,
			}, hc); err == nil {
				_ = k8sClient.Delete(ctx, hc)
			}

			mc := &clusterv1.ManagedCluster{}
			if err := k8sClient.Get(ctx, types.NamespacedName{
				Name: managedMCEClusterName + "-" + e2eHC1Name,
			}, mc); err == nil {
				_ = k8sClient.Delete(ctx, mc)
			}

			dc := &discoveryv1.DiscoveredCluster{}
			if err := k8sClient.Get(ctx, types.NamespacedName{
				Namespace: managedMCEClusterName, Name: e2eClusterID1,
			}, dc); err == nil {
				_ = k8sClient.Delete(ctx, dc)
			}
		})
	})

	// ---------------------------------------------------------------------------
	// Scenario 2: Multiple hosted clusters on the same MCE spoke
	// ---------------------------------------------------------------------------
	Context("Multiple hosted clusters on the same spoke", Ordered, func() {

		It("Creates multiple HostedClusters and verifies individual DiscoveredClusters", func() {
			By("Creating the first HostedCluster")
			hc1 := newE2EHostedCluster(e2eMultiHC1Name, e2eHCNamespace, e2eMultiClusterID1)
			Expect(k8sClient.Create(ctx, hc1)).Should(Succeed())

			By("Making the first HostedCluster available")
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: e2eHCNamespace, Name: e2eMultiHC1Name,
			}, hc1)).Should(Succeed())
			makeHostedClusterAvailable(ctx, hc1, "api.multi-hc-1.example.com", 6443, "4.16.0")

			By("Creating the second HostedCluster")
			hc2 := newE2EHostedCluster(e2eMultiHC2Name, e2eHCNamespace, e2eMultiClusterID2)
			Expect(k8sClient.Create(ctx, hc2)).Should(Succeed())

			By("Making the second HostedCluster available")
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: e2eHCNamespace, Name: e2eMultiHC2Name,
			}, hc2)).Should(Succeed())
			makeHostedClusterAvailable(ctx, hc2, "api.multi-hc-2.example.com", 6443, "4.15.8")

			By("Waiting for both DiscoveredClusters to be created")
			dc1 := &discoveryv1.DiscoveredCluster{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Namespace: managedMCEClusterName,
					Name:      e2eMultiClusterID1,
				}, dc1)
			}, "30s", "1s").Should(Succeed())

			dc2 := &discoveryv1.DiscoveredCluster{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Namespace: managedMCEClusterName,
					Name:      e2eMultiClusterID2,
				}, dc2)
			}, "30s", "1s").Should(Succeed())

			By("Verifying each DiscoveredCluster has the correct display name and version")
			Expect(dc1.Spec.DisplayName).To(Equal(managedMCEClusterName + "-" + e2eMultiHC1Name))
			Expect(dc2.Spec.DisplayName).To(Equal(managedMCEClusterName + "-" + e2eMultiHC2Name))
			Expect(dc1.Spec.OpenshiftVersion).To(Equal("4.16.0"))
			Expect(dc2.Spec.OpenshiftVersion).To(Equal("4.15.8"))
			Expect(dc1.Spec.APIURL).To(Equal("https://api.multi-hc-1.example.com:6443"))
			Expect(dc2.Spec.APIURL).To(Equal("https://api.multi-hc-2.example.com:6443"))
		})

		It("Deleting one HostedCluster removes only its DiscoveredCluster", func() {
			By("Deleting the first HostedCluster")
			hc1 := &hyperv1beta1.HostedCluster{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: e2eHCNamespace, Name: e2eMultiHC1Name,
			}, hc1)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, hc1)).Should(Succeed())

			By("Verifying the first DiscoveredCluster is removed")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: managedMCEClusterName,
					Name:      e2eMultiClusterID1,
				}, &discoveryv1.DiscoveredCluster{})
				return apierrors.IsNotFound(err)
			}, "30s", "1s").Should(BeTrue())

			By("Verifying the second DiscoveredCluster still exists")
			dc2 := &discoveryv1.DiscoveredCluster{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: managedMCEClusterName,
				Name:      e2eMultiClusterID2,
			}, dc2)).Should(Succeed())
			Expect(dc2.Spec.DisplayName).To(Equal(managedMCEClusterName + "-" + e2eMultiHC2Name))
		})

		AfterAll(func() {
			hc2 := &hyperv1beta1.HostedCluster{}
			if err := k8sClient.Get(ctx, types.NamespacedName{
				Namespace: e2eHCNamespace, Name: e2eMultiHC2Name,
			}, hc2); err == nil {
				_ = k8sClient.Delete(ctx, hc2)
			}
		})
	})
})

// newE2EHostedCluster builds a HostedCluster for the E2E tests.
// Status is left empty so it can be set via Status().Update() after creation.
func newE2EHostedCluster(name, namespace, clusterID string) *hyperv1beta1.HostedCluster {
	return &hyperv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: hyperv1beta1.HostedClusterSpec{
			ClusterID: clusterID,
			InfraID:   name + "-infra",
			Platform: hyperv1beta1.PlatformSpec{
				Type: hyperv1beta1.AWSPlatform,
			},
			Networking: hyperv1beta1.ClusterNetworking{
				NetworkType:    hyperv1beta1.OpenShiftSDN,
				ServiceNetwork: []hyperv1beta1.ServiceNetworkEntry{},
				ClusterNetwork: []hyperv1beta1.ClusterNetworkEntry{},
			},
			Services: []hyperv1beta1.ServicePublishingStrategyMapping{},
			Release: hyperv1beta1.Release{
				Image: "quay.io/openshift-release-dev/ocp-release:4.16.0-x86_64",
			},
			Etcd: hyperv1beta1.EtcdSpec{
				ManagementType: hyperv1beta1.Managed,
			},
		},
	}
}

// makeHostedClusterAvailable sets the HostedCluster status to indicate the
// hosted control plane API server is available, which triggers the DiscoveryAgent.
func makeHostedClusterAvailable(ctx context.Context, hc *hyperv1beta1.HostedCluster, host string, port int32, version string) {
	updated := hc.DeepCopy()
	updated.Status.Conditions = []metav1.Condition{{
		Type:               string(hyperv1beta1.HostedClusterAvailable),
		Status:             metav1.ConditionTrue,
		Reason:             hyperv1beta1.AsExpectedReason,
		LastTransitionTime: metav1.Time{Time: time.Now()},
	}}
	updated.Status.ControlPlaneEndpoint = hyperv1beta1.APIEndpoint{
		Host: host,
		Port: port,
	}
	updated.Status.Version = &hyperv1beta1.ClusterVersionStatus{
		History: []configv1.UpdateHistory{{
			State:       configv1.CompletedUpdate,
			Version:     version,
			StartedTime: metav1.Time{Time: time.Now()},
		}},
	}
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, updated)).Should(Succeed())
}

// managedClusterFromDiscoveredCluster simulates what the hub-side discovery
// operator does when importAsManagedCluster is set to true: it creates a
// ManagedCluster with hosted import annotations derived from the DiscoveredCluster.
func managedClusterFromDiscoveredCluster(dc *discoveryv1.DiscoveredCluster, hostingClusterName string) *clusterv1.ManagedCluster {
	return &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: dc.Spec.DisplayName,
			Annotations: map[string]string{
				"import.open-cluster-management.io/klusterlet-deploy-mode": "Hosted",
				"import.open-cluster-management.io/hosting-cluster-name":   hostingClusterName,
				"open-cluster-management/created-via":                      "hypershift",
			},
			Labels: map[string]string{
				"name":   dc.Spec.DisplayName,
				"vendor": "auto-detect",
				"cloud":  "auto-detect",
				"cluster.open-cluster-management.io/clusterset": "default",
			},
		},
		Spec: clusterv1.ManagedClusterSpec{
			HubAcceptsClient:     true,
			LeaseDurationSeconds: 60,
		},
	}
}
