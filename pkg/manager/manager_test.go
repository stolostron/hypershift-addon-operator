package manager

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"
	"text/template"

	"github.com/go-logr/zapr"
	configv1 "github.com/openshift/api/config/v1"
	consolev1 "github.com/openshift/api/console/v1"
	routev1 "github.com/openshift/api/route/v1"
	tlspkg "github.com/openshift/controller-runtime-common/pkg/tls"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/crypto"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	mcev1 "github.com/stolostron/backplane-operator/api/v1"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	addonv1beta1 "open-cluster-management.io/api/addon/v1beta1"
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

	addon := &addonv1beta1.ManagedClusterAddOn{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ManagedClusterAddOn",
			APIVersion: "addon.open-cluster-management.io/v1beta1",
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
	assert.NotNil(t, regOpt.Configurations)
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
	addon := &addonv1beta1.ManagedClusterAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon",
			Namespace: "local-cluster",
		},
		Status: addonv1beta1.ManagedClusterAddOnStatus{
			Registrations: []addonv1beta1.RegistrationConfig{{
				Type: addonv1beta1.KubeClient,
				KubeClient: &addonv1beta1.KubeClientConfig{
					Subject: addonv1beta1.KubeClientSubject{
						BaseSubject: addonv1beta1.BaseSubject{
							User: "system:open-cluster-management:cluster:local-cluster:addon:hypershift-addon:agent:abc12",
							Groups: []string{
								"system:open-cluster-management:cluster:local-cluster:addon:hypershift-addon",
								"system:open-cluster-management:addon:hypershift-addon",
								"system:authenticated",
							},
						},
					},
				},
			}},
		},
	}

	err = regOpt.PermissionConfig(context.TODO(), cluster, addon)
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

func Test_newRegistrationOptionWithClient_removesAuthenticatedFromExistingBindings(t *testing.T) {
	const roleName = "open-cluster-management:hypershift-addon:agent"
	clusterNS := "local-cluster"

	expectedUser := "system:open-cluster-management:cluster:local-cluster:addon:hypershift-addon:agent:abc12"
	expectedGroup := "system:open-cluster-management:addon:hypershift-addon"
	authenticatedGroup := "system:authenticated"

	kubeclient := kubefake.NewSimpleClientset(
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: roleName, Namespace: clusterNS}},
		&rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: roleName}},
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: roleName, Namespace: clusterNS},
			RoleRef:    rbacv1.RoleRef{Kind: "Role", Name: roleName, APIGroup: rbacv1.GroupName},
			Subjects: []rbacv1.Subject{
				{Kind: rbacv1.UserKind, APIGroup: rbacv1.GroupName, Name: expectedUser},
				{Kind: rbacv1.GroupKind, APIGroup: rbacv1.GroupName, Name: expectedGroup},
				{Kind: rbacv1.GroupKind, APIGroup: rbacv1.GroupName, Name: authenticatedGroup},
			},
		},
		&rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: roleName},
			RoleRef:    rbacv1.RoleRef{Kind: "ClusterRole", Name: roleName, APIGroup: rbacv1.GroupName},
			Subjects: []rbacv1.Subject{
				{Kind: rbacv1.UserKind, APIGroup: rbacv1.GroupName, Name: expectedUser},
				{Kind: rbacv1.GroupKind, APIGroup: rbacv1.GroupName, Name: expectedGroup},
				{Kind: rbacv1.GroupKind, APIGroup: rbacv1.GroupName, Name: authenticatedGroup},
			},
		},
	)

	regOpt, err := newRegistrationOptionWithClient(kubeclient, "hypershift-addon", "abc12")
	assert.NoError(t, err)

	addon := &addonv1beta1.ManagedClusterAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon",
			Namespace: clusterNS,
			UID:       "test-uid",
		},
		Status: addonv1beta1.ManagedClusterAddOnStatus{
			Registrations: []addonv1beta1.RegistrationConfig{{
				Type: addonv1beta1.KubeClient,
				KubeClient: &addonv1beta1.KubeClientConfig{
					Subject: addonv1beta1.KubeClientSubject{
						BaseSubject: addonv1beta1.BaseSubject{
							User: expectedUser,
							Groups: []string{
								expectedGroup,
								authenticatedGroup,
							},
						},
					},
				},
			}},
		},
	}

	err = regOpt.PermissionConfig(
		context.TODO(),
		&clusterv1.ManagedCluster{ObjectMeta: metav1.ObjectMeta{Name: clusterNS}},
		addon,
	)
	assert.NoError(t, err)

	for _, check := range []struct {
		name     string
		subjects func() ([]rbacv1.Subject, error)
	}{
		{
			name: "RoleBinding",
			subjects: func() ([]rbacv1.Subject, error) {
				rb, err := kubeclient.RbacV1().RoleBindings(clusterNS).Get(
					context.TODO(), roleName, metav1.GetOptions{})
				return rb.Subjects, err
			},
		},
		{
			name: "ClusterRoleBinding",
			subjects: func() ([]rbacv1.Subject, error) {
				crb, err := kubeclient.RbacV1().ClusterRoleBindings().Get(
					context.TODO(), roleName, metav1.GetOptions{})
				return crb.Subjects, err
			},
		},
	} {
		t.Run(check.name, func(t *testing.T) {
			subjects, err := check.subjects()
			assert.NoError(t, err)

			var names []string
			for _, subject := range subjects {
				names = append(names, subject.Name)
			}

			assert.NotContains(t, names, authenticatedGroup)
			assert.Contains(t, names, expectedUser)
			assert.Contains(t, names, expectedGroup)
		})
	}
}

func Test_getTLSProfileSpec(t *testing.T) {
	tests := []struct {
		name               string
		profile            *configv1.TLSSecurityProfile
		expectedMinVersion string
		expectCiphers      bool
	}{
		{
			name:               "nil profile defaults to Intermediate",
			profile:            nil,
			expectedMinVersion: "VersionTLS12",
			expectCiphers:      true,
		},
		{
			name: "Intermediate profile",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileIntermediateType,
			},
			expectedMinVersion: "VersionTLS12",
			expectCiphers:      true,
		},
		{
			name: "Modern profile uses TLS 1.3",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			},
			expectedMinVersion: "VersionTLS13",
			expectCiphers:      false,
		},
		{
			name: "Old profile uses TLS 1.0",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileOldType,
			},
			expectedMinVersion: "VersionTLS10",
			expectCiphers:      true,
		},
		{
			name: "Custom profile",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS13,
						Ciphers:       []string{},
					},
				},
			},
			expectedMinVersion: "VersionTLS13",
			expectCiphers:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profileSpec, err := tlspkg.GetTLSProfileSpec(tt.profile)
			assert.Nil(t, err)
			minVersion := string(profileSpec.MinTLSVersion)
			cipherSuites := strings.Join(crypto.OpenSSLToIANACipherSuites(profileSpec.Ciphers), ",")
			assert.Equal(t, tt.expectedMinVersion, minVersion)
			if tt.expectCiphers {
				assert.NotEmpty(t, cipherSuites, "expected cipher suites for profile")
			}
		})
	}
}

func Test_getTLSProfileValues_noAPIServer(t *testing.T) {
	zapLog, _ := zap.NewDevelopment()
	o := &override{
		Client: initClient(),
		log:    zapr.NewLogger(zapLog),
	}

	minVersion, cipherSuites := o.getTLSProfileValues()
	assert.Equal(t, "VersionTLS12", minVersion)
	assert.NotEmpty(t, cipherSuites)
}

func Test_getTLSProfileValues_withAPIServer(t *testing.T) {
	zapLog, _ := zap.NewDevelopment()
	c := initClient()

	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			},
		},
	}
	err := c.Create(context.TODO(), apiServer)
	assert.Nil(t, err)

	o := &override{
		Client: c,
		log:    zapr.NewLogger(zapLog),
	}

	minVersion, _ := o.getTLSProfileValues()
	assert.Equal(t, "VersionTLS13", minVersion)
}

func Test_getTLSProfileValues_OldProfile(t *testing.T) {
	zapLog, _ := zap.NewDevelopment()
	c := initClient()

	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileOldType,
			},
		},
	}
	err := c.Create(context.TODO(), apiServer)
	assert.Nil(t, err)

	o := &override{
		Client: c,
		log:    zapr.NewLogger(zapLog),
	}

	minVersion, cipherSuites := o.getTLSProfileValues()
	assert.Equal(t, "VersionTLS10", minVersion)
	assert.NotEmpty(t, cipherSuites, "Old profile should produce cipher suites")
}

func Test_getTLSProfileValues_CustomProfile(t *testing.T) {
	zapLog, _ := zap.NewDevelopment()
	c := initClient()

	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS12,
						Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
					},
				},
			},
		},
	}
	err := c.Create(context.TODO(), apiServer)
	assert.Nil(t, err)

	o := &override{
		Client: c,
		log:    zapr.NewLogger(zapLog),
	}

	minVersion, cipherSuites := o.getTLSProfileValues()
	assert.Equal(t, "VersionTLS12", minVersion)
	assert.Contains(t, cipherSuites, "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
		"Custom profile cipher should be converted from OpenSSL to IANA name")
}

func Test_getTLSProfileValues_IntermediateProfile(t *testing.T) {
	zapLog, _ := zap.NewDevelopment()
	c := initClient()

	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileIntermediateType,
			},
		},
	}
	err := c.Create(context.TODO(), apiServer)
	assert.Nil(t, err)

	o := &override{
		Client: c,
		log:    zapr.NewLogger(zapLog),
	}

	minVersion, cipherSuites := o.getTLSProfileValues()
	assert.Equal(t, "VersionTLS12", minVersion)
	assert.NotEmpty(t, cipherSuites, "Intermediate profile should produce cipher suites")
}

func Test_getTLSProfileValues_NilTLSSecurityProfile(t *testing.T) {
	zapLog, _ := zap.NewDevelopment()
	c := initClient()

	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{},
	}
	err := c.Create(context.TODO(), apiServer)
	assert.Nil(t, err)

	o := &override{
		Client: c,
		log:    zapr.NewLogger(zapLog),
	}

	minVersion, cipherSuites := o.getTLSProfileValues()
	assert.Equal(t, "VersionTLS12", minVersion,
		"nil TLSSecurityProfile on APIServer should default to Intermediate (TLS 1.2)")
	assert.NotEmpty(t, cipherSuites)
}

func initClient() client.Client {
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)
	configv1.Install(scheme)
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

func findRBACRule(rules []rbacv1.PolicyRule, apiGroup, resource string) *rbacv1.PolicyRule {
	for i := range rules {
		for _, ag := range rules[i].APIGroups {
			if ag != apiGroup {
				continue
			}
			for _, res := range rules[i].Resources {
				if res == resource {
					return &rules[i]
				}
			}
		}
	}
	return nil
}

func Test_DeploymentTemplateRendersTLSFlags(t *testing.T) {
	tmplData, err := fs.ReadFile("manifests/templates/deployment.yaml")
	assert.Nil(t, err, "should read embedded deployment template")

	funcMap := template.FuncMap{
		"regexMatch": func(pattern, input string) bool {
			matched, _ := regexp.MatchString(pattern, input)
			return matched
		},
	}

	tmpl, err := template.New("deployment").Funcs(funcMap).Parse(string(tmplData))
	assert.Nil(t, err, "should parse deployment template")

	tests := []struct {
		name            string
		tlsMinVersion   string
		tlsCipherSuites string
		expectMinVer    string
		expectCiphers   bool
	}{
		{
			name:            "Intermediate profile renders min version and ciphers",
			tlsMinVersion:   "VersionTLS12",
			tlsCipherSuites: "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
			expectMinVer:    "--tls-min-version=VersionTLS12",
			expectCiphers:   true,
		},
		{
			name:            "Modern profile renders min version without ciphers",
			tlsMinVersion:   "VersionTLS13",
			tlsCipherSuites: "",
			expectMinVer:    "--tls-min-version=VersionTLS13",
			expectCiphers:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rendered bytes.Buffer
			err = tmpl.Execute(&rendered, map[string]interface{}{
				"TLSMinVersion":                       tt.tlsMinVersion,
				"TLSCipherSuites":                     tt.tlsCipherSuites,
				"AddonName":                           "hypershift-addon",
				"SpokeRolebindingName":                "test-cluster-hypershift-addon",
				"AddonInstallNamespace":               "open-cluster-management-agent-addon",
				"Image":                               "quay.io/test/image:latest",
				"SpokeClusterName":                    "test-cluster",
				"PullSecret":                          "pull-secret",
				"MetricsServiceCert":                  "metrics-cert",
				"RBACProxyImage":                      "quay.io/test/kube-rbac-proxy:latest",
				"EnableKubeRBACProxy":                 true,
				"EnableMCEDiscovery":                  false,
				"ConfigureMCEImport":                  false,
				"ClusterAPIServerIP":                  "10.0.0.1",
				"HypershiftDownstreamImage":           "",
				"PullSecretData":                      "",
				"MulticlusterEnginePullSecret":        "",
				"HypershiftDownstreamOverride":        "",
				"HypershiftDownstreamOverrideContent": "",
				"ImageOverrides":                      []interface{}{},
			})
			assert.Nil(t, err, "should render deployment template")

			output := rendered.String()
			assert.Contains(t, output, tt.expectMinVer,
				"rendered deployment should contain --tls-min-version flag")

			if tt.expectCiphers {
				assert.Contains(t, output, "--tls-cipher-suites="+tt.tlsCipherSuites,
					"rendered deployment should contain --tls-cipher-suites flag")
			} else {
				assert.NotContains(t, output, "--tls-cipher-suites",
					"rendered deployment should NOT contain --tls-cipher-suites when empty")
			}
		})
	}
}

func Test_ClusterRoleContainsClusterAPIPermissions(t *testing.T) {
	tmplData, err := fs.ReadFile("manifests/templates/clusterrole.yaml")
	assert.Nil(t, err, "should read embedded clusterrole template")

	tmpl, err := template.New("clusterrole").Parse(string(tmplData))
	assert.Nil(t, err, "should parse template")

	var rendered bytes.Buffer
	err = tmpl.Execute(&rendered, map[string]string{
		"SpokeRolebindingName":  "test-cluster-hypershift-addon",
		"AddonInstallNamespace": "open-cluster-management-agent-addon",
	})
	assert.Nil(t, err, "should render template")

	clusterRole := &rbacv1.ClusterRole{}
	err = yaml.NewYAMLOrJSONDecoder(&rendered, 4096).Decode(clusterRole)
	assert.Nil(t, err, "should decode rendered ClusterRole")

	rule := findRBACRule(clusterRole.Rules, "operator.openshift.io", "clusterapis")
	assert.NotNil(t, rule,
		"ClusterRole must include operator.openshift.io/clusterapis rule "+
			"(required by hypershift CAPI coordination, see openshift/hypershift#7996)")

	if rule != nil {
		verbSet := make(map[string]bool)
		for _, v := range rule.Verbs {
			verbSet[v] = true
		}
		for _, required := range []string{"get", "list", "watch", "patch"} {
			assert.True(t, verbSet[required],
				"operator.openshift.io/clusterapis must include verb %q", required)
		}
	}
}

func Test_ClusterRoleContainsAPIServerTLSPermissions(t *testing.T) {
	tmplData, err := fs.ReadFile("manifests/templates/clusterrole.yaml")
	assert.Nil(t, err, "should read embedded clusterrole template")

	tmpl, err := template.New("clusterrole").Parse(string(tmplData))
	assert.Nil(t, err, "should parse template")

	var rendered bytes.Buffer
	err = tmpl.Execute(&rendered, map[string]string{
		"SpokeRolebindingName":  "test-cluster-hypershift-addon",
		"AddonInstallNamespace": "open-cluster-management-agent-addon",
	})
	assert.Nil(t, err, "should render template")

	clusterRole := &rbacv1.ClusterRole{}
	err = yaml.NewYAMLOrJSONDecoder(&rendered, 4096).Decode(clusterRole)
	assert.Nil(t, err, "should decode rendered ClusterRole")

	rule := findRBACRule(clusterRole.Rules, "config.openshift.io", "apiservers")
	assert.NotNil(t, rule,
		"ClusterRole must include config.openshift.io/apiservers rule "+
			"(required for dynamic TLS profile configuration, ACM-30178)")

	if rule != nil {
		verbSet := make(map[string]bool)
		for _, v := range rule.Verbs {
			verbSet[v] = true
		}
		for _, required := range []string{"get", "list", "watch"} {
			assert.True(t, verbSet[required],
				"config.openshift.io/apiservers must include verb %q", required)
		}
	}
}

func Test_ClusterRoleContainsPodDisruptionBudgetPermissions(t *testing.T) {
	tmplData, err := fs.ReadFile("manifests/templates/clusterrole.yaml")
	assert.Nil(t, err, "should read embedded clusterrole template")

	tmpl, err := template.New("clusterrole").Parse(string(tmplData))
	assert.Nil(t, err, "should parse template")

	var rendered bytes.Buffer
	err = tmpl.Execute(&rendered, map[string]string{
		"SpokeRolebindingName":  "test-cluster-hypershift-addon",
		"AddonInstallNamespace": "open-cluster-management-agent-addon",
	})
	assert.Nil(t, err, "should render template")

	clusterRole := &rbacv1.ClusterRole{}
	err = yaml.NewYAMLOrJSONDecoder(&rendered, 4096).Decode(clusterRole)
	assert.Nil(t, err, "should decode rendered ClusterRole")

	rule := findRBACRule(clusterRole.Rules, "policy", "poddisruptionbudgets")
	assert.NotNil(t, rule,
		"ClusterRole must include policy/poddisruptionbudgets rule "+
			"(required to apply the hypershift operator PodDisruptionBudget, OCPBUGS-114936)")

	if rule != nil {
		verbSet := make(map[string]bool)
		for _, v := range rule.Verbs {
			verbSet[v] = true
		}
		for _, required := range []string{"get", "list", "watch", "create", "patch", "update", "delete"} {
			assert.True(t, verbSet[required],
				"policy/poddisruptionbudgets must include verb %q", required)
		}
	}
}
