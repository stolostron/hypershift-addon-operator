package agent

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/go-logr/zapr"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/clientcmd"
	clustercsfake "open-cluster-management.io/api/client/cluster/clientset/versioned/fake"
	clusterv1alpha1 "open-cluster-management.io/api/cluster/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1beta1 "github.com/openshift/hypershift/api/v1beta1"

	"github.com/stolostron/hypershift-addon-operator/pkg/metrics"
)

func TestReconcile(t *testing.T) {
	ctx := context.Background()
	client := initClient()
	zapLog, _ := zap.NewDevelopment()

	fakeClusterCS := clustercsfake.NewSimpleClientset()

	aCtrl := &agentController{
		spokeClustersClient: fakeClusterCS,
		spokeUncachedClient: client,
		spokeClient:         client,
		hubClient:           client,
		log:                 zapr.NewLogger(zapLog),
	}

	// Create secrets
	hcNN := types.NamespacedName{Name: "hd-1", Namespace: "clusters"}
	secrets := aCtrl.scaffoldHostedclusterSecrets(hcNN)

	for _, sec := range secrets {
		sec.SetName(fmt.Sprintf("%s-%s", hcNN.Name, sec.Name))
		secData := map[string][]byte{}
		secData["kubeconfig"] = []byte(`apiVersion: v1
clusters:
- cluster:
    server: https://kube-apiserver.ocm-dev-1sv4l4ldnr6rd8ni12ndo4vtiq2gd7a4-sbarouti267.svc.cluster.local:7443
  name: cluster
contexts:
- context:
    cluster: cluster
    namespace: default
    user: admin
  name: admin
current-context: admin
kind: Config`)
		sec.Data = secData
		aCtrl.hubClient.Create(ctx, sec)
		defer aCtrl.hubClient.Delete(ctx, sec)
	}

	apiService := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: "clusters-hd-1",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:     "https",
					Port:     443,
					Protocol: "TCP",
					TargetPort: intstr.IntOrString{
						IntVal: 6443,
					},
				},
			},
		},
	}
	err := aCtrl.hubClient.Create(ctx, apiService)
	assert.Nil(t, err, "err nil when kube-apiserver service is created successfully")
	defer aCtrl.hubClient.Delete(ctx, apiService)

	// Create hosted cluster
	hc := getHostedCluster(hcNN)
	err = aCtrl.hubClient.Create(ctx, hc)
	assert.Nil(t, err, "err nil when hosted cluster is created successfully")

	// Create klusterlet namespace
	klusterletNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "klusterlet-" + hc.Name,
		},
	}
	err = aCtrl.hubClient.Create(ctx, klusterletNamespace)
	assert.Nil(t, err, "err nil when klusterletNamespace was created successfully")
	defer aCtrl.hubClient.Delete(ctx, klusterletNamespace)

	// Reconcile with annotation
	_, err = aCtrl.Reconcile(ctx, ctrl.Request{NamespacedName: hcNN})
	assert.Nil(t, err, "err nil when reconcile was successfully")

	// Secret for kubconfig and kubeadmin-password are created
	secret := &corev1.Secret{}
	kcSecretNN := types.NamespacedName{Name: fmt.Sprintf("%s-admin-kubeconfig", hc.Name), Namespace: aCtrl.clusterName}
	err = aCtrl.hubClient.Get(ctx, kcSecretNN, secret)
	assert.Nil(t, err, "is nil when the admin kubeconfig secret is found")

	pwdSecretNN := types.NamespacedName{Name: fmt.Sprintf("%s-kubeadmin-password", hc.Name), Namespace: aCtrl.clusterName}
	err = aCtrl.hubClient.Get(ctx, pwdSecretNN, secret)
	assert.Nil(t, err, "is nil when the kubeadmin password secret is found")

	kcExtSecretNN := types.NamespacedName{Name: "external-managed-kubeconfig", Namespace: "klusterlet-" + hc.Name}
	err = aCtrl.hubClient.Get(ctx, kcExtSecretNN, secret)
	assert.Nil(t, err, "is nil when external-managed-kubeconfig secret is found")

	kubeconfig, err := clientcmd.Load(secret.Data["kubeconfig"])
	assert.Nil(t, err, "is nil when kubeconfig data can be loaded")
	assert.Equal(t, kubeconfig.Clusters["cluster"].Server, "https://kube-apiserver."+hc.Namespace+"-"+hc.Name+".svc.cluster.local:443")

	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.KubeconfigSecretCopyFailureCount))
	assert.Equal(t, float64(1), testutil.ToFloat64(metrics.TotalHostedClusterGauge))
	assert.Equal(t, float64(1), testutil.ToFloat64(metrics.HostedClusterAvailableGauge))

	// Delete hosted cluster and reconcile
	aCtrl.hubClient.Delete(ctx, hc)
	_, err = aCtrl.Reconcile(ctx, ctrl.Request{NamespacedName: hcNN})
	assert.Nil(t, err, "err nil when reconcile was successfully")

	err = aCtrl.hubClient.Get(ctx, kcSecretNN, secret)
	assert.True(t, err != nil && errors.IsNotFound(err), "is true when the admin kubeconfig secret is deleted")
	err = aCtrl.hubClient.Get(ctx, pwdSecretNN, secret)
	assert.True(t, err != nil && errors.IsNotFound(err), "is nil when the kubeadmin password secret is deleted")
}

func TestReconcileRequeue(t *testing.T) {
	ctx := context.Background()
	client := initClient()
	zapLog, _ := zap.NewDevelopment()

	fakeClusterCS := clustercsfake.NewSimpleClientset()

	aCtrl := &agentController{
		spokeClustersClient: fakeClusterCS,
		spokeUncachedClient: client,
		spokeClient:         client,
		hubClient:           client,
		log:                 zapr.NewLogger(zapLog),
	}

	// Create secrets
	hcNN := types.NamespacedName{Name: "hd-1", Namespace: "clusters"}
	secrets := aCtrl.scaffoldHostedclusterSecrets(hcNN)

	for _, sec := range secrets {
		sec.SetName(fmt.Sprintf("%s-%s", hcNN.Name, sec.Name))
		secData := map[string][]byte{}
		secData["kubeconfig"] = []byte(`apiVersion: v1
clusters:
- cluster:
    server: https://kube-apiserver.ocm-dev-1sv4l4ldnr6rd8ni12ndo4vtiq2gd7a4-sbarouti267.svc.cluster.local:7443
  name: cluster
contexts:
- context:
    cluster: cluster
    namespace: default
    user: admin
  name: admin
current-context: admin
kind: Config`)
		sec.Data = secData
		aCtrl.hubClient.Create(ctx, sec)
		defer aCtrl.hubClient.Delete(ctx, sec)
	}

	apiService := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: "clusters-hd-1",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:     "https",
					Port:     443,
					Protocol: "TCP",
					TargetPort: intstr.IntOrString{
						IntVal: 6443,
					},
				},
			},
		},
	}
	err := aCtrl.hubClient.Create(ctx, apiService)
	assert.Nil(t, err, "err nil when kube-apiserver service is created successfully")
	defer aCtrl.hubClient.Delete(ctx, apiService)

	// Create hosted cluster
	hc := getHostedCluster(hcNN)
	err = aCtrl.hubClient.Create(ctx, hc)
	assert.Nil(t, err, "err nil when hosted cluster is created successfully")

	// Reconcile with annotation
	res, err := aCtrl.Reconcile(ctx, ctrl.Request{NamespacedName: hcNN})
	assert.Nil(t, err, "err nil when reconcile was successfully")

	// external-managed-kubeconfig could not be created because there is no klusterlet namespace
	secret := &corev1.Secret{}
	kcExtSecretNN := types.NamespacedName{Name: "external-managed-kubeconfig", Namespace: "klusterlet-" + hc.Name}
	err = aCtrl.hubClient.Get(ctx, kcExtSecretNN, secret)
	assert.NotNil(t, err, "external-managed-kubeconfig secret not found")
	assert.Equal(t, true, res.Requeue)
	assert.Equal(t, 30*time.Second, res.RequeueAfter)
	// Test that we do not count the klusterlet namespace missing as an error, this just means import has not been
	// triggered
	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.KubeconfigSecretCopyFailureCount))
}

func TestReconcileWithAnnotation(t *testing.T) {
	ctx := context.Background()
	client := initClient()
	zapLog, _ := zap.NewDevelopment()

	fakeClusterCS := clustercsfake.NewSimpleClientset()

	aCtrl := &agentController{
		spokeClustersClient: fakeClusterCS,
		spokeUncachedClient: client,
		spokeClient:         client,
		hubClient:           client,
		log:                 zapr.NewLogger(zapLog),
	}

	// Create secrets
	hcNN := types.NamespacedName{Name: "hd-1", Namespace: "clusters"}
	secrets := aCtrl.scaffoldHostedclusterSecrets(hcNN)

	for _, sec := range secrets {
		sec.SetName(fmt.Sprintf("%s-%s", hcNN.Name, sec.Name))
		secData := map[string][]byte{}
		secData["kubeconfig"] = []byte(`apiVersion: v1
clusters:
- cluster:
    server: https://kube-apiserver.ocm-dev-1sv4l4ldnr6rd8ni12ndo4vtiq2gd7a4-sbarouti267.svc.cluster.local:6443
  name: cluster	
contexts:
- context:
    cluster: cluster
    namespace: default
    user: admin
  name: admin
current-context: admin
kind: Config`)
		sec.Data = secData
		aCtrl.hubClient.Create(ctx, sec)
		defer aCtrl.hubClient.Delete(ctx, sec)
	}

	apiService := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: "clusters-hd-1",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:     "https",
					Port:     443,
					Protocol: "TCP",
					TargetPort: intstr.IntOrString{
						IntVal: 6443,
					},
				},
			},
		},
	}
	err := aCtrl.hubClient.Create(ctx, apiService)
	assert.Nil(t, err, "err nil when kube-apiserver service is created successfully")
	defer aCtrl.hubClient.Delete(ctx, apiService)

	// Create hosted cluster
	hc := getHostedCluster(hcNN)
	hc.Annotations = map[string]string{util.ManagedClusterAnnoKey: "infra-abcdef"}
	err = aCtrl.hubClient.Create(ctx, hc)
	assert.Nil(t, err, "err nil when hosted cluster is created successfully")

	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.PlacementScoreFailureCount))

	// Create klusterlet namespace
	klusterletNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "klusterlet-" + hc.Spec.InfraID,
		},
	}
	err = aCtrl.hubClient.Create(ctx, klusterletNamespace)
	assert.Nil(t, err, "err nil when klusterletNamespace was created successfully")
	defer aCtrl.hubClient.Delete(ctx, klusterletNamespace)

	// Reconcile with no annotation
	_, err = aCtrl.Reconcile(ctx, ctrl.Request{NamespacedName: hcNN})
	assert.Nil(t, err, "err nil when reconcile was successfully")

	// Secret for kubconfig and kubeadmin-password are created
	secret := &corev1.Secret{}
	kcSecretNN := types.NamespacedName{Name: fmt.Sprintf("%s-admin-kubeconfig", hc.Spec.InfraID), Namespace: aCtrl.clusterName}
	err = aCtrl.spokeClient.Get(ctx, kcSecretNN, secret)
	assert.Nil(t, err, "is nil when the admin kubeconfig secret is found")

	pwdSecretNN := types.NamespacedName{Name: fmt.Sprintf("%s-kubeadmin-password", hc.Spec.InfraID), Namespace: aCtrl.clusterName}
	err = aCtrl.hubClient.Get(ctx, pwdSecretNN, secret)
	assert.Nil(t, err, "is nil when the kubeadmin password secret is found")

	kcExtSecretNN := types.NamespacedName{Name: "external-managed-kubeconfig", Namespace: "klusterlet-" + hc.Spec.InfraID}
	err = aCtrl.hubClient.Get(ctx, kcExtSecretNN, secret)
	assert.Nil(t, err, "is nil when external-managed-kubeconfig secret is found")

	addOnPlacementScore := &clusterv1alpha1.AddOnPlacementScore{}
	addOnPlacementScoreNN := types.NamespacedName{Name: util.HostedClusterScoresResourceName, Namespace: aCtrl.clusterName}
	err = aCtrl.hubClient.Get(ctx, addOnPlacementScoreNN, addOnPlacementScore)
	assert.Nil(t, err, "is nil when hosted-clusters-score AddOnPlacementScore is found")
	assert.Equal(t, util.HostedClusterScoresScoreName, addOnPlacementScore.Status.Scores[0].Name, "hosted-clusters-score AddOnPlacementScore score name should be "+util.HostedClusterScoresScoreName)
	assert.Equal(t, int32(1), addOnPlacementScore.Status.Scores[0].Value, "hosted-clusters-score AddOnPlacementScore score value should be 1")

	kubeconfig, err := clientcmd.Load(secret.Data["kubeconfig"])
	assert.Nil(t, err, "is nil when kubeconfig data can be loaded")
	assert.Equal(t, kubeconfig.Clusters["cluster"].Server, "https://kube-apiserver."+hc.Namespace+"-"+hc.Name+".svc.cluster.local:443")

	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.PlacementScoreFailureCount))
	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.PlacementClusterClaimsFailureCount.WithLabelValues(util.MetricsLabelFullClusterClaim)))
	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.PlacementClusterClaimsFailureCount.WithLabelValues(util.MetricsLabelThresholdClusterClaim)))
	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.PlacementClusterClaimsFailureCount.WithLabelValues(util.MetricsLabelZeroClusterClaim)))

	// Delete hosted cluster and reconcile
	aCtrl.hubClient.Delete(ctx, hc)
	_, err = aCtrl.Reconcile(ctx, ctrl.Request{NamespacedName: hcNN})
	assert.Nil(t, err, "err nil when reconcile was successfully")

	err = aCtrl.hubClient.Get(ctx, kcSecretNN, secret)
	assert.True(t, err != nil && errors.IsNotFound(err), "is true when the admin kubeconfig secret is deleted")
	err = aCtrl.hubClient.Get(ctx, pwdSecretNN, secret)
	assert.True(t, err != nil && errors.IsNotFound(err), "is nil when the kubeadmin password secret is deleted")
}

func TestHostedClusterCount(t *testing.T) {
	ctx := context.Background()
	client := initClient()
	zapLog, _ := zap.NewDevelopment()

	fakeClusterCS := clustercsfake.NewSimpleClientset()

	aCtrl := &agentController{
		spokeClustersClient:         fakeClusterCS,
		spokeUncachedClient:         client,
		spokeClient:                 client,
		hubClient:                   client,
		log:                         zapr.NewLogger(zapLog),
		maxHostedClusterCount:       5,
		thresholdHostedClusterCount: 3,
	}

	err := aCtrl.SyncAddOnPlacementScore(ctx, false)
	assert.Nil(t, err, "err nil when CreateAddOnPlacementScore was successfully")

	// No HC yet, so the zero cluster claim value should be true
	zeroClusterClaim, err := aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountZeroClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the hc count zero clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(true), zeroClusterClaim.Spec.Value)

	hcNN := types.NamespacedName{Name: "hd-1", Namespace: "clusters"}

	i := 0
	for i < (aCtrl.maxHostedClusterCount - 1) {
		hc := getHostedCluster(hcNN)
		hc.SetName("test-" + strconv.Itoa(i))
		err := aCtrl.hubClient.Create(ctx, hc)
		defer aCtrl.hubClient.Delete(ctx, hc)
		assert.Nil(t, err, "err nil when hosted cluster is created successfully")
		i++
	}

	err = aCtrl.SyncAddOnPlacementScore(ctx, false)
	assert.Nil(t, err, "err nil when CreateAddOnPlacementScore was successfully")

	// Created 4 HCs, max 5 so the full cluster claim value should be false
	fullClusterClaim, err := aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountFullClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the hc count full clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(false), fullClusterClaim.Spec.Value)

	// Created 4 HCs, threshold 3 so the threshold cluster claim value should be true
	thresholdClusterClaim, err := aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountAboveThresholdClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the hc count at threshold clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(true), thresholdClusterClaim.Spec.Value)

	// Created 4 HCs, so the zero cluster claim value should be false
	zeroClusterClaim, err = aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountZeroClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the hc count zero clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(false), zeroClusterClaim.Spec.Value)

	placementScore := &clusterv1alpha1.AddOnPlacementScore{}
	placementScoreNN := types.NamespacedName{Name: util.HostedClusterScoresResourceName, Namespace: aCtrl.clusterName}
	err = aCtrl.hubClient.Get(ctx, placementScoreNN, placementScore)
	assert.Nil(t, err, "is nil when addonPlacementScore is found")
	assert.Equal(t, int32(aCtrl.maxHostedClusterCount-1), placementScore.Status.Scores[0].Value)

	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.PlacementScoreFailureCount))
	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.PlacementClusterClaimsFailureCount.WithLabelValues(util.MetricsLabelFullClusterClaim)))
	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.PlacementClusterClaimsFailureCount.WithLabelValues(util.MetricsLabelThresholdClusterClaim)))
	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.PlacementClusterClaimsFailureCount.WithLabelValues(util.MetricsLabelZeroClusterClaim)))
	assert.Equal(t, float64(4), testutil.ToFloat64(metrics.TotalHostedClusterGauge))
	assert.Equal(t, float64(4), testutil.ToFloat64(metrics.HostedClusterAvailableGauge))

	// Create one more hosted cluster and expect the cluserclaim to have hostedclustercount.full.hypershift.openshift.io=true
	// indicating it reached the maximum number of hosted clusters
	hc := getHostedCluster(hcNN)
	hc.SetName("test-80")
	err = aCtrl.hubClient.Create(ctx, hc)
	assert.Nil(t, err, "err nil when hosted cluster is created successfully")

	err = aCtrl.SyncAddOnPlacementScore(ctx, false)
	assert.Nil(t, err, "err nil when CreateAddOnPlacementScore was successfully")

	// 5 HCs, max 5 so the full cluster claim value should be true
	fullClusterClaim, err = aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountFullClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(true), fullClusterClaim.Spec.Value)

	// Created 5 HCs, threshold 3 so the threshold cluster claim value should be true
	thresholdClusterClaim, err = aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountAboveThresholdClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the hc count at threshold clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(true), thresholdClusterClaim.Spec.Value)

	// Created 5 HCs, so the zero cluster claim value should be false
	zeroClusterClaim, err = aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountZeroClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the hc count zero clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(false), zeroClusterClaim.Spec.Value)

	err = aCtrl.hubClient.Get(ctx, placementScoreNN, placementScore)
	assert.Nil(t, err, "is nil when addonPlacementScore is found")
	assert.Equal(t, int32(aCtrl.maxHostedClusterCount), placementScore.Status.Scores[0].Value)

	assert.Equal(t, float64(5), testutil.ToFloat64(metrics.TotalHostedClusterGauge))
	assert.Equal(t, float64(5), testutil.ToFloat64(metrics.HostedClusterAvailableGauge))

	// Delete one hosted cluster and expect the cluserclaim to have hostedclustercount.full.hypershift.openshift.io=false
	// indicating it did not reach the maximum number of hosted clusters after removing one
	err = aCtrl.hubClient.Delete(ctx, hc)
	assert.Nil(t, err, "err nil when hosted cluster is deleted successfully")

	err = aCtrl.SyncAddOnPlacementScore(ctx, false)
	assert.Nil(t, err, "err nil when CreateAddOnPlacementScore was successfully")

	fullClusterClaim, err = aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountFullClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(false), fullClusterClaim.Spec.Value)

	// Created 4 HCs, threshold 3 so the threshold cluster claim value should be true
	thresholdClusterClaim, err = aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountAboveThresholdClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the hc count at threshold clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(true), thresholdClusterClaim.Spec.Value)

	// Created 4 HCs, so the zero cluster claim value should be false
	zeroClusterClaim, err = aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountZeroClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the hc count zero clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(false), zeroClusterClaim.Spec.Value)

	err = aCtrl.hubClient.Get(ctx, placementScoreNN, placementScore)
	assert.Nil(t, err, "is nil when addonPlacementScore is found")
	assert.Equal(t, int32(aCtrl.maxHostedClusterCount-1), placementScore.Status.Scores[0].Value)

	hcNN = types.NamespacedName{Name: "test-3", Namespace: "clusters"}
	hc = &hyperv1beta1.HostedCluster{}
	err = aCtrl.hubClient.Get(ctx, hcNN, hc)
	assert.Nil(t, err, "err nil when hosted cluster is found")
	err = aCtrl.hubClient.Delete(ctx, hc)
	assert.Nil(t, err, "err nil when hosted cluster is deleted successfully")

	err = aCtrl.SyncAddOnPlacementScore(ctx, false)
	assert.Nil(t, err, "err nil when CreateAddOnPlacementScore was successfully")

	// 3 HCs, threshold 3 so the threshold cluster claim value should be true
	thresholdClusterClaim, err = aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountAboveThresholdClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the hc count at threshold clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(true), thresholdClusterClaim.Spec.Value)

	hcNN = types.NamespacedName{Name: "test-2", Namespace: "clusters"}
	hc = &hyperv1beta1.HostedCluster{}
	err = aCtrl.hubClient.Get(ctx, hcNN, hc)
	assert.Nil(t, err, "err nil when hosted cluster is found")
	err = aCtrl.hubClient.Delete(ctx, hc)
	assert.Nil(t, err, "err nil when hosted cluster is deleted successfully")

	err = aCtrl.SyncAddOnPlacementScore(ctx, false)
	assert.Nil(t, err, "err nil when CreateAddOnPlacementScore was successfully")

	// 2 HCs, threshold 3 so the threshold cluster claim value should be true
	thresholdClusterClaim, err = aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountAboveThresholdClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the hc count at threshold clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(false), thresholdClusterClaim.Spec.Value)

	hcNN = types.NamespacedName{Name: "test-1", Namespace: "clusters"}
	hc = &hyperv1beta1.HostedCluster{}
	err = aCtrl.hubClient.Get(ctx, hcNN, hc)
	assert.Nil(t, err, "err nil when hosted cluster is found")
	err = aCtrl.hubClient.Delete(ctx, hc)
	assert.Nil(t, err, "err nil when hosted cluster is deleted successfully")

	hcNN = types.NamespacedName{Name: "test-0", Namespace: "clusters"}
	hcNN.Name = "test-0"
	hc = &hyperv1beta1.HostedCluster{}
	err = aCtrl.hubClient.Get(ctx, hcNN, hc)
	assert.Nil(t, err, "err nil when hosted cluster is found")
	err = aCtrl.hubClient.Delete(ctx, hc)
	assert.Nil(t, err, "err nil when hosted cluster is deleted successfully")

	err = aCtrl.SyncAddOnPlacementScore(ctx, false)
	assert.Nil(t, err, "err nil when CreateAddOnPlacementScore was successfully")

	// 0 HC, max 5 so the full cluster claim value should be false
	fullClusterClaim, err = aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountFullClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(false), fullClusterClaim.Spec.Value)

	// 0 HC, threshold 3 so the threshold cluster claim value should be false
	thresholdClusterClaim, err = aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountAboveThresholdClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the hc count at threshold clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(false), thresholdClusterClaim.Spec.Value)

	// 0 HC, so the zero cluster claim value should be true
	zeroClusterClaim, err = aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountZeroClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the hc count zero clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(true), zeroClusterClaim.Spec.Value)

	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.TotalHostedClusterGauge))
	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.HostedClusterAvailableGauge))
}

func TestHostedClusterCountErrorCase(t *testing.T) {
	ctx := context.Background()
	client := initClient()
	zapLog, _ := zap.NewDevelopment()

	fakeClusterCS := clustercsfake.NewSimpleClientset()

	aCtrl := &agentController{
		spokeClustersClient:         fakeClusterCS,
		spokeUncachedClient:         client,
		spokeClient:                 client,
		hubClient:                   client,
		log:                         zapr.NewLogger(zapLog),
		maxHostedClusterCount:       80,
		thresholdHostedClusterCount: 60,
	}

	hcNN := types.NamespacedName{Name: "hd-1", Namespace: "clusters"}

	i := 10
	for i < 15 {
		hc := getHostedCluster(hcNN)
		hc.SetName("test-" + strconv.Itoa(i))
		err := aCtrl.hubClient.Create(ctx, hc)
		defer aCtrl.hubClient.Delete(ctx, hc)
		assert.Nil(t, err, "err nil when hosted cluster is created successfully")
		i++
	}

	err := aCtrl.SyncAddOnPlacementScore(ctx, false)
	assert.Nil(t, err, "err nil when CreateAddOnPlacementScore was successfully")

	clusterClaim, err := aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountFullClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(false), clusterClaim.Spec.Value)

	placementScore := &clusterv1alpha1.AddOnPlacementScore{}
	placementScoreNN := types.NamespacedName{Name: util.HostedClusterScoresResourceName, Namespace: aCtrl.clusterName}
	err = aCtrl.hubClient.Get(ctx, placementScoreNN, placementScore)
	assert.Nil(t, err, "is nil when addonPlacementScore is found")
	assert.Equal(t, int32(5), placementScore.Status.Scores[0].Value)

	// Simulate that it fails to get a list of all hosted clusters. In this case, the addonPlacementScore
	// should contain a condition indicating the failure but the existing score should not change
	// The score should still be 5
	aCtrl.spokeUncachedClient = initErrorClient()
	err = aCtrl.SyncAddOnPlacementScore(ctx, false)
	assert.Nil(t, err, "err nil when CreateAddOnPlacementScore was successfully")

	err = aCtrl.hubClient.Get(ctx, placementScoreNN, placementScore)
	assert.Nil(t, err, "is nil when addonPlacementScore is found")
	assert.Equal(t, metav1.ConditionFalse, placementScore.Status.Conditions[0].Status)
	assert.Equal(t, int32(5), placementScore.Status.Scores[0].Value)

	fullClusterClaim, err := aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountFullClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(false), fullClusterClaim.Spec.Value)

	thresholdClusterClaim, err := aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountAboveThresholdClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the hc count at threshold clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(false), thresholdClusterClaim.Spec.Value)

	zeroClusterClaim, err := aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountZeroClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the hc count zero clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(false), zeroClusterClaim.Spec.Value)

	assert.Equal(t, float64(5), testutil.ToFloat64(metrics.TotalHostedClusterGauge))
	assert.Equal(t, float64(5), testutil.ToFloat64(metrics.HostedClusterAvailableGauge))
}
func TestHostedClusterCountStartupErrorCase(t *testing.T) {
	ctx := context.Background()
	client := initErrorClient()
	zapLog, _ := zap.NewDevelopment()

	fakeClusterCS := clustercsfake.NewSimpleClientset()

	aCtrl := &agentController{
		spokeClustersClient:         fakeClusterCS,
		spokeUncachedClient:         client,
		spokeClient:                 client,
		hubClient:                   client,
		log:                         zapr.NewLogger(zapLog),
		maxHostedClusterCount:       80,
		thresholdHostedClusterCount: 60,
	}

	// This tests SyncAddOnPlacementScore call during agent startup with no hypershift operator installation on the cluster yet.
	err := aCtrl.SyncAddOnPlacementScore(ctx, true)
	assert.Nil(t, err, "err nil when CreateAddOnPlacementScore was successfully")

	clusterClaim, err := aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountFullClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(false), clusterClaim.Spec.Value)

	thresholdClusterClaim, err := aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountAboveThresholdClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the hc count at threshold clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(false), thresholdClusterClaim.Spec.Value)

	zeroClusterClaim, err := aCtrl.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountZeroClusterClaimKey, metav1.GetOptions{})
	assert.Nil(t, err, "is nil when the hc count zero clusterclaim is found")
	assert.Equal(t, strconv.FormatBool(true), zeroClusterClaim.Spec.Value)

	placementScore := &clusterv1alpha1.AddOnPlacementScore{}
	placementScoreNN := types.NamespacedName{Name: util.HostedClusterScoresResourceName, Namespace: aCtrl.clusterName}
	err = aCtrl.hubClient.Get(ctx, placementScoreNN, placementScore)
	assert.Nil(t, err, "is nil when addonPlacementScore is found")
	assert.Equal(t, int32(0), placementScore.Status.Scores[0].Value)

	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.TotalHostedClusterGauge))
	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.HostedClusterAvailableGauge))
}

func getHostedCluster(hcNN types.NamespacedName) *hyperv1beta1.HostedCluster {
	hc := &hyperv1beta1.HostedCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "HostedCluster",
			APIVersion: "hypershift.openshift.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      hcNN.Name,
			Namespace: hcNN.Namespace,
		},
		Spec: hyperv1beta1.HostedClusterSpec{
			Platform: hyperv1beta1.PlatformSpec{
				Type: hyperv1beta1.AWSPlatform,
			},
			Networking: hyperv1beta1.ClusterNetworking{
				NetworkType: hyperv1beta1.OpenShiftSDN,
			},
			Services: []hyperv1beta1.ServicePublishingStrategyMapping{},
			Release: hyperv1beta1.Release{
				Image: "test-image",
			},
			Etcd: hyperv1beta1.EtcdSpec{
				ManagementType: hyperv1beta1.Managed,
			},
			InfraID: "infra-abcdef",
		},
		Status: hyperv1beta1.HostedClusterStatus{
			KubeConfig:        &corev1.LocalObjectReference{Name: "kubeconfig"},
			KubeadminPassword: &corev1.LocalObjectReference{Name: "kubeadmin"},
			Conditions:        []metav1.Condition{{Type: string(hyperv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue, Reason: hyperv1beta1.AsExpectedReason}},
			Version: &hyperv1beta1.ClusterVersionStatus{
				History: []configv1.UpdateHistory{{State: configv1.CompletedUpdate}},
			},
		},
	}
	return hc
}

func TestAgentCommand(t *testing.T) {
	zapLog, _ := zap.NewDevelopment()
	cleanupCmd := NewAgentCommand("operator", zapr.NewLogger(zapLog))
	assert.Equal(t, "agent", cleanupCmd.Use)
}

func TestCleanupCommand(t *testing.T) {
	ctx := context.Background()
	zapLog, _ := zap.NewDevelopment()
	cleanupCmd := NewCleanupCommand("operator", zapr.NewLogger(zapLog))
	assert.Equal(t, "cleanup", cleanupCmd.Use)

	// Cleanup
	// Hypershift deployment is not deleted because there is an existing hostedcluster
	o := &AgentOptions{
		Log:            zapr.NewLogger(zapLog),
		AddonName:      "hypershift-addon",
		AddonNamespace: "hypershift",
	}

	err := o.runCleanup(ctx, nil)
	assert.Nil(t, err, "is nil if cleanup is succcessful")
}

func TestRunControllerManager(t *testing.T) {
	ctx := context.Background()
	zapLog, _ := zap.NewDevelopment()
	o := &AgentOptions{
		Log:            zapr.NewLogger(zapLog),
		AddonName:      "hypershift-addon",
		AddonNamespace: "hypershift",
	}

	err := o.runControllerManager(ctx)
	assert.NotNil(t, err, "err it not nil if the controller fail to run")
}

func initClient() client.Client {
	scheme := runtime.NewScheme()
	//corev1.AddToScheme(scheme)
	appsv1.AddToScheme(scheme)
	corev1.AddToScheme(scheme)
	metav1.AddMetaToScheme(scheme)
	hyperv1beta1.AddToScheme(scheme)
	clusterv1alpha1.AddToScheme(scheme)

	ncb := fake.NewClientBuilder()
	ncb.WithScheme(scheme)
	return ncb.Build()

}

func initErrorClient() client.Client {
	scheme := runtime.NewScheme()
	//corev1.AddToScheme(scheme)
	appsv1.AddToScheme(scheme)
	corev1.AddToScheme(scheme)
	metav1.AddMetaToScheme(scheme)
	clusterv1alpha1.AddToScheme(scheme)

	ncb := fake.NewClientBuilder()
	ncb.WithScheme(scheme)
	return ncb.Build()

}
