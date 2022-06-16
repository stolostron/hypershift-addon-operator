package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-logr/zapr"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	hyperv1alpha1 "github.com/openshift/hypershift/api/v1alpha1"
)

func TestReconcile(t *testing.T) {
	ctx := context.Background()
	client := initClient()
	zapLog, _ := zap.NewDevelopment()

	aCtrl := &agentController{
		spokeUncachedClient:       client,
		spokeClient:               client,
		hubClient:                 client,
		log:                       zapr.NewLogger(zapLog),
		addonNamespace:            "addon",
		operatorImage:             "my-test-image",
		clusterName:               "cluster1",
		pullSecret:                "pull-secret",
		hypershiftInstallExecutor: &HypershiftTestCliExecutor{},
	}

	// Create secrets
	hcNN := types.NamespacedName{Name: "hd-1", Namespace: "clusters"}
	secrets := aCtrl.scaffoldHostedclusterSecrets(hcNN)
	for _, sec := range secrets {
		sec.SetName(fmt.Sprintf("%s-%s", hcNN.Name, sec.Name))
		aCtrl.hubClient.Create(ctx, sec)
		defer aCtrl.hubClient.Delete(ctx, sec)
	}

	// Create hosted cluster
	hc := getHostedCluster(hcNN)
	err := aCtrl.hubClient.Create(ctx, hc)
	assert.Nil(t, err, "err nil when hosted cluster is created successfull")

	// Reconcile with no annotation

	// Add annotation and reconcile
	_, err = aCtrl.Reconcile(ctx, ctrl.Request{NamespacedName: hcNN})
	assert.Nil(t, err, "err nil when reconcile was successfull")

	// Secret for kubconfig and kubeadmin-password are created
	secret := &corev1.Secret{}
	kcSecretNN := types.NamespacedName{Name: fmt.Sprintf("%s-admin-kubeconfig", hc.Spec.InfraID), Namespace: aCtrl.clusterName}
	err = aCtrl.hubClient.Get(ctx, kcSecretNN, secret)
	assert.Nil(t, err, "is nil when the admin kubeconfig secret is found")

	pwdSecretNN := types.NamespacedName{Name: fmt.Sprintf("%s-kubeadmin-password", hc.Spec.InfraID), Namespace: aCtrl.clusterName}
	err = aCtrl.hubClient.Get(ctx, pwdSecretNN, secret)
	assert.Nil(t, err, "is nil when the kubeadmin password secret is found")

	// Delete hosted cluster and reconcile
	aCtrl.hubClient.Delete(ctx, hc)
	_, err = aCtrl.Reconcile(ctx, ctrl.Request{NamespacedName: hcNN})
	assert.Nil(t, err, "err nil when reconcile was successfull")

	err = aCtrl.hubClient.Get(ctx, kcSecretNN, secret)
	assert.True(t, err != nil && errors.IsNotFound(err), "is true when the admin kubeconfig secret is deleted")
	err = aCtrl.hubClient.Get(ctx, pwdSecretNN, secret)
	assert.True(t, err != nil && errors.IsNotFound(err), "is nil when the kubeadmin password secret is deleted")
}

func getHostedCluster(hcNN types.NamespacedName) *hyperv1alpha1.HostedCluster {
	hc := &hyperv1alpha1.HostedCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "HostedCluster",
			APIVersion: "hypershift.openshift.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        hcNN.Name,
			Namespace:   hcNN.Namespace,
			Annotations: map[string]string{hypershiftDeploymentAnnoKey: "test-hd1"},
		},
		Spec: hyperv1alpha1.HostedClusterSpec{
			Platform: hyperv1alpha1.PlatformSpec{
				Type: hyperv1alpha1.AWSPlatform,
			},
			Networking: hyperv1alpha1.ClusterNetworking{
				NetworkType: hyperv1alpha1.OpenShiftSDN,
			},
			Services: []hyperv1alpha1.ServicePublishingStrategyMapping{},
			Release: hyperv1alpha1.Release{
				Image: "test-image",
			},
			Etcd: hyperv1alpha1.EtcdSpec{
				ManagementType: hyperv1alpha1.Managed,
			},
			InfraID: "infra-abcdef",
		},
		Status: hyperv1alpha1.HostedClusterStatus{
			KubeConfig:        &corev1.LocalObjectReference{Name: "kubeconfig"},
			KubeadminPassword: &corev1.LocalObjectReference{Name: "kubeadmin"},
			Conditions:        []metav1.Condition{{Type: string(hyperv1alpha1.HostedClusterAvailable), Status: metav1.ConditionTrue}},
		},
	}
	return hc
}

func TestAgentCommand(t *testing.T) {
	zapLog, _ := zap.NewDevelopment()
	cleanupCmd := NewAgentCommand("operator", zapr.NewLogger(zapLog))
	assert.Equal(t, "agent", cleanupCmd.Use)
}
