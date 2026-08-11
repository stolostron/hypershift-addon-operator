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
	csv, err := GetMCECSV(context.Background(), o.Client, o.log)
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
	csv, err = GetMCECSV(context.Background(), o.Client, o.log)
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
	csv, err = GetMCECSV(context.Background(), o.Client, o.log)
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

	err = EnableHypershiftCLIDownload(context.Background(), o.Client, o.log)
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

	// Verify the exact platform matrix uses flat hcp-<os>-<arch>.tar.gz naming (openshift/hypershift#8649)
	hrefSet := map[string]bool{}
	for _, link := range cliDownload.Spec.Links {
		hrefSet[link.Href] = true
	}
	routeHost := "" // route host is empty in tests (no real Route admission)
	expectedSuffixes := []string{
		"https://" + routeHost + "/hcp-linux-amd64.tar.gz",
		"https://" + routeHost + "/hcp-darwin-amd64.tar.gz",
		"https://" + routeHost + "/hcp-windows-amd64.tar.gz",
		"https://" + routeHost + "/hcp-linux-arm64.tar.gz",
		"https://" + routeHost + "/hcp-darwin-arm64.tar.gz",
		"https://" + routeHost + "/hcp-windows-arm64.tar.gz",
		"https://" + routeHost + "/hcp-linux-ppc64le.tar.gz",
		"https://" + routeHost + "/hcp-linux-s390x.tar.gz",
	}
	suite.Equal(len(expectedSuffixes), len(cliDownload.Spec.Links), "download link count mismatch")
	for _, expected := range expectedSuffixes {
		suite.True(hrefSet[expected], "missing expected download link: "+expected)
	}

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
	csv, err := GetMCECSV(context.Background(), o.Client, o.log)
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
	csv, err = GetMCECSV(context.Background(), o.Client, o.log)
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
	csv, err = GetMCECSV(context.Background(), o.Client, o.log)
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

	err = EnableHypershiftCLIDownload(context.Background(), o.Client, o.log)
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

// TestEnableHypershiftCLIDownloadWithImageEnvVarOverride simulates an OLM v1 install where no MCE
// ClusterServiceVersion object exists in the cluster. The hcp CLI download image should be
// taken from the HYPERSHIFT_CLI_IMAGE_NAME env var instead, so the CSV lookup/retry loop is
// skipped entirely and the route/service/deployment are still created.
//
// Named to sort alphabetically after TestEnableHypershiftCLIDownloadNoConsole so it doesn't
// change existing suite test ordering (TestEnableHypershiftCLIDownloadNoConsole relies on
// ConsoleCLIDownload state left over from TestEnableHypershiftCLIDownload).
func (suite *CLIDownloadTestSuite) TestEnableHypershiftCLIDownloadWithImageEnvVarOverride() {
	controllerContext := &controllercmd.ControllerContext{}

	o := &override{
		Client:            suite.testKubeClient,
		log:               suite.log,
		operatorNamespace: controllerContext.OperatorNamespace,
		withOverride:      false,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	// Register cancel first so it runs last among Cleanups (after resource deletes).
	suite.T().Cleanup(cancel)

	// createFixture creates obj and registers cleanup only when this test created it.
	// AlreadyExists is tolerated for fixtures left behind by earlier suite tests;
	// any other create error fails the test immediately.
	createFixture := func(obj client.Object, desc string) {
		err := o.Client.Create(ctx, obj)
		if apierrors.IsAlreadyExists(err) {
			return
		}
		suite.Require().NoError(err, "create %s", desc)
		suite.T().Cleanup(func() {
			err := o.Client.Delete(ctx, obj)
			suite.True(err == nil || apierrors.IsNotFound(err), "cleanup %s", desc)
		})
	}

	// Create mock multicluster engine. No MCE CSV is created, simulating an OLM v1 install
	// where ClusterServiceVersion objects do not exist in the cluster.
	//
	// TestEnableHypershiftCLIDownloadNoConsole (which runs before this test) does not clean up
	// the MCE/addon deployment/clusterRole it creates, so tolerate them already existing here.
	createFixture(getTestMCE("multiclusterengine", "multicluster-engine"), "test MCE")
	createFixture(getTestAddonDeployment(), "addon deployment")
	createFixture(getTestClusterRole(), "addon clusterRole")
	createFixture(getTestOCCLIDownload(), "oc cli ConsoleCLIDownload")

	const overrideImage = "registry.example/hypershift-cli:test"
	suite.T().Setenv(HypershiftCLIImageEnvVar, overrideImage)

	err := EnableHypershiftCLIDownload(ctx, o.Client, o.log)
	suite.Require().NoError(err, "EnableHypershiftCLIDownload should succeed with image env var override")

	cliDeployment := &appsv1.Deployment{}
	cliDeploymentNN := types.NamespacedName{Namespace: "multicluster-engine", Name: NewCLIDownloadResourceName}
	err = o.Client.Get(ctx, cliDeploymentNN, cliDeployment)
	suite.Require().NoError(err, "hypershift CLI download deployment should exist")
	suite.Equal(overrideImage, cliDeployment.Spec.Template.Spec.Containers[0].Image,
		"Deployment must use HYPERSHIFT_CLI_IMAGE_NAME override so OLM v1 installs get the correct hcp CLI image")
	suite.T().Cleanup(func() {
		err := o.Client.Delete(ctx, cliDeployment)
		suite.True(err == nil || apierrors.IsNotFound(err), "cleanup CLI deployment")
	})

	cliService := &corev1.Service{}
	cliServiceNN := types.NamespacedName{Namespace: "multicluster-engine", Name: NewCLIDownloadResourceName}
	err = o.Client.Get(ctx, cliServiceNN, cliService)
	suite.Require().NoError(err, "hypershift CLI download service should exist")
	suite.T().Cleanup(func() {
		err := o.Client.Delete(ctx, cliService)
		suite.True(err == nil || apierrors.IsNotFound(err), "cleanup CLI service")
	})

	cliRoute := &routev1.Route{}
	cliRouteNN := types.NamespacedName{Namespace: "multicluster-engine", Name: NewCLIDownloadResourceName}
	err = o.Client.Get(ctx, cliRouteNN, cliRoute)
	suite.Require().NoError(err, "hypershift CLI download route should exist")
	suite.T().Cleanup(func() {
		err := o.Client.Delete(ctx, cliRoute)
		suite.True(err == nil || apierrors.IsNotFound(err), "cleanup CLI route")
	})

	cliDownload := &consolev1.ConsoleCLIDownload{}
	cliDownloadNN := types.NamespacedName{Name: NewCLIDownloadResourceName}
	err = o.Client.Get(ctx, cliDownloadNN, cliDownload)
	suite.Require().NoError(err, "hypershift CLI download ConsoleCLIDownload should exist")
	suite.deleteCLIDownload(cliDownload.Name)
}

// TestEnableHypershiftCLIDownloadXCSVLookupError verifies that EnableHypershiftCLIDownload
// surfaces a wrapped error when the MCE CSV cannot be found and the retry loop's context is
// cancelled, instead of exhausting all 5 attempts (which would otherwise take several minutes
// of real wall-clock time due to the 2 minute retry interval).
//
// Named to sort alphabetically after TestEnableHypershiftCLIDownloadWithImageEnvVarOverride so
// it doesn't disturb the existing suite test ordering.
func (suite *CLIDownloadTestSuite) TestEnableHypershiftCLIDownloadXCSVLookupError() {
	o := &override{
		Client: suite.testKubeClient,
		log:    suite.log,
	}

	// No HYPERSHIFT_CLI_IMAGE_NAME override, so the CSV lookup path is exercised.
	suite.T().Setenv(HypershiftCLIImageEnvVar, "")

	// Remove any CSVs left over from earlier suite tests so the lookup fails and the retry
	// loop is entered.
	csvList := &operatorsv1alpha1.ClusterServiceVersionList{}
	suite.Require().NoError(o.Client.List(context.Background(), csvList))
	for i := range csvList.Items {
		suite.Require().NoError(o.Client.Delete(context.Background(), &csvList.Items[i]))
	}

	// Expire the context before calling in, so the retry loop's ctx.Done() case fires
	// immediately instead of sleeping for 2 minutes between attempts.
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	time.Sleep(5 * time.Millisecond)

	err := EnableHypershiftCLIDownload(ctx, o.Client, o.log)
	suite.Require().Error(err, "EnableHypershiftCLIDownload should fail when the MCE CSV lookup is cancelled")
	suite.Contains(err.Error(), "get MCE CSV", "error should be wrapped with context about the failing operation")
	suite.Contains(err.Error(), "context deadline exceeded", "error should surface the context cancellation reason")
}

// TestEnableHypershiftCLIDownloadXSkipsWithoutImage verifies that EnableHypershiftCLIDownload
// returns nil without attempting to deploy anything when neither the env var override nor the
// MCE CSV provide an hcp CLI image (the upstream build case).
//
// Named to sort alphabetically after TestEnableHypershiftCLIDownloadXCSVLookupError so it
// doesn't disturb the existing suite test ordering.
func (suite *CLIDownloadTestSuite) TestEnableHypershiftCLIDownloadXSkipsWithoutImage() {
	o := &override{
		Client: suite.testKubeClient,
		log:    suite.log,
	}
	ctx := context.Background()

	suite.T().Setenv(HypershiftCLIImageEnvVar, "")

	// Leave only an upstream CSV (no hypershift_cli related image) in place, so the CSV lookup
	// succeeds but getHypershiftCLIDownloadImage still returns "".
	csvList := &operatorsv1alpha1.ClusterServiceVersionList{}
	suite.Require().NoError(o.Client.List(ctx, csvList))
	for i := range csvList.Items {
		suite.Require().NoError(o.Client.Delete(ctx, &csvList.Items[i]))
	}
	upstreamCSV := getTestMCECSV("v2.1.0", false)
	suite.Require().NoError(o.Client.Create(ctx, upstreamCSV))
	suite.T().Cleanup(func() {
		err := o.Client.Delete(ctx, upstreamCSV)
		suite.True(err == nil || apierrors.IsNotFound(err), "cleanup upstream CSV")
	})

	err := EnableHypershiftCLIDownload(ctx, o.Client, o.log)
	suite.Require().NoError(err, "EnableHypershiftCLIDownload should skip enabling the hcp CLI download without failing when no image is available")

	// Nothing should have been deployed.
	cliDeployment := &appsv1.Deployment{}
	cliDeploymentNN := types.NamespacedName{Namespace: "multicluster-engine", Name: NewCLIDownloadResourceName}
	err = o.Client.Get(ctx, cliDeploymentNN, cliDeployment)
	suite.True(apierrors.IsNotFound(err), "hcp CLI download deployment should not be created without an image")
}

// TestEnableHypershiftCLIDownloadXDeployError verifies that EnableHypershiftCLIDownload wraps
// and returns the error when deployHCPCLIDownload fails, here because the hypershift-addon-manager
// deployment that the hcp CLI download resources are owned by cannot be found.
//
// Named to sort alphabetically after TestEnableHypershiftCLIDownloadXSkipsWithoutImage so it
// doesn't disturb the existing suite test ordering.
func (suite *CLIDownloadTestSuite) TestEnableHypershiftCLIDownloadXDeployError() {
	o := &override{
		Client: suite.testKubeClient,
		log:    suite.log,
	}
	ctx := context.Background()

	// Skip the CSV lookup so this test is isolated from CSV state left over from earlier tests.
	suite.T().Setenv(HypershiftCLIImageEnvVar, "registry.example/hypershift-cli:deploy-error-test")

	// deployHCPCLIDownload only attempts to deploy anything when at least one ConsoleCLIDownload
	// resource already exists; earlier tests clean theirs up, so create one here.
	occli := getTestOCCLIDownload()
	err := o.Client.Create(ctx, occli)
	if !apierrors.IsAlreadyExists(err) {
		suite.Require().NoError(err, "create oc cli ConsoleCLIDownload")
		suite.T().Cleanup(func() {
			err := o.Client.Delete(ctx, occli)
			suite.True(err == nil || apierrors.IsNotFound(err), "cleanup oc cli ConsoleCLIDownload")
		})
	}

	// Temporarily remove the hypershift-addon-manager deployment so getOwnerRef() fails and
	// deployHCPCLIDownload returns an error for EnableHypershiftCLIDownload to wrap.
	addonDeployment := &appsv1.Deployment{}
	addonDeploymentNN := types.NamespacedName{Namespace: "multicluster-engine", Name: "hypershift-addon-manager"}
	suite.Require().NoError(o.Client.Get(ctx, addonDeploymentNN, addonDeployment), "hypershift-addon-manager deployment should exist from earlier tests")
	suite.Require().NoError(o.Client.Delete(ctx, addonDeployment))
	suite.T().Cleanup(func() {
		err := o.Client.Create(ctx, getTestAddonDeployment())
		suite.True(err == nil || apierrors.IsAlreadyExists(err), "restore hypershift-addon-manager deployment")
	})

	err = EnableHypershiftCLIDownload(ctx, o.Client, o.log)
	suite.Require().Error(err, "EnableHypershiftCLIDownload should fail when the addon manager deployment used for the owner reference is missing")
	suite.Contains(err.Error(), "deploy HypershiftCLIDownload", "error should be wrapped with context about the failing operation")
	suite.Contains(err.Error(), "hypershift-addon-manager", "error should surface the missing owning deployment")
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
	err := EnableHypershiftCLIDownload(context.Background(), mockClient, log)
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
