package manager

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	consolev1 "github.com/openshift/api/console/v1"
	routev1 "github.com/openshift/api/route/v1"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	mcev1 "github.com/stolostron/backplane-operator/api/v1"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestEnableHypershiftCLIDownload(t *testing.T) {
	controllerContext := &controllercmd.ControllerContext{}

	client := initClient()
	zapLog, _ := zap.NewDevelopment()
	o := &override{
		Client:            client,
		log:               zapr.NewLogger(zapLog),
		operatorNamespace: controllerContext.OperatorNamespace,
		withOverride:      false,
	}

	//
	// This section tests that we can find the correct MCE CSV
	// and get the hypershift CLI container image reference from the CSV
	//

	// Create mock multicluster engine
	newmce := getTestMCE("multiclusterengine", "multicluster-engine")
	err := o.Client.Create(context.TODO(), newmce)
	assert.Nil(t, err, "could not create test MCE")

	// This should get no MCE CSV (error case)
	csv, err := GetMCECSV(o.Client, o.log)
	assert.NotNil(t, err, "no MCE CSV found")

	// Create upstream MCE 2.1.0 CSV
	newcsv := getTestMCECSV("v2.1.0", false)
	err = o.Client.Create(context.TODO(), newcsv)
	assert.Nil(t, err, "err nil when mce csv is created successfull")

	// Create downstream MCE 2.1.1 CSV
	newcsv = getTestMCECSV("v2.1.1", false)
	err = o.Client.Create(context.TODO(), newcsv)
	assert.Nil(t, err, "err nil when mce csv is created successfull")

	// This should get upstream MCE 2.1.1 CSV
	csv, err = GetMCECSV(o.Client, o.log)
	assert.Nil(t, err, "err nil when mce csv is found")
	assert.Equal(t, "multicluster-engine.v2.1.1", csv.Name)

	// upstream CSV should not contain the hypershift cli image
	cliImage := getHypershiftCLIDownloadImage(csv, o.log)
	assert.Equal(t, "", cliImage)

	// Create downstream MCE 2.2.0 CSV
	newcsv = getTestMCECSV("v2.2.0", true)
	err = o.Client.Create(context.TODO(), newcsv)
	assert.Nil(t, err, "err nil when mce csv is created successfull")

	// Create downstream MCE 2.2.1 CSV
	newcsv = getTestMCECSV("v2.2.1", true)
	err = o.Client.Create(context.TODO(), newcsv)
	assert.Nil(t, err, "err nil when mce csv is created successfull")

	// This should get MCE 2.2.1 CSV
	csv, err = GetMCECSV(o.Client, o.log)
	assert.Nil(t, err, "err nil when mce csv is found")
	assert.Equal(t, "multicluster-engine.v2.2.1", csv.Name)

	cliImage = getHypershiftCLIDownloadImage(csv, o.log)
	assert.Equal(t, "https://hypershift.cli.image.io", cliImage)

	//
	// Create the hypershift addon deployment which is going to be the owner
	// of hypershift CLI deployment, service and route. When the hypershift feature
	// is disabled, the hypershift CLI deployment, service and route should be deleted.
	//
	dep := getTestAddonDeployment()
	err = o.Client.Create(context.TODO(), dep)
	assert.Nil(t, err, "err nil when addon deployment is created successfully")

	//
	// Create the hypershift clusterrole which is going to be the owner
	// of hypershift ConsoleCLIDownload which is cluster scoped resource.
	// When the hypershift feature is disabled, the hypershift ConsoleCLIDownload should be deleted.
	//
	clusterRole := getTestClusterRole()
	err = o.Client.Create(context.TODO(), clusterRole)
	assert.Nil(t, err, "err nil when addon clusterRole is created successfully")

	//
	// Create the oc cli ConsoleCLIDownload to satisfy that condition that checks for
	// existing ConsoleCLIDownload to determine whether to enable ConsoleCLIDownload for hypershift
	//
	ocCliDownload := getTestOCCLIDownload()
	err = o.Client.Create(context.TODO(), ocCliDownload)
	assert.Nil(t, err, "err nil when oc cli ConsoleCLIDownload is created successfully")

	err = EnableHypershiftCLIDownload(o.Client, o.log)
	assert.Nil(t, err, "err nil when hypershift CLI download is deployed successfully")

	// Check hypershift CLI deployment
	cliDeployment := &appsv1.Deployment{}
	cliDeploymentNN := types.NamespacedName{Namespace: "multicluster-engine", Name: "hcp-cli-download"}
	err = o.Client.Get(context.TODO(), cliDeploymentNN, cliDeployment)
	assert.Nil(t, err, "err nil when hypershift CLI download deployment exists")
	assert.Equal(t, "hypershift-addon-manager", cliDeployment.OwnerReferences[0].Name)

	// Check hypershift CLI deployment proxy settings
	assert.Equal(t, 3, len(cliDeployment.Spec.Template.Spec.Containers[0].Env))
	assert.True(t, strings.HasSuffix(cliDeployment.Spec.Template.Spec.Containers[0].Env[0].Name, "_PROXY"))
	assert.True(t, strings.HasSuffix(cliDeployment.Spec.Template.Spec.Containers[0].Env[1].Name, "_PROXY"))
	assert.True(t, strings.HasSuffix(cliDeployment.Spec.Template.Spec.Containers[0].Env[2].Name, "_PROXY"))

	// Check hypershift CLI service
	cliService := &corev1.Service{}
	cliServiceNN := types.NamespacedName{Namespace: "multicluster-engine", Name: "hcp-cli-download"}
	err = o.Client.Get(context.TODO(), cliServiceNN, cliService)
	assert.Nil(t, err, "err nil when hypershift CLI download service exists")
	assert.Equal(t, "hypershift-addon-manager", cliService.OwnerReferences[0].Name)

	// Check hypershift CLI route
	cliRoute := &routev1.Route{}
	cliRouteNN := types.NamespacedName{Namespace: "multicluster-engine", Name: "hcp-cli-download"}
	err = o.Client.Get(context.TODO(), cliRouteNN, cliRoute)
	assert.Nil(t, err, "err nil when hypershift CLI download route exists")
	assert.Equal(t, "hypershift-addon-manager", cliRoute.OwnerReferences[0].Name)

	// Check hypershift CLI ConsoleCLIDownload
	cliDownload := &consolev1.ConsoleCLIDownload{}
	cliDownloadNN := types.NamespacedName{Name: "hcp-cli-download"}
	err = o.Client.Get(context.TODO(), cliDownloadNN, cliDownload)
	assert.Nil(t, err, "err nil when hypershift CLI download ConsoleCLIDownload exists")
	assert.Equal(t, "open-cluster-management:hypershift-preview:hypershift-addon-manager", cliDownload.OwnerReferences[0].Name)
}

func TestEnableHypershiftCLIDownloadNoConsole(t *testing.T) {
	controllerContext := &controllercmd.ControllerContext{}

	client := initClient()
	zapLog, _ := zap.NewDevelopment()
	o := &override{
		Client:            client,
		log:               zapr.NewLogger(zapLog),
		operatorNamespace: controllerContext.OperatorNamespace,
		withOverride:      false,
	}

	//
	// This section tests that we can find the correct MCE CSV
	// and get the hypershift CLI container image reference from the CSV
	//

	// Create mock multicluster engine
	newmce := getTestMCE("multiclusterengine", "multicluster-engine")
	err := o.Client.Create(context.TODO(), newmce)
	assert.Nil(t, err, "could not create test MCE")

	// This should get no MCE CSV (error case)
	csv, err := GetMCECSV(o.Client, o.log)
	assert.NotNil(t, err, "no MCE CSV found")

	// Create upstream MCE 2.1.0 CSV
	newcsv := getTestMCECSV("v2.1.0", false)
	err = o.Client.Create(context.TODO(), newcsv)
	assert.Nil(t, err, "err nil when mce csv is created successfull")

	// Create downstream MCE 2.1.1 CSV
	newcsv = getTestMCECSV("v2.1.1", false)
	err = o.Client.Create(context.TODO(), newcsv)
	assert.Nil(t, err, "err nil when mce csv is created successfull")

	// This should get upstream MCE 2.1.1 CSV
	csv, err = GetMCECSV(o.Client, o.log)
	assert.Nil(t, err, "err nil when mce csv is found")
	assert.Equal(t, "multicluster-engine.v2.1.1", csv.Name)

	// upstream CSV should not contain the hypershift cli image
	cliImage := getHypershiftCLIDownloadImage(csv, o.log)
	assert.Equal(t, "", cliImage)

	// Create downstream MCE 2.2.0 CSV
	newcsv = getTestMCECSV("v2.2.0", true)
	err = o.Client.Create(context.TODO(), newcsv)
	assert.Nil(t, err, "err nil when mce csv is created successfull")

	// Create downstream MCE 2.2.1 CSV
	newcsv = getTestMCECSV("v2.2.1", true)
	err = o.Client.Create(context.TODO(), newcsv)
	assert.Nil(t, err, "err nil when mce csv is created successfull")

	// This should get MCE 2.2.1 CSV
	csv, err = GetMCECSV(o.Client, o.log)
	assert.Nil(t, err, "err nil when mce csv is found")
	assert.Equal(t, "multicluster-engine.v2.2.1", csv.Name)

	cliImage = getHypershiftCLIDownloadImage(csv, o.log)
	assert.Equal(t, "https://hypershift.cli.image.io", cliImage)

	//
	// Create the hypershift addon deployment which is going to be the owner
	// of hypershift CLI deployment, service and route. When the hypershift feature
	// is disabled, the hypershift CLI deployment, service and route should be deleted.
	//
	dep := getTestAddonDeployment()
	err = o.Client.Create(context.TODO(), dep)
	assert.Nil(t, err, "err nil when addon deployment is created successfully")

	//
	// Create the hypershift clusterrole which is going to be the owner
	// of hypershift ConsoleCLIDownload which is cluster scoped resource.
	// When the hypershift feature is disabled, the hypershift ConsoleCLIDownload should be deleted.
	//
	clusterRole := getTestClusterRole()
	err = o.Client.Create(context.TODO(), clusterRole)
	assert.Nil(t, err, "err nil when addon clusterRole is created successfully")

	err = EnableHypershiftCLIDownload(o.Client, o.log)
	assert.Nil(t, err, "err nil when hypershift CLI download is deployed successfully")

	// Check hypershift CLI deployment
	cliDeployment := &appsv1.Deployment{}
	cliDeploymentNN := types.NamespacedName{Namespace: "multicluster-engine", Name: "hcp-cli-download"}
	err = o.Client.Get(context.TODO(), cliDeploymentNN, cliDeployment)
	assert.Nil(t, err, "err nil when hypershift CLI download deployment exists")
	assert.Equal(t, "hypershift-addon-manager", cliDeployment.OwnerReferences[0].Name)

	// Check hypershift CLI deployment proxy settings
	assert.Equal(t, 3, len(cliDeployment.Spec.Template.Spec.Containers[0].Env))
	assert.True(t, strings.HasSuffix(cliDeployment.Spec.Template.Spec.Containers[0].Env[0].Name, "_PROXY"))
	assert.True(t, strings.HasSuffix(cliDeployment.Spec.Template.Spec.Containers[0].Env[1].Name, "_PROXY"))
	assert.True(t, strings.HasSuffix(cliDeployment.Spec.Template.Spec.Containers[0].Env[2].Name, "_PROXY"))

	// Check hypershift CLI service
	cliService := &corev1.Service{}
	cliServiceNN := types.NamespacedName{Namespace: "multicluster-engine", Name: "hcp-cli-download"}
	err = o.Client.Get(context.TODO(), cliServiceNN, cliService)
	assert.Nil(t, err, "err nil when hypershift CLI download service exists")
	assert.Equal(t, "hypershift-addon-manager", cliService.OwnerReferences[0].Name)

	// Check hypershift CLI route
	cliRoute := &routev1.Route{}
	cliRouteNN := types.NamespacedName{Namespace: "multicluster-engine", Name: "hcp-cli-download"}
	err = o.Client.Get(context.TODO(), cliRouteNN, cliRoute)
	assert.Nil(t, err, "err nil when hypershift CLI download route exists")
	assert.Equal(t, "hypershift-addon-manager", cliRoute.OwnerReferences[0].Name)

	// Check hypershift CLI ConsoleCLIDownload
	cliDownload := &consolev1.ConsoleCLIDownload{}
	cliDownloadNN := types.NamespacedName{Name: "hcp-cli-download"}
	err = o.Client.Get(context.TODO(), cliDownloadNN, cliDownload)
	assert.EqualError(t, err, "consoleclidownloads.console.openshift.io \"hcp-cli-download\" not found")
}

func TestRetryCSV(t *testing.T) {
	controllerContext := &controllercmd.ControllerContext{}
	client, sch := initCSVErrorClient()
	zapLog, _ := zap.NewDevelopment()
	o := &override{
		Client:            client,
		log:               zapr.NewLogger(zapLog),
		operatorNamespace: controllerContext.OperatorNamespace,
		withOverride:      false,
	}

	//Channel to read errors from either goroutine
	c := make(chan error)

	// Create mock multicluster engine
	newmce := getTestMCE("multiclusterengine", "multicluster-engine")
	err := o.Client.Create(context.TODO(), newmce)
	assert.Nil(t, err, "could not create test MCE")

	dep := getTestAddonDeployment()
	err = o.Client.Create(context.TODO(), dep)
	assert.Nil(t, err, "err nil when addon deployment is created successfully")

	clusterRole := getTestClusterRole()
	err = o.Client.Create(context.TODO(), clusterRole)
	assert.Nil(t, err, "err nil when addon clusterRole is created successfully")

	go asyncEnableHypershiftCLIDownload(client, o.log, c) //Attempt to enable clidownload
	go asyncClusterRole(o, sch, t)                        //Add permissions after a small period of time
	result := <-c
	assert.Nil(t, result, "could not get MCE")

}

func getTestMCECSV(version string, downstream bool) *operatorsv1alpha1.ClusterServiceVersion {
	csv := &operatorsv1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterServiceVersion",
			APIVersion: "operators.coreos.com/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "multicluster-engine." + version,
			Namespace: "multicluster-engine",
		},
		Spec: operatorsv1alpha1.ClusterServiceVersionSpec{
			InstallStrategy: operatorsv1alpha1.NamedInstallStrategy{
				StrategyName: "deployment",
			},
			DisplayName: "multicluster engine for Kubernetes",
		},
	}

	if downstream {
		csv.Spec.RelatedImages = []operatorsv1alpha1.RelatedImage{
			{
				Name:  "hypershift_cli",
				Image: "https://hypershift.cli.image.io",
			},
		}
	}
	return csv
}

func getTestOCCLIDownload() *consolev1.ConsoleCLIDownload {
	cli := &consolev1.ConsoleCLIDownload{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConsoleCLIDownload",
			APIVersion: "console.openshift.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "oc-cli-downloads",
		},
		Spec: consolev1.ConsoleCLIDownloadSpec{},
	}

	return cli
}

func getTestAddonDeployment() *appsv1.Deployment {
	container := corev1.Container{
		Name:  "operator",
		Image: "https://hypershift.addon.image.io",
		Env: []corev1.EnvVar{
			{
				Name:  "HTTP_PROXY",
				Value: "1.2.3.4",
			},
			{
				Name:  "HTTPS_PROXY",
				Value: "5.6.7.8",
			},
			{
				Name:  "NO_PROXY",
				Value: "9.1.2.3",
			},
		},
	}

	dep := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-manager",
			Namespace: "multicluster-engine",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "hypershift-addon-manager"},
			},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{container},
				},
			},
		},
	}

	return dep
}

func getTestClusterRole() *rbacv1.ClusterRole {
	clusterRole := &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRole",
			APIVersion: "rbac.authorization.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management:hypershift-preview:hypershift-addon-manager",
		},
	}
	return clusterRole
}

func getTestMCE(name string, namespace string) *mcev1.MultiClusterEngine {
	mce := &mcev1.MultiClusterEngine{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: mcev1.MultiClusterEngineSpec{
			TargetNamespace: namespace,
		},
	}
	return mce
}

func asyncClusterRole(o *override, s *runtime.Scheme, t *testing.T) {
	//Simulate adding permissions to clusterrole after a delay
	//Hard to simulate RBAC, add csv to scheme and create it after a delay instead
	time.Sleep(1 * time.Minute)

	operatorsv1alpha1.AddToScheme(s)

	newcsv := getTestMCECSV("v2.2.1", true)
	err := o.Client.Create(context.TODO(), newcsv)
	assert.Nil(t, err, "err nil when mce csv is created successfull")

}

func asyncEnableHypershiftCLIDownload(mockClient client.Client, log logr.Logger, c chan error) {
	err := EnableHypershiftCLIDownload(mockClient, log)
	c <- err
	log.Info("Successfully enabled HypershiftCLIDownload after retrying")
	if err != nil {
		log.Error(err, "Could not enable HypershiftCLIDownload after retrying")
	}
}

func initCSVErrorClient() (client.Client, *runtime.Scheme) {
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)
	routev1.AddToScheme(scheme)
	consolev1.AddToScheme(scheme)
	appsv1.AddToScheme(scheme)
	rbacv1.AddToScheme(scheme)
	mcev1.AddToScheme(scheme)

	ncb := fake.NewClientBuilder()
	ncb.WithScheme(scheme)
	return ncb.Build(), scheme
}
