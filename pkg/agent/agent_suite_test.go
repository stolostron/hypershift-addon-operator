package agent

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/go-logr/zapr"
	configv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"
	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stolostron/hypershift-addon-operator/pkg/install"
	"github.com/stolostron/hypershift-addon-operator/pkg/metrics"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	"github.com/stolostron/klusterlet-addon-controller/pkg/apis"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	schemes "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clustercsfake "open-cluster-management.io/api/client/cluster/clientset/versioned/fake"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	clusterv1alpha1 "open-cluster-management.io/api/cluster/v1alpha1"
	operatorapiv1 "open-cluster-management.io/api/operator/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

type AgentTestSuite struct {
	suite.Suite
	testKubeConfig *rest.Config
	testKubeClient client.Client
	controller     *agentController
	errController  *agentController
	t              *envtest.Environment
}

func (suite *AgentTestSuite) SetupSuite() {
	suite.t = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "hack", "crds"),
		},
	}

	apis.AddToScheme(schemes.Scheme)
	appsv1.AddToScheme(schemes.Scheme)
	corev1.AddToScheme(schemes.Scheme)
	metav1.AddMetaToScheme(schemes.Scheme)
	hyperv1beta1.AddToScheme(schemes.Scheme)
	clusterv1alpha1.AddToScheme(schemes.Scheme)
	clusterv1.AddToScheme(schemes.Scheme)
	operatorapiv1.AddToScheme(schemes.Scheme)
	addonv1alpha1.AddToScheme(schemes.Scheme)
	routev1.AddToScheme(schemes.Scheme)

	var err error
	if suite.testKubeConfig, err = suite.t.Start(); err != nil {
		log.Fatal(err)
	}

	if suite.testKubeClient, err = client.New(suite.testKubeConfig, client.Options{Scheme: schemes.Scheme}); err != nil {
		log.Fatal(err)
	}

	zapLog, _ := zap.NewDevelopment()

	suite.controller = &agentController{
		spokeClustersClient: clustercsfake.NewSimpleClientset(),
		spokeUncachedClient: suite.testKubeClient,
		spokeClient:         suite.testKubeClient,
		hubClient:           suite.testKubeClient,
		log:                 zapr.NewLogger(zapLog),
		clusterName:         "local-cluster",
	}

	errClient := initReconcileErrorClient()
	suite.errController = &agentController{
		spokeClustersClient: clustercsfake.NewSimpleClientset(),
		spokeUncachedClient: errClient,
		spokeClient:         errClient,
		hubClient:           errClient,
		log:                 zapr.NewLogger(zapLog),
		clusterName:         "local-cluster",
	}

	err = suite.testKubeClient.Create(context.TODO(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "local-cluster"},
	})
	if err != nil {
		log.Fatal(err)
	}

	err = suite.testKubeClient.Create(context.TODO(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "spoke-1"},
	})
	if err != nil {
		log.Fatal(err)
	}

	err = suite.testKubeClient.Create(context.TODO(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "clusters"},
	})
	if err != nil {
		log.Fatal(err)
	}
}

func (suite *AgentTestSuite) TearDownSuite() {
	suite.t.Stop()
}

func (suite *AgentTestSuite) SetupTest() {
}

func (suite *AgentTestSuite) TearDownTest() {
}

func (suite *AgentTestSuite) createHCResources(hcName string, hcAnnotations map[string]string, specConfig *hyperv1beta1.ClusterConfiguration) *hyperv1beta1.HostedCluster {
	hcNN := types.NamespacedName{Name: hcName, Namespace: "clusters"}
	secrets := suite.controller.scaffoldHostedclusterSecrets(hcNN)

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
		suite.testKubeClient.Create(context.TODO(), sec)
	}

	err := suite.testKubeClient.Create(context.TODO(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "clusters-" + hcName},
	})
	if err != nil {
		log.Fatal(err)
	}

	apiService := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: "clusters-" + hcName,
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

	err = suite.controller.hubClient.Create(context.TODO(), apiService)
	if err != nil {
		log.Fatal(err)
	}

	// Create hosted cluster
	hc := getHostedCluster(hcNN)
	if hcAnnotations != nil {
		hc.Annotations = hcAnnotations
	}
	hc.Spec.Configuration = specConfig
	err = suite.controller.hubClient.Create(context.TODO(), hc)
	if err != nil {
		log.Fatal(err)
	}

	return hc
}

func (suite *AgentTestSuite) deleteHCResources(hcName string) {
	hcNN := types.NamespacedName{Name: hcName, Namespace: "clusters"}

	hc := &hyperv1beta1.HostedCluster{}
	err := suite.controller.hubClient.Get(context.TODO(), hcNN, hc)
	suite.Nil(err, "err nil when hosted cluster is found successfully")

	// Delete hosted cluster and reconcile
	suite.controller.hubClient.Delete(context.TODO(), hc)

	suite.Eventually(func() bool {
		hcToDelete := &hyperv1beta1.HostedCluster{}
		err := suite.controller.hubClient.Get(context.TODO(), hcNN, hcToDelete)
		return err != nil && errors.IsNotFound(err)
	}, 5*time.Second, 500*time.Millisecond)

}

func (suite *AgentTestSuite) TestReconcileRequeue() {
	fmt.Println("From TestReconcileRequeue")

	hcName := "test-1"

	suite.createHCResources(hcName, nil, &hyperv1beta1.ClusterConfiguration{})

	hcNN := types.NamespacedName{Name: hcName, Namespace: "clusters"}

	hc := &hyperv1beta1.HostedCluster{}
	err := suite.controller.hubClient.Get(context.TODO(), hcNN, hc)
	suite.Nil(err, "err nil when hosted cluster is found successfully")

	newStatus := &hyperv1beta1.HostedClusterStatus{
		Conditions: []metav1.Condition{{Type: string(hyperv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue, Reason: hyperv1beta1.AsExpectedReason, LastTransitionTime: metav1.NewTime(time.Now())}},
		KubeConfig: &corev1.LocalObjectReference{Name: "kubeconfig"},
		Version: &hyperv1beta1.ClusterVersionStatus{
			History: []configv1.UpdateHistory{{State: configv1.CompletedUpdate, StartedTime: metav1.NewTime(time.Now())}},
		},
	}

	hc.Status = *newStatus

	err = suite.controller.hubClient.Status().Update(context.TODO(), hc, &client.SubResourceUpdateOptions{})
	suite.Nil(err, "err nil when hosted cluster status is found successfully")

	// Reconcile with annotation
	res, err := suite.controller.Reconcile(context.TODO(), ctrl.Request{NamespacedName: hcNN})
	suite.Nil(err, "err nil when reconcile was successfully")

	// Secret for kubconfig is created
	// external-managed-kubeconfig could not be created because there is no klusterlet namespace
	secret := &corev1.Secret{}
	kcExtSecretNN := types.NamespacedName{Name: "external-managed-kubeconfig", Namespace: "klusterlet-" + hc.Name}
	err = suite.controller.hubClient.Get(context.TODO(), kcExtSecretNN, secret)
	suite.NotNil(err, "external-managed-kubeconfig secret not found")
	suite.Equal(true, res.Requeue)
	suite.Equal(1*time.Minute, res.RequeueAfter)
	// Test that we do not count the klusterlet namespace missing as an error, this just means import has not been
	// triggered
	suite.Equal(float64(0), testutil.ToFloat64(metrics.KubeconfigSecretCopyFailureCount))

	// Delete the hosted cluster here so it does not affect subsequent tests
	suite.deleteHCResources(hcName)
}

func (suite *AgentTestSuite) TestReconcile() {
	hcName := "test-2"

	suite.createHCResources(hcName, nil, &hyperv1beta1.ClusterConfiguration{})

	hc := &hyperv1beta1.HostedCluster{}
	err := suite.controller.hubClient.Get(context.TODO(), types.NamespacedName{Name: hcName, Namespace: "clusters"}, hc)
	suite.Nil(err, "err nil when hosted cluster is found successfully")

	newStatus := &hyperv1beta1.HostedClusterStatus{
		Conditions: []metav1.Condition{{Type: string(hyperv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue, Reason: hyperv1beta1.AsExpectedReason, LastTransitionTime: metav1.NewTime(time.Now())}},
		KubeConfig: &corev1.LocalObjectReference{Name: "kubeconfig"},
		Version: &hyperv1beta1.ClusterVersionStatus{
			History: []configv1.UpdateHistory{{State: configv1.CompletedUpdate, StartedTime: metav1.NewTime(time.Now())}},
		},
	}

	hc.Status = *newStatus

	err = suite.controller.hubClient.Status().Update(context.TODO(), hc, &client.SubResourceUpdateOptions{})
	suite.Nil(err, "err nil when hosted cluster status is found successfully")

	// Create klusterlet namespace
	klusterletNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "klusterlet-" + hc.Name,
		},
	}
	err = suite.controller.hubClient.Create(context.TODO(), klusterletNamespace)
	suite.Nil(err, "err nil when klusterletNamespace was created successfully")

	// Reconcile with annotation
	_, err = suite.controller.Reconcile(context.TODO(), ctrl.Request{NamespacedName: types.NamespacedName{Name: hcName, Namespace: "clusters"}})
	suite.Nil(err, "err nil when reconcile was successfully")

	// Secret for kubconfig is created
	secret := &corev1.Secret{}
	kcSecretNN := types.NamespacedName{Name: fmt.Sprintf("%s-admin-kubeconfig", hc.Name), Namespace: suite.controller.clusterName}
	err = suite.controller.hubClient.Get(context.TODO(), kcSecretNN, secret)
	suite.Nil(err, "is nil when the admin kubeconfig secret is found")

	// The hosted cluster does not have status.KubeadminPassword so the kubeadmin-password is not expected to be copied
	pwdSecretNN := types.NamespacedName{Name: fmt.Sprintf("%s-kubeadmin-password", hc.Name), Namespace: suite.controller.clusterName}
	err = suite.controller.hubClient.Get(context.TODO(), pwdSecretNN, secret)
	suite.True(err != nil && errors.IsNotFound(err), "is true when the kubeadmin-password secret is not copied")

	kcExtSecretNN := types.NamespacedName{Name: "external-managed-kubeconfig", Namespace: "klusterlet-" + hc.Name}
	err = suite.controller.hubClient.Get(context.TODO(), kcExtSecretNN, secret)
	suite.Nil(err, "is nil when external-managed-kubeconfig secret is found")

	kubeconfig, err := clientcmd.Load(secret.Data["kubeconfig"])
	suite.Nil(err, "is nil when kubeconfig data can be loaded")
	suite.Equal(kubeconfig.Clusters["cluster"].Server, "https://kube-apiserver."+hc.Namespace+"-"+hc.Name+".svc.cluster.local:443")

	suite.Equal(float64(0), testutil.ToFloat64(metrics.KubeconfigSecretCopyFailureCount))
	suite.Equal(float64(1), testutil.ToFloat64(metrics.TotalHostedClusterGauge))
	suite.Equal(float64(1), testutil.ToFloat64(metrics.HostedClusterAvailableGauge))

	hc1 := &hyperv1beta1.HostedCluster{}
	err = suite.controller.hubClient.Get(context.TODO(), types.NamespacedName{Namespace: hc.Namespace, Name: hc.Name}, hc1)
	suite.Nil(err, "err nil when hosted cluster is found successfully")

	// Add the kubeadmin password to the hosted cluster CR to test that the kubeadmin-password is copied
	newStatus = &hyperv1beta1.HostedClusterStatus{
		Conditions: []metav1.Condition{{Type: string(hyperv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue, Reason: hyperv1beta1.AsExpectedReason, LastTransitionTime: metav1.NewTime(time.Now())}},
		KubeConfig: &corev1.LocalObjectReference{Name: "kubeconfig"},
		Version: &hyperv1beta1.ClusterVersionStatus{
			History: []configv1.UpdateHistory{{State: configv1.CompletedUpdate, StartedTime: metav1.NewTime(time.Now())}},
		},
		KubeadminPassword: &corev1.LocalObjectReference{Name: "kubeadmin-password"},
	}
	hc1.Status = *newStatus

	err = suite.controller.hubClient.Status().Update(context.TODO(), hc1, &client.SubResourceUpdateOptions{})
	suite.Nil(err, "err nil when hosted cluster was updated successfully")

	_, err = suite.controller.Reconcile(context.TODO(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: hc.Namespace, Name: hc.Name}})
	suite.Nil(err, "err nil when reconcile was successfully")

	// The hosted cluster now has status.KubeadminPassword so the kubeadmin-password is expected to be copied
	err = suite.controller.hubClient.Get(context.TODO(), pwdSecretNN, secret)
	suite.Nil(err, "is nil when the kubeadmin-password secret is found")

	// Delete hosted cluster and reconcile
	suite.controller.hubClient.Delete(context.TODO(), hc)

	suite.Eventually(func() bool {
		hcToDelete := &hyperv1beta1.HostedCluster{}
		err := suite.controller.hubClient.Get(context.TODO(), types.NamespacedName{Namespace: "clusters", Name: hcName}, hcToDelete)
		return err != nil && errors.IsNotFound(err)
	}, 5*time.Second, 500*time.Millisecond)

	_, err = suite.controller.Reconcile(context.TODO(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: hc.Namespace, Name: hc.Name}})
	suite.Nil(err, "err nil when reconcile was successfully")

	err = suite.controller.hubClient.Get(context.TODO(), kcSecretNN, secret)
	suite.True(err != nil && errors.IsNotFound(err), "is true when the admin kubeconfig secret is deleted")
	err = suite.controller.hubClient.Get(context.TODO(), pwdSecretNN, secret)
	suite.True(err != nil && errors.IsNotFound(err), "is nil when the kubeadmin password secret is deleted")
}

func (suite *AgentTestSuite) TestReconcileRequeueFromFailedReconcile() {
	hcName := "test-3"
	hcNN := types.NamespacedName{Name: hcName, Namespace: "clusters"}

	suite.createHCResources(hcName, nil, &hyperv1beta1.ClusterConfiguration{})

	// Reconcile
	res, err := suite.errController.Reconcile(ctx, ctrl.Request{NamespacedName: hcNN})
	suite.Nil(err, "err nil when reconcile was successfully")

	// Could not generate AddOnPlacementScore so the reconcile should be requeued
	suite.Equal(true, res.Requeue)
	suite.Equal(1*time.Minute, res.RequeueAfter)
	// Test that we do not count the klusterlet namespace missing as an error, this just means import has not been
	// triggered
	suite.Equal(float64(1), testutil.ToFloat64(metrics.PlacementScoreFailureCount))

	// Delete the hosted cluster here so it does not affect subsequent tests
	suite.deleteHCResources(hcName)
}

func (suite *AgentTestSuite) TestReconcileWithAnnotation() {
	hcName := "test-4"
	hcNN := types.NamespacedName{Name: hcName, Namespace: "clusters"}
	annotations := map[string]string{util.ManagedClusterAnnoKey: hcName + "-abcdef"}
	config := &hyperv1beta1.ClusterConfiguration{
		APIServer: &configv1.APIServerSpec{
			ServingCerts: configv1.APIServerServingCerts{
				NamedCertificates: []configv1.APIServerNamedServingCert{
					{
						ServingCertificate: configv1.SecretNameReference{
							Name: "test-tls",
						},
					},
				},
			},
		},
	}

	suite.createHCResources(hcName, annotations, config)

	ctx := context.TODO()

	hc := &hyperv1beta1.HostedCluster{}
	err := suite.controller.hubClient.Get(ctx, types.NamespacedName{Namespace: "clusters", Name: hcName}, hc)
	suite.Nil(err, "err nil when hosted cluster is found successfully")

	newStatus := &hyperv1beta1.HostedClusterStatus{
		Conditions: []metav1.Condition{{Type: string(hyperv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue, Reason: hyperv1beta1.AsExpectedReason, LastTransitionTime: metav1.NewTime(time.Now())}},
		KubeConfig: &corev1.LocalObjectReference{Name: "kubeconfig"},
		Version: &hyperv1beta1.ClusterVersionStatus{
			History: []configv1.UpdateHistory{{State: configv1.CompletedUpdate, StartedTime: metav1.NewTime(time.Now())}},
		},
	}

	hc.Status = *newStatus

	err = suite.controller.hubClient.Status().Update(ctx, hc, &client.SubResourceUpdateOptions{})
	suite.Nil(err, "err nil when hosted cluster status is found successfully")

	// Create klusterlet namespace
	klusterletNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "klusterlet-" + hc.Spec.InfraID,
		},
	}
	err = suite.controller.hubClient.Create(ctx, klusterletNamespace)
	suite.Nil(err, "err nil when klusterletNamespace was created successfully")
	defer suite.controller.hubClient.Delete(ctx, klusterletNamespace)

	tlsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-tls",
			Namespace: "clusters",
		},
		Data: map[string][]byte{"tls.crt": []byte("replaced-crt")},
	}

	err = suite.controller.hubClient.Create(ctx, tlsSecret)
	suite.Nil(err, "tls secret created successfully")

	// Reconcile with no annotation
	_, err = suite.controller.Reconcile(ctx, ctrl.Request{NamespacedName: hcNN})
	suite.Nil(err, "err nil when reconcile was successfully")

	// Secret for kubconfig and kubeadmin-password are created
	secret := &corev1.Secret{}
	kcSecretNN := types.NamespacedName{Name: fmt.Sprintf("%s-admin-kubeconfig", hc.Spec.InfraID), Namespace: suite.controller.clusterName}
	err = suite.controller.spokeClient.Get(ctx, kcSecretNN, secret)
	suite.Nil(err, "is nil when the admin kubeconfig secret is found")

	// The hosted cluster does not have status.KubeadminPassword so the kubeadmin-password is not expected to be copied
	pwdSecretNN := types.NamespacedName{Name: fmt.Sprintf("%s-kubeadmin-password", hc.Name), Namespace: suite.controller.clusterName}
	err = suite.controller.hubClient.Get(ctx, pwdSecretNN, secret)
	suite.True(err != nil && errors.IsNotFound(err), "is true when the kubeadmin-password secret is not copied")

	kcExtSecretNN := types.NamespacedName{Name: "external-managed-kubeconfig", Namespace: "klusterlet-" + hc.Spec.InfraID}
	err = suite.controller.hubClient.Get(ctx, kcExtSecretNN, secret)
	suite.Nil(err, "is nil when external-managed-kubeconfig secret is found")

	addOnPlacementScore := &clusterv1alpha1.AddOnPlacementScore{}
	addOnPlacementScoreNN := types.NamespacedName{Name: util.HostedClusterScoresResourceName, Namespace: suite.controller.clusterName}
	err = suite.controller.hubClient.Get(ctx, addOnPlacementScoreNN, addOnPlacementScore)
	suite.Nil(err, "is nil when hosted-clusters-score AddOnPlacementScore is found")
	suite.Equal(util.HostedClusterScoresScoreName, addOnPlacementScore.Status.Scores[0].Name, "hosted-clusters-score AddOnPlacementScore score name should be "+util.HostedClusterScoresScoreName)
	suite.Equal(int32(1), addOnPlacementScore.Status.Scores[0].Value, "hosted-clusters-score AddOnPlacementScore score value should be 1")

	kubeconfig, err := clientcmd.Load(secret.Data["kubeconfig"])
	suite.Nil(err, "is nil when kubeconfig data can be loaded")
	suite.Equal(kubeconfig.Clusters["cluster"].Server, "https://kube-apiserver."+hc.Namespace+"-"+hc.Name+".svc.cluster.local:443")

	suite.Equal(float64(0), testutil.ToFloat64(metrics.PlacementClusterClaimsFailureCount.WithLabelValues(util.MetricsLabelFullClusterClaim)))
	suite.Equal(float64(0), testutil.ToFloat64(metrics.PlacementClusterClaimsFailureCount.WithLabelValues(util.MetricsLabelThresholdClusterClaim)))
	suite.Equal(float64(0), testutil.ToFloat64(metrics.PlacementClusterClaimsFailureCount.WithLabelValues(util.MetricsLabelZeroClusterClaim)))

	// Delete hosted cluster and reconcile
	hc.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	suite.controller.hubClient.Update(ctx, hc)
	_, err = suite.controller.Reconcile(ctx, ctrl.Request{NamespacedName: hcNN})
	suite.Nil(err, "err nil when reconcile was successfully")

	suite.deleteHCResources(hcName)

	_, err = suite.controller.Reconcile(ctx, ctrl.Request{NamespacedName: hcNN})
	suite.Nil(err, "err nil when reconcile was successfully")

	err = suite.controller.hubClient.Get(ctx, kcSecretNN, secret)
	suite.True(err != nil && errors.IsNotFound(err), "is true when the admin kubeconfig secret is deleted")
	err = suite.controller.hubClient.Get(ctx, pwdSecretNN, secret)
	suite.True(err != nil && errors.IsNotFound(err), "is nil when the kubeadmin password secret is deleted")
}

func (suite *AgentTestSuite) TestReconcileDiscovery() {
	hcName := "test-5"

	suite.controller.clusterName = "spoke-1"

	suite.createHCResources(hcName, nil, &hyperv1beta1.ClusterConfiguration{})

	hc := &hyperv1beta1.HostedCluster{}
	err := suite.controller.hubClient.Get(context.TODO(), types.NamespacedName{Name: hcName, Namespace: "clusters"}, hc)
	suite.Nil(err, "err nil when hosted cluster is found successfully")

	newStatus := &hyperv1beta1.HostedClusterStatus{
		Conditions: []metav1.Condition{{Type: string(hyperv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue, Reason: hyperv1beta1.AsExpectedReason, LastTransitionTime: metav1.NewTime(time.Now())}},
		KubeConfig: &corev1.LocalObjectReference{Name: "kubeconfig"},
		Version: &hyperv1beta1.ClusterVersionStatus{
			History: []configv1.UpdateHistory{{State: configv1.CompletedUpdate, StartedTime: metav1.NewTime(time.Now())}},
		},
	}

	hc.Status = *newStatus

	err = suite.controller.hubClient.Status().Update(context.TODO(), hc, &client.SubResourceUpdateOptions{})
	suite.Nil(err, "err nil when hosted cluster status is found successfully")

	// Create klusterlet namespace
	klusterletNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "klusterlet-spoke-1-" + hc.Name,
		},
	}
	err = suite.controller.hubClient.Create(context.TODO(), klusterletNamespace)
	suite.Nil(err, "err nil when klusterletNamespace was created successfully")

	// Reconcile with annotation
	_, err = suite.controller.Reconcile(context.TODO(), ctrl.Request{NamespacedName: types.NamespacedName{Name: hcName, Namespace: "clusters"}})
	suite.Nil(err, "err nil when reconcile was successfully")

	// Secret for kubconfig is created
	secret := &corev1.Secret{}
	kcSecretNN := types.NamespacedName{Name: fmt.Sprintf("%s-admin-kubeconfig", hc.Name), Namespace: suite.controller.clusterName}
	err = suite.controller.hubClient.Get(context.TODO(), kcSecretNN, secret)
	suite.Nil(err, "is nil when the admin kubeconfig secret is found")

	// The hosted cluster does not have status.KubeadminPassword so the kubeadmin-password is not expected to be copied
	pwdSecretNN := types.NamespacedName{Name: fmt.Sprintf("%s-kubeadmin-password", hc.Name), Namespace: suite.controller.clusterName}
	err = suite.controller.hubClient.Get(context.TODO(), pwdSecretNN, secret)
	suite.True(err != nil && errors.IsNotFound(err), "is true when the kubeadmin-password secret is not copied")

	kcExtSecretNN := types.NamespacedName{Name: "external-managed-kubeconfig", Namespace: "klusterlet-spoke-1-" + hc.Name}
	err = suite.controller.hubClient.Get(context.TODO(), kcExtSecretNN, secret)
	suite.Nil(err, "is nil when external-managed-kubeconfig secret is found")

	kubeconfig, err := clientcmd.Load(secret.Data["kubeconfig"])
	suite.Nil(err, "is nil when kubeconfig data can be loaded")
	suite.Equal(kubeconfig.Clusters["cluster"].Server, "https://kube-apiserver."+hc.Namespace+"-"+hc.Name+".svc.cluster.local:443")

	suite.Equal(float64(0), testutil.ToFloat64(metrics.KubeconfigSecretCopyFailureCount))
	suite.Equal(float64(1), testutil.ToFloat64(metrics.TotalHostedClusterGauge))
	suite.Equal(float64(1), testutil.ToFloat64(metrics.HostedClusterAvailableGauge))

	hc1 := &hyperv1beta1.HostedCluster{}
	err = suite.controller.hubClient.Get(context.TODO(), types.NamespacedName{Namespace: hc.Namespace, Name: hc.Name}, hc1)
	suite.Nil(err, "err nil when hosted cluster is found successfully")

	// Add the kubeadmin password to the hosted cluster CR to test that the kubeadmin-password is copied
	newStatus = &hyperv1beta1.HostedClusterStatus{
		Conditions: []metav1.Condition{{Type: string(hyperv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue, Reason: hyperv1beta1.AsExpectedReason, LastTransitionTime: metav1.NewTime(time.Now())}},
		KubeConfig: &corev1.LocalObjectReference{Name: "kubeconfig"},
		Version: &hyperv1beta1.ClusterVersionStatus{
			History: []configv1.UpdateHistory{{State: configv1.CompletedUpdate, StartedTime: metav1.NewTime(time.Now())}},
		},
		KubeadminPassword: &corev1.LocalObjectReference{Name: "kubeadmin-password"},
	}
	hc1.Status = *newStatus

	err = suite.controller.hubClient.Status().Update(context.TODO(), hc1, &client.SubResourceUpdateOptions{})
	suite.Nil(err, "err nil when hosted cluster was updated successfully")

	_, err = suite.controller.Reconcile(context.TODO(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: hc.Namespace, Name: hc.Name}})
	suite.Nil(err, "err nil when reconcile was successfully")

	// The hosted cluster now has status.KubeadminPassword so the kubeadmin-password is expected to be copied
	err = suite.controller.hubClient.Get(context.TODO(), pwdSecretNN, secret)
	suite.Nil(err, "is nil when the kubeadmin-password secret is found")

	// Delete hosted cluster and reconcile
	suite.controller.hubClient.Delete(context.TODO(), hc)

	suite.Eventually(func() bool {
		hcToDelete := &hyperv1beta1.HostedCluster{}
		err := suite.controller.hubClient.Get(context.TODO(), types.NamespacedName{Namespace: "clusters", Name: hcName}, hcToDelete)
		return err != nil && errors.IsNotFound(err)
	}, 5*time.Second, 500*time.Millisecond)

	_, err = suite.controller.Reconcile(context.TODO(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: hc.Namespace, Name: hc.Name}})
	suite.Nil(err, "err nil when reconcile was successfully")

	err = suite.controller.hubClient.Get(context.TODO(), kcSecretNN, secret)
	suite.True(err != nil && errors.IsNotFound(err), "is true when the admin kubeconfig secret is deleted")
	err = suite.controller.hubClient.Get(context.TODO(), pwdSecretNN, secret)
	suite.True(err != nil && errors.IsNotFound(err), "is nil when the kubeadmin password secret is deleted")
}

func (suite *AgentTestSuite) TestGenerateHCPMetrics() {
	ctx := context.Background()

	availableHCP := &hyperv1beta1.HostedControlPlane{
		TypeMeta: metav1.TypeMeta{
			Kind:       "HostedControlPlane",
			APIVersion: "hypershift.openshift.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp1",
			Namespace: "clusters",
		},
		Spec: hyperv1beta1.HostedControlPlaneSpec{
			Platform: hyperv1beta1.PlatformSpec{
				Type: hyperv1beta1.AWSPlatform,
			},
			Networking: hyperv1beta1.ClusterNetworking{
				NetworkType:    hyperv1beta1.OpenShiftSDN,
				ServiceNetwork: []hyperv1beta1.ServiceNetworkEntry{},
				ClusterNetwork: []hyperv1beta1.ClusterNetworkEntry{},
			},
			Services: []hyperv1beta1.ServicePublishingStrategyMapping{},
			Etcd: hyperv1beta1.EtcdSpec{
				ManagementType: hyperv1beta1.Managed,
			},
			InfraID: "hcp1-abcdef",
		},
		Status: hyperv1beta1.HostedControlPlaneStatus{
			Ready:   true,
			Version: "4.14.0",
		},
	}

	unavailableHCP := &hyperv1beta1.HostedControlPlane{
		TypeMeta: metav1.TypeMeta{
			Kind:       "HostedControlPlane",
			APIVersion: "hypershift.openshift.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp2",
			Namespace: "clusters",
		},
		Spec: hyperv1beta1.HostedControlPlaneSpec{
			Platform: hyperv1beta1.PlatformSpec{
				Type: hyperv1beta1.AWSPlatform,
			},
			Networking: hyperv1beta1.ClusterNetworking{
				NetworkType:    hyperv1beta1.OpenShiftSDN,
				ServiceNetwork: []hyperv1beta1.ServiceNetworkEntry{},
				ClusterNetwork: []hyperv1beta1.ClusterNetworkEntry{},
			},
			Services: []hyperv1beta1.ServicePublishingStrategyMapping{},
			Etcd: hyperv1beta1.EtcdSpec{
				ManagementType: hyperv1beta1.Managed,
			},
			InfraID: "hcp1-abcdef",
		},
		Status: hyperv1beta1.HostedControlPlaneStatus{
			Ready:   false,
			Version: "4.14.3",
		},
	}

	err := suite.controller.spokeUncachedClient.Create(ctx, availableHCP)
	suite.Nil(err, "err nil when hosted control plane hcp1 is created successfully")

	hcp1 := &hyperv1beta1.HostedControlPlane{}
	err = suite.controller.hubClient.Get(ctx, types.NamespacedName{Namespace: "clusters", Name: "hcp1"}, hcp1)
	suite.Nil(err, "err nil when hosted control plane is found successfully")

	newStatus := &hyperv1beta1.HostedControlPlaneStatus{
		Ready:   true,
		Version: "4.14.0",
	}

	hcp1.Status = *newStatus

	err = suite.controller.hubClient.Status().Update(ctx, hcp1, &client.SubResourceUpdateOptions{})
	suite.Nil(err, "err nil when hosted control plane status is found successfully")

	err = suite.controller.spokeUncachedClient.Create(ctx, unavailableHCP)
	suite.Nil(err, "err nil when hosted control plane hcp2 is created successfully")

	hcp2 := &hyperv1beta1.HostedControlPlane{}
	err = suite.controller.hubClient.Get(ctx, types.NamespacedName{Namespace: "clusters", Name: "hcp2"}, hcp2)
	suite.Nil(err, "err nil when hosted control plane is found successfully")

	newStatus2 := &hyperv1beta1.HostedControlPlaneStatus{
		Ready:   false,
		Version: "4.14.3",
	}

	hcp2.Status = *newStatus2

	err = suite.controller.hubClient.Status().Update(ctx, hcp2, &client.SubResourceUpdateOptions{})
	suite.Nil(err, "err nil when hosted control plane status is found successfully")

	suite.controller.GenerateHCPMetrics(ctx)
	suite.Equal(float64(1), testutil.ToFloat64(metrics.HostedControlPlaneStatusGaugeVec.WithLabelValues("clusters", "hcp1", "true", "4.14.0")))
	suite.Equal(float64(1), testutil.ToFloat64(metrics.HostedControlPlaneStatusGaugeVec.WithLabelValues("clusters", "hcp2", "false", "4.14.3")))
}

func (suite *AgentTestSuite) TestHostedClusterCount() {
	ctx := context.TODO()

	err := suite.controller.SyncAddOnPlacementScore(ctx, false)
	suite.Nil(err, "err nil when CreateAddOnPlacementScore was successfully")

	// No HC yet, so the zero cluster claim value should be true
	//zeroClusterClaim, err := suite.controller.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountZeroClusterClaimKey, metav1.GetOptions{})
	//suite.Nil(err, "is nil when the hc count zero clusterclaim is found")
	//suite.Equal(strconv.FormatBool(true), zeroClusterClaim.Spec.Value)

	suite.controller.maxHostedClusterCount = 5
	suite.controller.thresholdHostedClusterCount = 3

	i := 0
	for i < (suite.controller.maxHostedClusterCount - 1) {
		hcName := "counttest-" + strconv.Itoa(i)
		suite.createHCResources(hcName, nil, &hyperv1beta1.ClusterConfiguration{})
		i++
	}

	err = suite.controller.SyncAddOnPlacementScore(ctx, false)
	suite.Nil(err, "err nil when CreateAddOnPlacementScore was successfully")

	// Created 4 HCs, max 5 so the full cluster claim value should be false
	fullClusterClaim, err := suite.controller.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountFullClusterClaimKey, metav1.GetOptions{})
	suite.Nil(err, "is nil when the hc count full clusterclaim is found")
	suite.Equal(strconv.FormatBool(false), fullClusterClaim.Spec.Value)

	// Created 4 HCs, threshold 3 so the threshold cluster claim value should be true
	thresholdClusterClaim, err := suite.controller.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountAboveThresholdClusterClaimKey, metav1.GetOptions{})
	suite.Nil(err, "is nil when the hc count at threshold clusterclaim is found")
	suite.Equal(strconv.FormatBool(true), thresholdClusterClaim.Spec.Value)

	// Created 4 HCs, so the zero cluster claim value should be false
	//zeroClusterClaim, err = suite.controller.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountZeroClusterClaimKey, metav1.GetOptions{})
	//suite.Nil(err, "is nil when the hc count zero clusterclaim is found")
	//suite.Equal(strconv.FormatBool(false), zeroClusterClaim.Spec.Value)

	placementScore := &clusterv1alpha1.AddOnPlacementScore{}
	placementScoreNN := types.NamespacedName{Name: util.HostedClusterScoresResourceName, Namespace: suite.controller.clusterName}
	err = suite.controller.hubClient.Get(ctx, placementScoreNN, placementScore)
	suite.Nil(err, "is nil when addonPlacementScore is found")
	suite.Equal(int32(suite.controller.maxHostedClusterCount-1), placementScore.Status.Scores[0].Value)

	suite.Equal(float64(0), testutil.ToFloat64(metrics.PlacementClusterClaimsFailureCount.WithLabelValues(util.MetricsLabelFullClusterClaim)))
	suite.Equal(float64(0), testutil.ToFloat64(metrics.PlacementClusterClaimsFailureCount.WithLabelValues(util.MetricsLabelThresholdClusterClaim)))
	suite.Equal(float64(0), testutil.ToFloat64(metrics.PlacementClusterClaimsFailureCount.WithLabelValues(util.MetricsLabelZeroClusterClaim)))
	suite.Equal(float64(4), testutil.ToFloat64(metrics.TotalHostedClusterGauge))
	suite.Equal(float64(4), testutil.ToFloat64(metrics.HostedClusterAvailableGauge))

	// Create one more hosted cluster and expect the cluserclaim to have hostedclustercount.full.hypershift.openshift.io=true
	// indicating it reached the maximum number of hosted clusters
	suite.createHCResources("counttest-80", nil, &hyperv1beta1.ClusterConfiguration{})

	hc := &hyperv1beta1.HostedCluster{}
	err = suite.controller.hubClient.Get(ctx, types.NamespacedName{Namespace: "clusters", Name: "counttest-80"}, hc)
	suite.Nil(err, "err nil when hosted cluster is found successfully")

	newStatus := &hyperv1beta1.HostedClusterStatus{
		Conditions: []metav1.Condition{{Type: string(hyperv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue, Reason: hyperv1beta1.AsExpectedReason, LastTransitionTime: metav1.NewTime(time.Now())}},
		KubeConfig: &corev1.LocalObjectReference{Name: "kubeconfig"},
		Version: &hyperv1beta1.ClusterVersionStatus{
			History: []configv1.UpdateHistory{{State: configv1.CompletedUpdate, StartedTime: metav1.NewTime(time.Now())}},
		},
	}

	hc.Status = *newStatus

	err = suite.controller.hubClient.Status().Update(ctx, hc, &client.SubResourceUpdateOptions{})
	suite.Nil(err, "err nil when hosted cluster status is found successfully")

	err = suite.controller.SyncAddOnPlacementScore(ctx, false)
	suite.Nil(err, "err nil when CreateAddOnPlacementScore was successfully")

	// 5 HCs, max 5 so the full cluster claim value should be true
	fullClusterClaim, err = suite.controller.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountFullClusterClaimKey, metav1.GetOptions{})
	suite.Nil(err, "is nil when the clusterclaim is found")
	suite.Equal(strconv.FormatBool(true), fullClusterClaim.Spec.Value)

	// Created 5 HCs, threshold 3 so the threshold cluster claim value should be true
	thresholdClusterClaim, err = suite.controller.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountAboveThresholdClusterClaimKey, metav1.GetOptions{})
	suite.Nil(err, "is nil when the hc count at threshold clusterclaim is found")
	suite.Equal(strconv.FormatBool(true), thresholdClusterClaim.Spec.Value)

	// Created 5 HCs, so the zero cluster claim value should be false
	//zeroClusterClaim, err = suite.controller.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountZeroClusterClaimKey, metav1.GetOptions{})
	//suite.Nil(err, "is nil when the hc count zero clusterclaim is found")
	//suite.Equal(strconv.FormatBool(false), zeroClusterClaim.Spec.Value)

	err = suite.controller.hubClient.Get(ctx, placementScoreNN, placementScore)
	suite.Nil(err, "is nil when addonPlacementScore is found")
	suite.Equal(int32(suite.controller.maxHostedClusterCount), placementScore.Status.Scores[0].Value)

	suite.Equal(float64(5), testutil.ToFloat64(metrics.TotalHostedClusterGauge))
	suite.Equal(float64(5), testutil.ToFloat64(metrics.HostedClusterAvailableGauge))

	// Delete one hosted cluster and expect the cluserclaim to have hostedclustercount.full.hypershift.openshift.io=false
	// indicating it did not reach the maximum number of hosted clusters after removing one

	suite.deleteHCResources("counttest-80")

	err = suite.controller.SyncAddOnPlacementScore(ctx, false)
	suite.Nil(err, "err nil when CreateAddOnPlacementScore was successfully")

	fullClusterClaim, err = suite.controller.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountFullClusterClaimKey, metav1.GetOptions{})
	suite.Nil(err, "is nil when the clusterclaim is found")
	suite.Equal(strconv.FormatBool(false), fullClusterClaim.Spec.Value)

	// Created 4 HCs, threshold 3 so the threshold cluster claim value should be true
	thresholdClusterClaim, err = suite.controller.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountAboveThresholdClusterClaimKey, metav1.GetOptions{})
	suite.Nil(err, "is nil when the hc count at threshold clusterclaim is found")
	suite.Equal(strconv.FormatBool(true), thresholdClusterClaim.Spec.Value)

	// Created 4 HCs, so the zero cluster claim value should be false
	//zeroClusterClaim, err = suite.controller.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountZeroClusterClaimKey, metav1.GetOptions{})
	//suite.Nil(err, "is nil when the hc count zero clusterclaim is found")
	//suite.Equal(strconv.FormatBool(false), zeroClusterClaim.Spec.Value)

	err = suite.controller.hubClient.Get(ctx, placementScoreNN, placementScore)
	suite.Nil(err, "is nil when addonPlacementScore is found")
	suite.Equal(int32(suite.controller.maxHostedClusterCount-1), placementScore.Status.Scores[0].Value)

	suite.deleteHCResources("counttest-3")

	err = suite.controller.SyncAddOnPlacementScore(ctx, false)
	suite.Nil(err, "err nil when CreateAddOnPlacementScore was successfully")

	// 3 HCs, threshold 3 so the threshold cluster claim value should be true
	thresholdClusterClaim, err = suite.controller.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountAboveThresholdClusterClaimKey, metav1.GetOptions{})
	suite.Nil(err, "is nil when the hc count at threshold clusterclaim is found")
	suite.Equal(strconv.FormatBool(true), thresholdClusterClaim.Spec.Value)

	suite.deleteHCResources("counttest-2")

	err = suite.controller.SyncAddOnPlacementScore(ctx, false)
	suite.Nil(err, "err nil when CreateAddOnPlacementScore was successfully")

	// 2 HCs, threshold 3 so the threshold cluster claim value should be true
	thresholdClusterClaim, err = suite.controller.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountAboveThresholdClusterClaimKey, metav1.GetOptions{})
	suite.Nil(err, "is nil when the hc count at threshold clusterclaim is found")
	suite.Equal(strconv.FormatBool(false), thresholdClusterClaim.Spec.Value)

	suite.deleteHCResources("counttest-1")

	suite.deleteHCResources("counttest-0")

	err = suite.controller.SyncAddOnPlacementScore(ctx, false)
	suite.Nil(err, "err nil when CreateAddOnPlacementScore was successfully")

	// 0 HC, max 5 so the full cluster claim value should be false
	fullClusterClaim, err = suite.controller.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountFullClusterClaimKey, metav1.GetOptions{})
	suite.Nil(err, "is nil when the clusterclaim is found")
	suite.Equal(strconv.FormatBool(false), fullClusterClaim.Spec.Value)

	// 0 HC, threshold 3 so the threshold cluster claim value should be false
	thresholdClusterClaim, err = suite.controller.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountAboveThresholdClusterClaimKey, metav1.GetOptions{})
	suite.Nil(err, "is nil when the hc count at threshold clusterclaim is found")
	suite.Equal(strconv.FormatBool(false), thresholdClusterClaim.Spec.Value)

	// 0 HC, so the zero cluster claim value should be true
	//zeroClusterClaim, err = suite.controller.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountZeroClusterClaimKey, metav1.GetOptions{})
	//suite.Nil(err, "is nil when the hc count zero clusterclaim is found")
	//suite.Equal(strconv.FormatBool(true), zeroClusterClaim.Spec.Value)

	suite.Equal(float64(0), testutil.ToFloat64(metrics.TotalHostedClusterGauge))
	suite.Equal(float64(0), testutil.ToFloat64(metrics.HostedClusterAvailableGauge))
}

func (suite *AgentTestSuite) TestHostedClusterCountStartupErrorCase() {
	// This tests SyncAddOnPlacementScore call during agent startup with no hypershift operator installation on the cluster yet.
	suite.controller.maxHostedClusterCount = 80
	suite.controller.thresholdHostedClusterCount = 60
	err := suite.controller.SyncAddOnPlacementScore(ctx, true)
	suite.Nil(err, "err nil when CreateAddOnPlacementScore was successfully")

	clusterClaim, err := suite.controller.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountFullClusterClaimKey, metav1.GetOptions{})
	suite.Nil(err, "is nil when the clusterclaim is found")
	suite.Equal(strconv.FormatBool(false), clusterClaim.Spec.Value)

	thresholdClusterClaim, err := suite.controller.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountAboveThresholdClusterClaimKey, metav1.GetOptions{})
	suite.Nil(err, "is nil when the hc count at threshold clusterclaim is found")
	suite.Equal(strconv.FormatBool(false), thresholdClusterClaim.Spec.Value)

	zeroClusterClaim, err := suite.controller.spokeClustersClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hostedClusterCountZeroClusterClaimKey, metav1.GetOptions{})
	suite.Nil(err, "is nil when the hc count zero clusterclaim is found")
	suite.Equal(strconv.FormatBool(true), zeroClusterClaim.Spec.Value)

	placementScore := &clusterv1alpha1.AddOnPlacementScore{}
	placementScoreNN := types.NamespacedName{Name: util.HostedClusterScoresResourceName, Namespace: suite.controller.clusterName}
	err = suite.controller.hubClient.Get(context.TODO(), placementScoreNN, placementScore)
	suite.Nil(err, "is nil when addonPlacementScore is found")
	suite.Equal(int32(0), placementScore.Status.Scores[0].Value)

	suite.Equal(float64(0), testutil.ToFloat64(metrics.TotalHostedClusterGauge))
	suite.Equal(float64(0), testutil.ToFloat64(metrics.HostedClusterAvailableGauge))
}

func (suite *AgentTestSuite) TestAgentCommand() {
	zapLog, _ := zap.NewDevelopment()
	cleanupCmd := NewAgentCommand("operator", zapr.NewLogger(zapLog))
	suite.Equal("agent", cleanupCmd.Use)
}

func (suite *AgentTestSuite) TestCleanupCommand() {
	ctx := context.Background()
	zapLog, _ := zap.NewDevelopment()

	cleanupCmd := NewCleanupCommand("operator", zapr.NewLogger(zapLog))
	suite.Equal("cleanup", cleanupCmd.Use)

	// Cleanup
	// Hypershift deployment is not deleted because there is an existing hostedcluster
	o := &AgentOptions{
		Log:            zapr.NewLogger(zapLog),
		AddonName:      "hypershift-addon",
		AddonNamespace: "hypershift",
	}

	uCtrl := install.NewUpgradeController(nil, initClient(), o.Log, o.AddonName, o.AddonNamespace, "my-spoke-cluster",
		"my-test-image", "my-pull-secret", true, ctx)

	err := o.runCleanup(ctx, uCtrl)
	suite.Nil(err, "is nil if cleanup is succcessful")
}

func (suite *AgentTestSuite) TestRunControllerManager() {
	ctx := context.Background()
	zapLog, _ := zap.NewDevelopment()
	o := &AgentOptions{
		Log:            zapr.NewLogger(zapLog),
		AddonName:      "hypershift-addon",
		AddonNamespace: "hypershift",
	}

	err := o.runControllerManager(ctx)
	suite.NotNil(err, "err it not nil if the controller fail to run")
}

func (suite *AgentTestSuite) TestInitialAddonStatus() {
	ctx := context.Background()
	addon := addonv1alpha1.ManagedClusterAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: suite.controller.clusterName,
			Name:      util.AddonControllerName,
		},
	}

	err := suite.controller.hubClient.Create(ctx, &addon)
	suite.Nil(err, "err nil when hypershift-addon ManagedClusterAddOn is created successfully for local-cluster")

	addonCondition1 := metav1.Condition{
		Type:    "testType1",
		Status:  metav1.ConditionTrue,
		Reason:  "NoReason",
		Message: "test message 123",
	}

	meta.SetStatusCondition(&addon.Status.Conditions, addonCondition1)

	err = suite.controller.hubClient.Status().Update(ctx, &addon, &client.SubResourceUpdateOptions{})
	suite.Nil(err, "err nil when hypershift-addon ManagedClusterAddOn test condition is set successfully for local-cluster")

	addonStatusController := &AddonStatusController{
		suite.controller.hubClient, suite.controller.hubClient, suite.controller.log,
		types.NamespacedName{Namespace: suite.controller.clusterName, Name: util.AddonControllerName},
		suite.controller.clusterName,
	}

	err = addonStatusController.UpdateInitialStatus(ctx)
	suite.Nil(err, "err nil when the initial hypershift-addon ManagedClusterAddOn status is set successfully for local-cluster")

	err = suite.controller.hubClient.Get(ctx, types.NamespacedName{Name: util.AddonControllerName, Namespace: suite.controller.clusterName}, &addon)
	suite.Nil(err, "is nil when hypershift addon is found")

	// At the initial startup time of the addon agent, there is no hypershift operator deployment.
	// The addon status conditions should have hypershift operator deployment degraded=True
	var degraded bool = false
	for _, condition := range addon.Status.Conditions {
		if condition.Status == metav1.ConditionTrue &&
			condition.Type == string(addonv1alpha1.ManagedClusterAddOnConditionDegraded) {
			degraded = true
			break
		}
	}
	suite.True(degraded, "the hypershift addon condition HO degraded is set to True when there is no HO deployment")
}

func (suite *AgentTestSuite) Test_agentController_deleteManagedCluster() {
	ctx := context.Background()

	kl := &operatorapiv1.Klusterlet{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "klusterlet-c1",
			Finalizers: []string{"operator.open-cluster-management.io/klusterlet-hosted-cleanup"},
		},
	}
	err := suite.controller.hubClient.Create(ctx, kl)
	suite.Nil(err, "err nil when klusterlet is created successfully")

	mc := &clusterv1.ManagedCluster{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:        "c1",
			Annotations: map[string]string{"import.open-cluster-management.io/klusterlet-deploy-mode": "Hosted"},
		},
		Spec: clusterv1.ManagedClusterSpec{
			HubAcceptsClient:     false,
			LeaseDurationSeconds: 0,
		},
	}
	err = suite.controller.hubClient.Create(ctx, mc)
	suite.Nil(err, "err nil when managedcluster is created successfully")

	mc2 := &clusterv1.ManagedCluster{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:        "deletetest-3",
			Annotations: map[string]string{"import.open-cluster-management.io/klusterlet-deploy-mode": "Hosted"},
		},
		Spec: clusterv1.ManagedClusterSpec{
			HubAcceptsClient:     false,
			LeaseDurationSeconds: 0,
		},
	}
	err = suite.controller.hubClient.Create(ctx, mc2)
	suite.Nil(err, "err nil when managedcluster is created successfully")

	hiveMc := &clusterv1.ManagedCluster{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:        "deletetest-4",
			Annotations: map[string]string{"open-cluster-management/created-via": "hive"},
		},
		Spec: clusterv1.ManagedClusterSpec{
			HubAcceptsClient:     false,
			LeaseDurationSeconds: 0,
		},
	}
	err = suite.controller.hubClient.Create(ctx, hiveMc)
	suite.Nil(err, "err nil when managedcluster is created successfully")

	notHCMc := &clusterv1.ManagedCluster{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:        "deletetest-5",
			Annotations: map[string]string{"import.open-cluster-management.io/klusterlet-deploy-mode": "other"},
		},
		Spec: clusterv1.ManagedClusterSpec{
			HubAcceptsClient:     false,
			LeaseDurationSeconds: 0,
		},
	}
	err = suite.controller.hubClient.Create(ctx, notHCMc)
	suite.Nil(err, "err nil when managedcluster is created successfully")

	hcNoAnno := suite.createHCResources("deletetest-1", nil, &hyperv1beta1.ClusterConfiguration{})

	hcAnno := suite.createHCResources("deletetest-2", map[string]string{util.ManagedClusterAnnoKey: "c1"}, &hyperv1beta1.ClusterConfiguration{})

	hcNoKlusterlet := suite.createHCResources("deletetest-3", map[string]string{util.ManagedClusterAnnoKey: "deletetest-3"}, &hyperv1beta1.ClusterConfiguration{})

	hc4 := suite.createHCResources("deletetest-4", nil, &hyperv1beta1.ClusterConfiguration{})

	suite.createHCResources("deletetest-5", nil, &hyperv1beta1.ClusterConfiguration{})

	suite.controller.maxHostedClusterCount = 80
	suite.controller.thresholdHostedClusterCount = 60

	type args struct {
		ctx context.Context
		hc  *hyperv1beta1.HostedCluster
	}
	tests := []struct {
		name    string
		hc      *hyperv1beta1.HostedCluster
		mc      *clusterv1.ManagedCluster
		k       *operatorapiv1.Klusterlet
		wantErr bool
	}{
		{
			name:    "Delete nil hosted cluster",
			hc:      nil,
			wantErr: true,
		},
		{
			name:    "Delete hosted cluster with no managedcluster-name annotation",
			hc:      hcNoAnno,
			wantErr: false,
		},
		{
			name:    "Delete hosted cluster with managedcluster-name annotation",
			hc:      hcAnno,
			mc:      mc,
			k:       kl,
			wantErr: false,
		},
		{
			name:    "Delete hosted cluster with no klusterlet",
			hc:      hcNoKlusterlet,
			mc:      mc2,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		suite.Run(tt.name, func() {
			if err := suite.controller.deleteManagedCluster(ctx, tt.hc); (err != nil) != tt.wantErr {
				suite.Errorf(err, "agentController.deleteManagedCluster() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.mc != nil {
				gotMc := &clusterv1.ManagedCluster{}
				err = suite.controller.hubClient.Get(ctx, types.NamespacedName{Name: tt.mc.Name, Namespace: tt.mc.Namespace}, gotMc)

				// Managed cluster is deleted
				suite.NotNil(err, "err not nil if managed cluster is not found")
				suite.True(apierrors.IsNotFound(err), "true if error is type IsNotFound")
			}

			if tt.k != nil {
				// verify klusterlet has finalizer removed
				gotKl := &operatorapiv1.Klusterlet{}
				err = suite.controller.hubClient.Get(ctx, types.NamespacedName{Name: tt.k.Name, Namespace: tt.k.Namespace}, gotKl)
				suite.Nil(err, "err nil if klusterlet is found")
				hasFinalizer := controllerutil.ContainsFinalizer(gotKl, "operator.open-cluster-management.io/klusterlet-hosted-cleanup")
				suite.False(hasFinalizer, "false if finalizer is removed")
			}
		})
	}

	tests2 := []struct {
		name    string
		hc      *hyperv1beta1.HostedCluster
		mc      *clusterv1.ManagedCluster
		k       *operatorapiv1.Klusterlet
		wantErr bool
	}{
		{
			name:    "Delete hosted cluster that has the same name as a hive cluster managed cluster",
			hc:      hc4,
			mc:      hiveMc,
			wantErr: false,
		},
		{
			name:    "Delete hosted cluster that has the same name as some other managed cluster that is not hosted cluster",
			hc:      hc4,
			mc:      notHCMc,
			wantErr: false,
		},
	}
	for _, tt := range tests2 {
		suite.Run(tt.name, func() {
			if err := suite.controller.deleteManagedCluster(ctx, tt.hc); (err != nil) != tt.wantErr {
				suite.Errorf(err, "agentController.deleteManagedCluster() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.mc != nil {
				gotMc := &clusterv1.ManagedCluster{}
				err = suite.controller.hubClient.Get(ctx, types.NamespacedName{Name: tt.mc.Name, Namespace: tt.mc.Namespace}, gotMc)

				// Managed cluster is NOT deleted
				suite.Nil(err, "managed cluster is found")
				suite.NotEmpty(gotMc, "managed cluster is found")
				//assert.True(t, apierrors.IsNotFound(err), "true if error is type IsNotFound")
			}
		})
	}

	suite.deleteHCResources("deletetest-1")
	suite.deleteHCResources("deletetest-2")
	suite.deleteHCResources("deletetest-3")
	suite.deleteHCResources("deletetest-4")
	suite.deleteHCResources("deletetest-5")
}

var kubeconfig0 = `apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: test
    server: https://kube-apiserver.ocm-dev-1sv4l4ldnr6rd8ni12ndo4vtiq2gd7a4-sbarouti267.svc.cluster.local:7443
  name: cluster
contexts:
- context:
    cluster: cluster
    namespace: default
    user: admin
  name: admin
current-context: admin
kind: Config
`

var kubeconfig1 = `apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: test
    server: https://kube-apiserver.ocm-dev-1sv4l4ldnr6rd8ni12ndo4vtiq2gd7a4-sbarouti267.svc.cluster.local:7443
  name: cluster
- cluster:
    certificate-authority-data: test
    server: https://kube-apiserver.ocm-dev-1sv4l4ldnr6rd8ni12ndo4vtiq2gd7a4-sbarouti267.svc.cluster.local:7443
  name: cluster2
contexts:
- context:
    cluster: cluster
    namespace: default
    user: admin
  name: admin
current-context: admin
kind: Config
`

func (suite *AgentTestSuite) Test_removeCertAuthDataFromKubeConfig() {
	ctx := context.Background()

	tlsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tls",
			Namespace: "default",
		},
		Data: map[string][]byte{"tls.crt": []byte("replaced-crt")},
	}

	err := suite.controller.hubClient.Create(ctx, tlsSecret)
	suite.Nil(err, "tls secret created successfully")

	tests := []struct {
		name       string
		kubeconfig []byte
		secretName string
		secretNs   string
		wantCert   string
		wantErr    string
	}{
		{
			name:       "No Secret",
			kubeconfig: []byte(kubeconfig0),
			secretName: "test-tls",
			secretNs:   "default",
			wantErr:    "secrets \"test-tls\" not found",
		},
		{
			name:       "Single cluster",
			kubeconfig: []byte(kubeconfig0),
			secretName: "tls",
			secretNs:   "default",
			wantCert:   "replaced-crt",
			wantErr:    "",
		},
		{
			name:       "Two cluster",
			kubeconfig: []byte(kubeconfig1),
			secretName: "tls",
			secretNs:   "default",
			wantCert:   "replaced-crt",
			wantErr:    "",
		},
	}
	for _, tt := range tests {
		suite.Run(tt.name, func() {
			got, err := suite.controller.replaceCertAuthDataInKubeConfig(ctx, tt.kubeconfig, tt.secretNs, tt.secretName)

			if tt.wantErr != "" {
				if err == nil {
					suite.Errorf(err, "Want error = %v, but nil", tt.wantErr)
				} else if tt.wantErr != err.Error() {
					suite.Errorf(err, "Want error = %v, but got = %v", tt.wantErr, err.Error())
				}
			}

			gotConfig, err := clientcmd.Load(got)
			suite.Nil(err, "No error loading updated kubeconfig")

			for _, v := range gotConfig.Clusters {
				suite.Equal(string(v.CertificateAuthorityData), tt.wantCert, "equals when cert is replaced")
			}
		})
	}
}

func (suite *AgentTestSuite) Test_getNameCerts() {
	hcNN1 := types.NamespacedName{Name: "hd-1", Namespace: "clusters"}
	hc1 := getHostedCluster(hcNN1)

	hcNN2 := types.NamespacedName{Name: "hd-2", Namespace: "clusters"}
	hc2 := getHostedCluster(hcNN2)
	hc2.Spec.Configuration = &hyperv1beta1.ClusterConfiguration{
		APIServer: &configv1.APIServerSpec{
			ServingCerts: configv1.APIServerServingCerts{
				NamedCertificates: []configv1.APIServerNamedServingCert{
					{
						ServingCertificate: configv1.SecretNameReference{
							Name: "test-tls",
						},
					},
				},
			},
		},
	}

	tests := []struct {
		name string
		hc   *hyperv1beta1.HostedCluster
		want string
	}{
		{
			name: "No ServingCertificate",
			hc:   hc1,
			want: "",
		},
		{
			name: "Has ServingCertificate",
			hc:   hc2,
			want: "test-tls",
		},
	}
	for _, tt := range tests {
		suite.Run(tt.name, func() {
			if got := getServingCert(tt.hc); got != tt.want {
				suite.Errorf(nil, "hasNameCerts() = %v, want %v", got, tt.want)
			}
		})
	}
}

func (suite *AgentTestSuite) Test_SetHCPSizingBaseline() {
	ctx := context.Background()

	// Test SetHCPSizingBaseline without the overriding configmap
	// and verify that all the baseline values are set
	suite.controller.SetHCPSizingBaseline(ctx)

	suite.Equal(float64(5), suite.controller.hcpSizingBaseline.cpuRequestPerHCP)
	suite.Equal(float64(18), suite.controller.hcpSizingBaseline.memoryRequestPerHCP)
	suite.Equal(float64(75), suite.controller.hcpSizingBaseline.podsPerHCP)
	suite.Equal(float64(9.0), suite.controller.hcpSizingBaseline.incrementalCPUUsagePer1KQPS)
	suite.Equal(float64(2.5), suite.controller.hcpSizingBaseline.incrementalMemUsagePer1KQPS)
	suite.Equal(float64(2.9), suite.controller.hcpSizingBaseline.idleCPUUsage)
	suite.Equal(float64(11.1), suite.controller.hcpSizingBaseline.idleMemoryUsage)
	suite.Equal(float64(50.0), suite.controller.hcpSizingBaseline.minimumQPSPerHCP)
	suite.Equal(float64(1000.0), suite.controller.hcpSizingBaseline.mediumQPSPerHCP)
	suite.Equal(float64(2000.0), suite.controller.hcpSizingBaseline.highQPSPerHCP)

	hcpSizingConfigmap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp-sizing-baseline",
			Namespace: "local-cluster",
		},
		Data: map[string]string{
			"cpuRequestPerHCP":            "8",
			"memoryRequestPerHCP":         "21",
			"podsPerHCP":                  "250",
			"incrementalCPUUsagePer1KQPS": "5.0",
			"incrementalMemUsagePer1KQPS": "3.5",
			"idleCPUUsage":                "2.1",
			"idleMemoryUsage":             "8.5",
			"minimumQPSPerHCP":            "100.0",
			"mediumQPSPerHCP":             "1500.0",
			"highQPSPerHCP":               "2500.0",
		},
	}

	err := suite.controller.hubClient.Create(ctx, hcpSizingConfigmap)
	suite.Nil(err, "hcp sizing baseline configmap created successfully")

	// With the HCP sizing baseline override configmap, verify the overriden values
	suite.controller.SetHCPSizingBaseline(ctx)

	suite.Equal(float64(8), suite.controller.hcpSizingBaseline.cpuRequestPerHCP)
	suite.Equal(float64(21), suite.controller.hcpSizingBaseline.memoryRequestPerHCP)
	suite.Equal(float64(250), suite.controller.hcpSizingBaseline.podsPerHCP)
	suite.Equal(float64(5.0), suite.controller.hcpSizingBaseline.incrementalCPUUsagePer1KQPS)
	suite.Equal(float64(3.5), suite.controller.hcpSizingBaseline.incrementalMemUsagePer1KQPS)
	suite.Equal(float64(2.1), suite.controller.hcpSizingBaseline.idleCPUUsage)
	suite.Equal(float64(8.5), suite.controller.hcpSizingBaseline.idleMemoryUsage)
	suite.Equal(float64(100.0), suite.controller.hcpSizingBaseline.minimumQPSPerHCP)
	suite.Equal(float64(1500.0), suite.controller.hcpSizingBaseline.mediumQPSPerHCP)
	suite.Equal(float64(2500.0), suite.controller.hcpSizingBaseline.highQPSPerHCP)

	hcpSizingConfigmap = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp-sizing-baseline",
			Namespace: "local-cluster",
		},
		Data: map[string]string{
			"cpuRequestPerHCP":            "8aa",
			"memoryRequestPerHCP":         "21aa",
			"podsPerHCP":                  "250aa",
			"incrementalCPUUsagePer1KQPS": "5.0aa",
			"incrementalMemUsagePer1KQPS": "3.5aa",
			"idleCPUUsage":                "2.1aa",
			"idleMemoryUsage":             "8.5aa",
			"minimumQPSPerHCP":            "100.0aa",
			"mediumQPSPerHCP":             "1500.0aa",
			"highQPSPerHCP":               "2500.0aa",
		},
	}

	err = suite.controller.hubClient.Update(ctx, hcpSizingConfigmap)
	suite.Nil(err, "hcp sizing baseline configmap updated successfully")

	// With the HCP sizing baseline override configmap containing invalid override values,
	// verify that the sizing baseline values are set with the default values
	suite.controller.SetHCPSizingBaseline(ctx)

	suite.Equal(float64(5), suite.controller.hcpSizingBaseline.cpuRequestPerHCP)
	suite.Equal(float64(18), suite.controller.hcpSizingBaseline.memoryRequestPerHCP)
	suite.Equal(float64(75), suite.controller.hcpSizingBaseline.podsPerHCP)
	suite.Equal(float64(9.0), suite.controller.hcpSizingBaseline.incrementalCPUUsagePer1KQPS)
	suite.Equal(float64(2.5), suite.controller.hcpSizingBaseline.incrementalMemUsagePer1KQPS)
	suite.Equal(float64(2.9), suite.controller.hcpSizingBaseline.idleCPUUsage)
	suite.Equal(float64(11.1), suite.controller.hcpSizingBaseline.idleMemoryUsage)
	suite.Equal(float64(50.0), suite.controller.hcpSizingBaseline.minimumQPSPerHCP)
	suite.Equal(float64(1000.0), suite.controller.hcpSizingBaseline.mediumQPSPerHCP)
	suite.Equal(float64(2000.0), suite.controller.hcpSizingBaseline.highQPSPerHCP)
}

func TestAgentTestSuite(t *testing.T) {
	suite.Run(t, new(AgentTestSuite))
}

/*
func TestMain(m *testing.M) {
	t := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "hack", "crds"),
		},
	}

	apis.AddToScheme(schemes.Scheme)
	appsv1.AddToScheme(schemes.Scheme)
	corev1.AddToScheme(schemes.Scheme)
	metav1.AddMetaToScheme(schemes.Scheme)
	hyperv1beta1.AddToScheme(schemes.Scheme)
	clusterv1alpha1.AddToScheme(schemes.Scheme)
	clusterv1.AddToScheme(schemes.Scheme)
	operatorapiv1.AddToScheme(schemes.Scheme)
	addonv1alpha1.AddToScheme(schemes.Scheme)
	routev1.AddToScheme(schemes.Scheme)

	var err error
	if cfg2, err = t.Start(); err != nil {
		log.Fatal(err)
	}

	var c client.Client

	if c, err = client.New(cfg2, client.Options{Scheme: schemes.Scheme}); err != nil {
		log.Fatal(err)
	}

	err = c.Create(context.TODO(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "local-cluster"},
	})
	if err != nil {
		log.Fatal(err)
	}

	err = c.Create(context.TODO(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "clusters"},
	})
	if err != nil {
		log.Fatal(err)
	}

	err = c.Create(context.TODO(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "clusters-hd-1"},
	})
	if err != nil {
		log.Fatal(err)
	}

	code := m.Run()

	t.Stop()
	os.Exit(code)
}

// SetupTestReconcile returns a reconcile.Reconcile implementation that delegates to inner and
// writes the request to requests after Reconcile is finished.
func SetupTestReconcile(inner reconcile.Reconciler) (reconcile.Reconciler, chan reconcile.Request) {
	requests := make(chan reconcile.Request)
	fn := reconcile.Func(func(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
		result, err := inner.Reconcile(ctx, req)
		requests <- req

		return result, err
	})

	return fn, requests
}

// StartTestManager adds recFn
func StartTestManager(ctx context.Context, mgr manager.Manager, g *gomega.GomegaWithT) *sync.WaitGroup {
	wg := &sync.WaitGroup{}
	wg.Add(1)

	go func() {
		wg.Done()
		mgr.Start(ctx)
	}()

	return wg
}
*/
