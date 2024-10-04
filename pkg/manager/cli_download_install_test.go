package manager

import (
	"context"
	"log"
	"os"
	"path/filepath"
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
	"github.com/stolostron/klusterlet-addon-controller/pkg/apis"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	schemes "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

type CLIDownloadTestSuite struct {
	suite.Suite
	t              *envtest.Environment
	testKubeConfig *rest.Config
	testKubeClient client.Client
	log            logr.Logger
}

func (suite *CLIDownloadTestSuite) SetupSuite() {
	suite.t = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "hack", "crds"),
		},
	}

	apis.AddToScheme(schemes.Scheme)
	appsv1.AddToScheme(schemes.Scheme)
	corev1.AddToScheme(schemes.Scheme)
	metav1.AddMetaToScheme(schemes.Scheme)
	routev1.AddToScheme(schemes.Scheme)
	consolev1.AddToScheme(schemes.Scheme)
	appsv1.AddToScheme(schemes.Scheme)
	rbacv1.AddToScheme(schemes.Scheme)
	mcev1.AddToScheme(schemes.Scheme)
	operatorsv1alpha1.AddToScheme(schemes.Scheme)

	var err error
	if suite.testKubeConfig, err = suite.t.Start(); err != nil {
		log.Fatal(err)
	}

	if suite.testKubeClient, err = client.New(suite.testKubeConfig, client.Options{Scheme: schemes.Scheme}); err != nil {
		log.Fatal(err)
	}

	zapLog, _ := zap.NewDevelopment()
	suite.log = zapr.NewLogger(zapLog)

	err = suite.testKubeClient.Create(context.TODO(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "local-cluster"},
	})
	if err != nil {
		log.Fatal(err)
	}

	err = suite.testKubeClient.Create(context.TODO(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "multicluster-engine"},
	})
	if err != nil {
		log.Fatal(err)
	}
}

func (suite *CLIDownloadTestSuite) TearDownSuite() {
	suite.t.Stop()
}

func (suite *CLIDownloadTestSuite) SetupTest() {
}

func (suite *CLIDownloadTestSuite) TearDownTest() {
}

func (suite *CLIDownloadTestSuite) TestEnableHypershiftCLIDownload() {
	controllerContext := &controllercmd.ControllerContext{}

	o := &override{
		Client:            suite.testKubeClient,
		log:               suite.log,
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
	suite.Nil(err, "could not create test MCE")
	defer o.Client.Delete(context.TODO(), newmce)

	// This should get no MCE CSV (error case)
	csv, err := GetMCECSV(o.Client, o.log)
	suite.NotNil(err, "no MCE CSV found")

	// Create upstream MCE 2.1.0 CSV
	newcsv := getTestMCECSV("v2.1.0", false)
	err = o.Client.Create(context.TODO(), newcsv)
	suite.Nil(err, "err nil when mce csv is created successfull")
	defer o.Client.Delete(context.TODO(), newcsv)

	// Create downstream MCE 2.1.1 CSV
	newcsv = getTestMCECSV("v2.1.1", false)
	err = o.Client.Create(context.TODO(), newcsv)
	suite.Nil(err, "err nil when mce csv is created successfull")
	defer o.Client.Delete(context.TODO(), newcsv)

	// This should get upstream MCE 2.1.1 CSV
	csv, err = GetMCECSV(o.Client, o.log)
	suite.Nil(err, "err nil when mce csv is found")
	suite.Equal("multicluster-engine.v2.1.1", csv.Name)

	// upstream CSV should not contain the hypershift cli image
	cliImage := getHypershiftCLIDownloadImage(csv, o.log)
	suite.Equal("", cliImage)

	// Create downstream MCE 2.2.0 CSV
	newcsv = getTestMCECSV("v2.2.0", true)
	err = o.Client.Create(context.TODO(), newcsv)
	suite.Nil(err, "err nil when mce csv is created successfull")
	defer o.Client.Delete(context.TODO(), newcsv)

	// Create downstream MCE 2.2.1 CSV
	newcsv = getTestMCECSV("v2.2.1", true)
	err = o.Client.Create(context.TODO(), newcsv)
	suite.Nil(err, "err nil when mce csv is created successfull")
	defer o.Client.Delete(context.TODO(), newcsv)

	// This should get MCE 2.2.1 CSV
	csv, err = GetMCECSV(o.Client, o.log)
	suite.Nil(err, "err nil when mce csv is found")
	suite.Equal("multicluster-engine.v2.2.1", csv.Name)

	cliImage = getHypershiftCLIDownloadImage(csv, o.log)
	suite.Equal("https://hypershift.cli.image.io", cliImage)

	//
	// Create the hypershift addon deployment which is going to be the owner
	// of hypershift CLI deployment, service and route. When the hypershift feature
	// is disabled, the hypershift CLI deployment, service and route should be deleted.
	//
	dep := getTestAddonDeployment()
	err = o.Client.Create(context.TODO(), dep)
	suite.Nil(err, "err nil when addon deployment is created successfully")
	defer o.Client.Delete(context.TODO(), dep)

	pod := getTestAddonManagerPod()
	err = o.Client.Create(context.TODO(), pod)
	suite.Nil(err, "err nil when addon manager pod is created successfully")
	defer o.Client.Delete(context.TODO(), pod)

	os.Setenv("POD_NAME", "hypershift-addon-manager-pod")
	os.Setenv("POD_NAMESPACE", "multicluster-engine")

	//
	// Create the hypershift clusterrole which is going to be the owner
	// of hypershift ConsoleCLIDownload which is cluster scoped resource.
	// When the hypershift feature is disabled, the hypershift ConsoleCLIDownload should be deleted.
	//
	clusterRole := getTestClusterRole()
	err = o.Client.Create(context.TODO(), clusterRole)
	suite.Nil(err, "err nil when addon clusterRole is created successfully")
	defer o.Client.Delete(context.TODO(), clusterRole)

	//
	// Create the oc cli ConsoleCLIDownload to satisfy that condition that checks for
	// existing ConsoleCLIDownload to determine whether to enable ConsoleCLIDownload for hypershift
	//
	ocCliDownload := getTestOCCLIDownload()
	err = o.Client.Create(context.TODO(), ocCliDownload)
	suite.Nil(err, "err nil when oc cli ConsoleCLIDownload is created successfully")

	//
	// The deployment, service, route and ConsoleCLIDownload names used to be hypershift-cli-download
	// but changed to hcp-cli-download to align with the CLI command name. Upon MCE upgrade,
	// these old resources should be deleted. Create them and check for the deletion later.
	//
	oldCliDownload := getHypershiftCLIDownload()
	err = o.Client.Create(context.TODO(), oldCliDownload)
	suite.Nil(err, "err nil when hypershift-cli-download ConsoleCLIDownload is created successfully")

	oldCliDeployment := getHypershiftCLIDeployment()
	err = o.Client.Create(context.TODO(), oldCliDeployment)
	suite.Nil(err, "err nil when hypershift-cli-download Deployment is created successfully")
	defer o.Client.Delete(context.TODO(), oldCliDeployment)

	oldCliService := getHypershiftCLIService()
	err = o.Client.Create(context.TODO(), oldCliService)
	suite.Nil(err, "err nil when hypershift-cli-download Service is created successfully")
	defer o.Client.Delete(context.TODO(), oldCliService)

	oldCliRoute := getHypershiftCLIRoute()
	err = o.Client.Create(context.TODO(), oldCliRoute)
	suite.Nil(err, "err nil when hypershift-cli-download Route is created successfully")
	// The previous version of hypershift-cli-download resources are now created
	defer o.Client.Delete(context.TODO(), oldCliRoute)

	err = EnableHypershiftCLIDownload(o.Client, o.log)
	suite.Nil(err, "err nil when hypershift CLI download is deployed successfully")

	// Check hypershift CLI deployment
	cliDeployment := &appsv1.Deployment{}
	cliDeploymentNN := types.NamespacedName{Namespace: "multicluster-engine", Name: NewCLIDownloadResourceName}
	err = o.Client.Get(context.TODO(), cliDeploymentNN, cliDeployment)
	suite.Nil(err, "err nil when hypershift CLI download deployment exists")
	suite.Equal("hypershift-addon-manager", cliDeployment.OwnerReferences[0].Name)

	// Check hypershift CLI deployment proxy settings
	suite.Equal(3, len(cliDeployment.Spec.Template.Spec.Containers[0].Env))
	suite.True(strings.HasSuffix(cliDeployment.Spec.Template.Spec.Containers[0].Env[0].Name, "_PROXY"))
	suite.True(strings.HasSuffix(cliDeployment.Spec.Template.Spec.Containers[0].Env[1].Name, "_PROXY"))
	suite.True(strings.HasSuffix(cliDeployment.Spec.Template.Spec.Containers[0].Env[2].Name, "_PROXY"))

	foundToleration1 := false
	foundToleration2 := false
	for _, toleration := range cliDeployment.Spec.Template.Spec.Tolerations {
		if toleration.Key == "toleration-key1" {
			foundToleration1 = true
		}
		if toleration.Key == "toleration-key2" {
			foundToleration2 = true
		}
	}
	suite.True(foundToleration1)
	suite.True(foundToleration2)

	// Check hypershift CLI service
	cliService := &corev1.Service{}
	cliServiceNN := types.NamespacedName{Namespace: "multicluster-engine", Name: NewCLIDownloadResourceName}
	err = o.Client.Get(context.TODO(), cliServiceNN, cliService)
	suite.Nil(err, "err nil when hypershift CLI download service exists")
	suite.Equal("hypershift-addon-manager", cliService.OwnerReferences[0].Name)

	// Check hypershift CLI route
	cliRoute := &routev1.Route{}
	cliRouteNN := types.NamespacedName{Namespace: "multicluster-engine", Name: NewCLIDownloadResourceName}
	err = o.Client.Get(context.TODO(), cliRouteNN, cliRoute)
	suite.Nil(err, "err nil when hypershift CLI download route exists")
	suite.Equal("hypershift-addon-manager", cliRoute.OwnerReferences[0].Name)

	// Check hypershift CLI ConsoleCLIDownload
	cliDownload := &consolev1.ConsoleCLIDownload{}
	cliDownloadNN := types.NamespacedName{Name: NewCLIDownloadResourceName}
	err = o.Client.Get(context.TODO(), cliDownloadNN, cliDownload)
	suite.Nil(err, "err nil when hypershift CLI download ConsoleCLIDownload exists")
	suite.Equal("open-cluster-management:hypershift:hypershift-addon-manager", cliDownload.OwnerReferences[0].Name)

	// Check the old hypershift-cli-download resources are deleted
	removedCliDeployment := &appsv1.Deployment{}
	removedCliDeploymentNN := types.NamespacedName{Namespace: "multicluster-engine", Name: OldCLIDownloadResourceName}
	err = o.Client.Get(context.TODO(), removedCliDeploymentNN, removedCliDeployment)
	suite.True(apierrors.IsNotFound(err))

	removedCliService := &corev1.Service{}
	removedCliServiceNN := types.NamespacedName{Namespace: "multicluster-engine", Name: OldCLIDownloadResourceName}
	err = o.Client.Get(context.TODO(), removedCliServiceNN, removedCliService)
	suite.True(apierrors.IsNotFound(err))

	removecCliRoute := &routev1.Route{}
	removecCliRouteNN := types.NamespacedName{Namespace: "multicluster-engine", Name: OldCLIDownloadResourceName}
	err = o.Client.Get(context.TODO(), removecCliRouteNN, removecCliRoute)
	suite.True(apierrors.IsNotFound(err))

	removedCliDownload := &consolev1.ConsoleCLIDownload{}
	removedCliDownloadNN := types.NamespacedName{Name: OldCLIDownloadResourceName}
	err = o.Client.Get(context.TODO(), removedCliDownloadNN, removedCliDownload)
	suite.True(apierrors.IsNotFound(err))

	cliDownloadList := &consolev1.ConsoleCLIDownloadList{}
	o.Client.List(context.TODO(), cliDownloadList)
	if len(cliDownloadList.Items) > 0 {
		for _, download := range cliDownloadList.Items {
			suite.deleteCLIDownload(download.Name)
		}
	}
}

func (suite *CLIDownloadTestSuite) TestEnableHypershiftCLIDownloadNoConsole() {
	controllerContext := &controllercmd.ControllerContext{}

	o := &override{
		Client:            suite.testKubeClient,
		log:               suite.log,
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
	suite.Nil(err, "could not create test MCE")

	// This should get no MCE CSV (error case)
	csv, err := GetMCECSV(o.Client, o.log)
	suite.NotNil(err, "no MCE CSV found")

	// Create upstream MCE 2.1.0 CSV
	newcsv := getTestMCECSV("v2.1.0", false)
	err = o.Client.Create(context.TODO(), newcsv)
	suite.Nil(err, "err nil when mce csv is created successfull")

	// Create downstream MCE 2.1.1 CSV
	newcsv = getTestMCECSV("v2.1.1", false)
	err = o.Client.Create(context.TODO(), newcsv)
	suite.Nil(err, "err nil when mce csv is created successfull")

	// This should get upstream MCE 2.1.1 CSV
	csv, err = GetMCECSV(o.Client, o.log)
	suite.Nil(err, "err nil when mce csv is found")
	suite.Equal("multicluster-engine.v2.1.1", csv.Name)

	// upstream CSV should not contain the hypershift cli image
	cliImage := getHypershiftCLIDownloadImage(csv, o.log)
	suite.Equal("", cliImage)

	// Create downstream MCE 2.2.0 CSV
	newcsv = getTestMCECSV("v2.2.0", true)
	err = o.Client.Create(context.TODO(), newcsv)
	suite.Nil(err, "err nil when mce csv is created successfull")

	// Create downstream MCE 2.2.1 CSV
	newcsv = getTestMCECSV("v2.2.1", true)
	err = o.Client.Create(context.TODO(), newcsv)
	suite.Nil(err, "err nil when mce csv is created successfull")

	// This should get MCE 2.2.1 CSV
	csv, err = GetMCECSV(o.Client, o.log)
	suite.Nil(err, "err nil when mce csv is found")
	suite.Equal("multicluster-engine.v2.2.1", csv.Name)

	cliImage = getHypershiftCLIDownloadImage(csv, o.log)
	suite.Equal("https://hypershift.cli.image.io", cliImage)

	//
	// Create the hypershift addon deployment which is going to be the owner
	// of hypershift CLI deployment, service and route. When the hypershift feature
	// is disabled, the hypershift CLI deployment, service and route should be deleted.
	//
	dep := getTestAddonDeployment()
	err = o.Client.Create(context.TODO(), dep)
	suite.Nil(err, "err nil when addon deployment is created successfully")

	//
	// Create the hypershift clusterrole which is going to be the owner
	// of hypershift ConsoleCLIDownload which is cluster scoped resource.
	// When the hypershift feature is disabled, the hypershift ConsoleCLIDownload should be deleted.
	//
	clusterRole := getTestClusterRole()
	err = o.Client.Create(context.TODO(), clusterRole)
	suite.Nil(err, "err nil when addon clusterRole is created successfully")

	err = EnableHypershiftCLIDownload(o.Client, o.log)
	suite.Nil(err, "err nil when hypershift CLI download is deployed successfully")

	// Check hypershift CLI deployment
	cliDeployment := &appsv1.Deployment{}
	cliDeploymentNN := types.NamespacedName{Namespace: "multicluster-engine", Name: NewCLIDownloadResourceName}
	err = o.Client.Get(context.TODO(), cliDeploymentNN, cliDeployment)
	suite.Nil(err, "err nil when hypershift CLI download deployment exists")
	suite.Equal("hypershift-addon-manager", cliDeployment.OwnerReferences[0].Name)

	// Check hypershift CLI deployment proxy settings
	suite.Equal(3, len(cliDeployment.Spec.Template.Spec.Containers[0].Env))
	suite.True(strings.HasSuffix(cliDeployment.Spec.Template.Spec.Containers[0].Env[0].Name, "_PROXY"))
	suite.True(strings.HasSuffix(cliDeployment.Spec.Template.Spec.Containers[0].Env[1].Name, "_PROXY"))
	suite.True(strings.HasSuffix(cliDeployment.Spec.Template.Spec.Containers[0].Env[2].Name, "_PROXY"))

	// Check hypershift CLI service
	cliService := &corev1.Service{}
	cliServiceNN := types.NamespacedName{Namespace: "multicluster-engine", Name: NewCLIDownloadResourceName}
	err = o.Client.Get(context.TODO(), cliServiceNN, cliService)
	suite.Nil(err, "err nil when hypershift CLI download service exists")
	suite.Equal("hypershift-addon-manager", cliService.OwnerReferences[0].Name)

	// Check hypershift CLI route
	cliRoute := &routev1.Route{}
	cliRouteNN := types.NamespacedName{Namespace: "multicluster-engine", Name: NewCLIDownloadResourceName}
	err = o.Client.Get(context.TODO(), cliRouteNN, cliRoute)
	suite.Nil(err, "err nil when hypershift CLI download route exists")
	suite.Equal("hypershift-addon-manager", cliRoute.OwnerReferences[0].Name)

	// Check hypershift CLI ConsoleCLIDownload
	cliDownload := &consolev1.ConsoleCLIDownload{}
	cliDownloadNN := types.NamespacedName{Name: NewCLIDownloadResourceName}
	err = o.Client.Get(context.TODO(), cliDownloadNN, cliDownload)
	suite.EqualError(err, "consoleclidownloads.console.openshift.io \"hcp-cli-download\" not found")
}

func (suite *CLIDownloadTestSuite) TestRetryCSV() {
	controllerContext := &controllercmd.ControllerContext{}
	client, sch := initCSVErrorClient()

	o := &override{
		Client:            client,
		log:               suite.log,
		operatorNamespace: controllerContext.OperatorNamespace,
		withOverride:      false,
	}

	//Channel to read errors from either goroutine
	c := make(chan error)

	// Create mock multicluster engine
	newmce := getTestMCE("multiclusterengine", "multicluster-engine")
	err := o.Client.Create(context.TODO(), newmce)
	suite.Nil(err, "could not create test MCE")

	dep := getTestAddonDeployment()
	err = o.Client.Create(context.TODO(), dep)
	suite.Nil(err, "err nil when addon deployment is created successfully")

	clusterRole := getTestClusterRole()
	err = o.Client.Create(context.TODO(), clusterRole)
	suite.Nil(err, "err nil when addon clusterRole is created successfully")

	go asyncEnableHypershiftCLIDownload(client, o.log, c) //Attempt to enable clidownload
	go suite.asyncClusterRole(o, sch)                     //Add permissions after a small period of time
	result := <-c
	suite.Nil(result, "could not get MCE")

}

func TestCLIDownloadTestSuite(t *testing.T) {
	suite.Run(t, new(CLIDownloadTestSuite))
}

func (suite *CLIDownloadTestSuite) deleteCLIDownload(name string) {
	hcNN := types.NamespacedName{Name: name}

	cliDownload := &consolev1.ConsoleCLIDownload{}
	err := suite.testKubeClient.Get(context.TODO(), hcNN, cliDownload)

	if err == nil {
		suite.testKubeClient.Delete(context.TODO(), cliDownload)

		suite.Eventually(func() bool {
			cliDownloadToDelete := &consolev1.ConsoleCLIDownload{}
			err := suite.testKubeClient.Get(context.TODO(), hcNN, cliDownloadToDelete)
			return err != nil && errors.IsNotFound(err)
		}, 5*time.Second, 500*time.Millisecond)
	}
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
				StrategySpec: operatorsv1alpha1.StrategyDetailsDeployment{
					DeploymentSpecs: []operatorsv1alpha1.StrategyDeploymentSpec{},
				},
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
		Spec: consolev1.ConsoleCLIDownloadSpec{
			Links: []consolev1.CLIDownloadLink{},
		},
	}

	return cli
}

func getHypershiftCLIDownload() *consolev1.ConsoleCLIDownload {
	cli := &consolev1.ConsoleCLIDownload{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConsoleCLIDownload",
			APIVersion: "console.openshift.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: OldCLIDownloadResourceName,
		},
		Spec: consolev1.ConsoleCLIDownloadSpec{
			Links: []consolev1.CLIDownloadLink{},
		},
	}

	return cli
}

func getHypershiftCLIService() *corev1.Service {
	service := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      OldCLIDownloadResourceName,
			Namespace: "multicluster-engine",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port: 8443,
				},
			},
		},
	}

	return service
}

func getHypershiftCLIRoute() *routev1.Route {
	route := &routev1.Route{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Route",
			APIVersion: "route.openshift.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      OldCLIDownloadResourceName,
			Namespace: "multicluster-engine",
		},
		Spec: routev1.RouteSpec{},
	}

	return route
}

func getHypershiftCLIDeployment() *appsv1.Deployment {
	container := corev1.Container{
		Name:  "operator",
		Image: "https://hypershift.addon.image.io",
	}
	deployment := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      OldCLIDownloadResourceName,
			Namespace: "multicluster-engine",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "hypershift-cli-download"},
			},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{container},
				},
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "hypershift-cli-download"},
				},
			},
		},
	}

	return deployment
}

func getTestAddonManagerPod() *corev1.Pod {
	container := corev1.Container{
		Name:  "operator",
		Image: "https://hypershift.addon.image.io",
	}
	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-manager-pod",
			Namespace: "multicluster-engine",
			//	Labels:    map[string]string{"app": "hypershift-addon-manager"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{container},
			Tolerations: []corev1.Toleration{
				{
					Key:      "toleration-key1",
					Operator: "Exists",
					Effect:   "NoSchedule",
				},
				{
					Key:      "toleration-key2",
					Operator: "Exists",
					Effect:   "NoSchedule",
				},
			},
		},
	}

	return pod
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
			{
				Name: "POD_NAME",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "metadata.name",
					},
				},
			},
			{
				Name: "POD_NAMESPACE",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "metadata.namespace",
					},
				},
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
			//	Labels:    map[string]string{"app": "hypershift-addon-manager"},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "hypershift-addon-manager"},
			},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{container},
					Tolerations: []corev1.Toleration{
						{
							Key:      "toleration-key1",
							Operator: "Exists",
							Effect:   "NoSchedule",
						},
						{
							Key:      "toleration-key2",
							Operator: "Exists",
							Effect:   "NoSchedule",
						},
					},
				},
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "hypershift-addon-manager"},
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
			Name: "open-cluster-management:hypershift:hypershift-addon-manager",
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

func (suite *CLIDownloadTestSuite) asyncClusterRole(o *override, s *runtime.Scheme) {
	//Simulate adding permissions to clusterrole after a delay
	//Hard to simulate RBAC, add csv to scheme and create it after a delay instead
	time.Sleep(1 * time.Minute)

	operatorsv1alpha1.AddToScheme(s)

	newcsv := getTestMCECSV("v2.2.1", true)
	err := o.Client.Create(context.TODO(), newcsv)
	suite.Nil(err, "err nil when mce csv is created successfull")

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
