package manager

import (
	"context"
	"testing"

	"github.com/go-logr/zapr"
	consolev1 "github.com/openshift/api/console/v1"
	routev1 "github.com/openshift/api/route/v1"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	mcev1 "github.com/stolostron/backplane-operator/api/v1"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	fakeaddon "open-cluster-management.io/api/client/addon/clientset/versioned/fake"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	nodeSelector = map[string]string{"kubernetes.io/os": "linux"}
	tolerations  = []corev1.Toleration{{Key: "foo", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute}}
)

func Test_getAgentAddon(t *testing.T) {
	controllerContext := &controllercmd.ControllerContext{
		KubeConfig: &rest.Config{},
	}
	componentName := "manager"

	configs := []runtime.Object{}
	fakeAddonClient := fakeaddon.NewSimpleClientset(configs...)
	client := initClient()
	zapLog, _ := zap.NewDevelopment()
	o := &override{
		Client:            client,
		log:               zapr.NewLogger(zapLog),
		operatorNamespace: controllerContext.OperatorNamespace,
		withOverride:      false,
	}

	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "Get agent addon",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getAgentAddon(componentName, o, controllerContext, fakeAddonClient)
			if (err != nil) != tt.wantErr {
				t.Errorf("getAgentAddon() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			assert.NotNil(t, got, "agent addon is not nil")
		})
	}

	cluster := &clusterv1.ManagedCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ManagedCluster",
			APIVersion: "cluster.open-cluster-management.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster1",
		},
	}

	addon := &addonv1alpha1.ManagedClusterAddOn{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ManagedClusterAddOn",
			APIVersion: "addon.open-cluster-management.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster1addon",
			Namespace: "cluster1",
		},
	}

	o.withOverride = true

	_, err := o.getValueForAgentTemplate(cluster, addon)
	assert.NotNil(t, err, "err not nil because the override configmap does not exist")

	overrideCM := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      util.HypershiftDownstreamOverride,
			Namespace: o.operatorNamespace,
		},
		Data: map[string]string{"test": "test"},
	}
	err = o.Client.Create(context.TODO(), overrideCM)
	assert.Nil(t, err, "err nil when override configmap is created successfull")

	_, err = o.getValueForAgentTemplate(cluster, addon)
	assert.Nil(t, err, "err nil because the override configmap exists now and can generate the addon chart values")

}

func Test_newRegistrationOption(t *testing.T) {
	regOpt, err := newRegistrationOption(&rest.Config{}, "hypershift-addon", "abc12")
	assert.Nil(t, err)
	assert.NotNil(t, regOpt)
	assert.NotNil(t, regOpt.CSRConfigurations)
	assert.NotNil(t, regOpt.CSRApproveCheck)
	assert.NotNil(t, regOpt.PermissionConfig)
}

func Test_newRegistrationOption_nilConfig(t *testing.T) {
	_, err := newRegistrationOption(nil, "hypershift-addon", "abc12")
	assert.NotNil(t, err)
}

func Test_newRegistrationOptionWithClient_filtersAuthenticatedGroup(t *testing.T) {
	kubeclient := kubefake.NewSimpleClientset()
	regOpt, err := newRegistrationOptionWithClient(kubeclient, "hypershift-addon", "abc12")
	assert.NoError(t, err)
	assert.NotNil(t, regOpt)
	assert.NotNil(t, regOpt.PermissionConfig)

	cluster := &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "local-cluster"},
	}
	addon := &addonv1alpha1.ManagedClusterAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon",
			Namespace: "local-cluster",
		},
		Status: addonv1alpha1.ManagedClusterAddOnStatus{
			Registrations: []addonv1alpha1.RegistrationConfig{
				{
					SignerName: certificatesv1.KubeAPIServerClientSignerName,
					Subject: addonv1alpha1.Subject{
						User: "system:open-cluster-management:cluster:local-cluster:addon:hypershift-addon:agent:abc12",
						Groups: []string{
							"system:open-cluster-management:cluster:local-cluster:addon:hypershift-addon",
							"system:open-cluster-management:addon:hypershift-addon",
							"system:authenticated",
						},
					},
				},
			},
		},
	}

	err = regOpt.PermissionConfig(cluster, addon)
	assert.NoError(t, err)

	roleName := "open-cluster-management:hypershift-addon:agent"
	roleBinding, err := kubeclient.RbacV1().RoleBindings("local-cluster").Get(
		context.TODO(), roleName, metav1.GetOptions{})
	assert.NoError(t, err)

	clusterRoleBinding, err := kubeclient.RbacV1().ClusterRoleBindings().Get(
		context.TODO(), roleName, metav1.GetOptions{})
	assert.NoError(t, err)

	expectedUser := "system:open-cluster-management:cluster:local-cluster:addon:hypershift-addon:agent:abc12"
	expectedGroup := "system:open-cluster-management:addon:hypershift-addon"

	for _, binding := range []struct {
		name     string
		subjects []rbacv1.Subject
	}{
		{name: "RoleBinding", subjects: roleBinding.Subjects},
		{name: "ClusterRoleBinding", subjects: clusterRoleBinding.Subjects},
	} {
		var names []string
		for _, subject := range binding.subjects {
			names = append(names, subject.Name)
		}

		assert.NotContains(t, names, "system:authenticated", binding.name)
		assert.Contains(t, names, expectedUser, binding.name)
		assert.Contains(t, names, expectedGroup, binding.name)
	}
}

func Test_filterAuthenticatedGroups(t *testing.T) {
	addon := &addonv1alpha1.ManagedClusterAddOn{
		Status: addonv1alpha1.ManagedClusterAddOnStatus{
			Registrations: []addonv1alpha1.RegistrationConfig{
				{
					Subject: addonv1alpha1.Subject{
						User: "addon-agent",
						Groups: []string{
							"system:open-cluster-management:addon:hypershift-addon",
							"system:authenticated",
						},
					},
				},
			},
		},
	}

	filtered := filterAuthenticatedGroups(addon)

	assert.Equal(t, []string{"system:open-cluster-management:addon:hypershift-addon"},
		filtered.Status.Registrations[0].Subject.Groups)
	assert.Contains(t, addon.Status.Registrations[0].Subject.Groups, "system:authenticated")
}

func initClient() client.Client {
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)
	operatorsv1alpha1.AddToScheme(scheme)
	routev1.AddToScheme(scheme)
	consolev1.AddToScheme(scheme)
	appsv1.AddToScheme(scheme)
	rbacv1.AddToScheme(scheme)
	clusterv1.AddToScheme(scheme)
	addonv1alpha1.AddToScheme(scheme)
	mcev1.AddToScheme(scheme)

	ncb := fake.NewClientBuilder()
	ncb.WithScheme(scheme)
	return ncb.Build()

}
