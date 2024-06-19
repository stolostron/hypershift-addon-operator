package manager

import (
	"context"
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	"github.com/go-logr/logr"
	consolev1 "github.com/openshift/api/console/v1"
	routev1 "github.com/openshift/api/route/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	mcev1 "github.com/stolostron/backplane-operator/api/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	NewCLIDownloadResourceName = "hcp-cli-download"
	OldCLIDownloadResourceName = "hypershift-cli-download"
)

func EnableHypershiftCLIDownload(hubclient client.Client, log logr.Logger) error {
	// get the current version of MCE CSV from multicluster-engine namespace

	//Every 2 minutes, try to get csv in case of cluster upgrade (5 attempts)
	var csv *operatorsv1alpha1.ClusterServiceVersion
	var err error
	for try := 1; try <= 5; try++ {
		if try != 1 {
			log.Error(err, "failed to get the most current version of MCE CSV from multicluster-engine namespace, retrying in 2 minutes (attempt "+strconv.Itoa(try)+"/5)")
			time.Sleep(2 * time.Minute)
		}
		csv, err = GetMCECSV(hubclient, log)
		if err == nil {
			break
		}
	}

	//Failed 5 attempts
	if err != nil {
		log.Error(err, "failed to get the most current version of MCE CSV from multicluster-engine namespace")
		return err
	}

	// check if the CSV has hypershift_cli image, which is the downstream case
	cliDownloadImage := getHypershiftCLIDownloadImage(csv, log)
	if cliDownloadImage == "" {
		// in upstream build, there is no hcp CLI download image
		log.Info("the hcp CLI download image was not found in the CSV. Skip enabling the hcp CLI download")
		return nil
	}

	err = deployHCPCLIDownload(hubclient, cliDownloadImage, log)
	if err != nil {
		log.Error(err, "failed to deploy HypershiftCLIDownload")
		return err
	}

	return err
}

func GetMCECSV(hubclient client.Client, log logr.Logger) (*operatorsv1alpha1.ClusterServiceVersion, error) {
	csvlist := &operatorsv1alpha1.ClusterServiceVersionList{}

	//listopts := &client.ListOptions{Namespace: "multicluster-engine"}

	err := hubclient.List(context.TODO(), csvlist)

	if err != nil {
		log.Error(err, "failed to list CSVs")
		return nil, err
	}

	var names []string
	var mce_namespace string
	for _, csv := range csvlist.Items {
		if strings.HasPrefix(csv.Name, "multicluster-engine.") {
			names = append(names, csv.Name)
			mce_namespace = csv.Namespace
		}
	}

	if len(names) == 0 {
		err := errors.New("no MCE CSV found")
		log.Error(err, "no MCE CSV found")
		return nil, err
	}

	// find the latest version of MCE CSV
	sort.Sort(sort.Reverse(sort.StringSlice(names)))

	csv := &operatorsv1alpha1.ClusterServiceVersion{}
	csvNN := types.NamespacedName{Namespace: mce_namespace, Name: names[0]}

	err = hubclient.Get(context.TODO(), csvNN, csv)

	if err != nil {
		log.Error(err, "failed to get MCE CSV "+names[0])
		return nil, err
	}

	log.Info("MCE CSV found " + csv.Name)
	return csv, nil
}

func getHypershiftCLIDownloadImage(csv *operatorsv1alpha1.ClusterServiceVersion, log logr.Logger) string {
	for _, relatedImage := range csv.Spec.RelatedImages {
		if strings.EqualFold(relatedImage.Name, "hypershift_cli") && relatedImage.Image != "" {
			log.Info("the hcp CLI download image was found in the CSV " + relatedImage.Image)
			return relatedImage.Image
		}
	}

	return ""
}

func deployHCPCLIDownload(hubclient client.Client, cliImage string, log logr.Logger) error {
	// Set owner reference to the addon manager deployment so that when the feature is disabled, HypershiftCLIDownload
	// is uninstalled
	ownerRef, envVars, installNamespace, err := getOwnerRef(hubclient, log)
	if err != nil {
		log.Error(err, "failed to get owner reference for hcp-cli-download. abort.")
		return err
	}

	// CLI download resources are renamed. Remove resources with old names
	removeHypershiftCLIDownload(hubclient, installNamespace, log)

	log.Info("deploying hcp CLI download in namespace " + installNamespace)

	// Deployment
	deployment, err := getCLIDeployment(cliImage, envVars, log, installNamespace, hubclient)
	if err != nil {
		log.Error(err, "failed to prepare hcp-cli-download deployment")
		return err
	}
	// set ownerRef for garbage collection after the hypershift feature is disabled
	deployment.SetOwnerReferences([]metav1.OwnerReference{*ownerRef})
	_, err = controllerutil.CreateOrUpdate(context.TODO(), hubclient, deployment, func() error { return nil })
	if err != nil {
		log.Error(err, "failed to create or update hcp-cli-download deployment")
		return err
	}
	log.Info("hcp-cli-download deployment was applied successfully")

	// Service
	service, err := getService(log, installNamespace)
	if err != nil {
		log.Error(err, "failed to prepare hcp-cli-download service")
		return err
	}
	// set ownerRef for garbage collection after the hypershift feature is disabled
	service.SetOwnerReferences([]metav1.OwnerReference{*ownerRef})
	_, err = controllerutil.CreateOrUpdate(context.TODO(), hubclient, service, func() error { return nil })
	if err != nil {
		log.Error(err, "failed to create or update hcp-cli-download service")
		return err
	}
	log.Info("hcp-cli-download service was applied successfully")

	// Route
	route, err := getRoute(log, installNamespace)
	if err != nil {
		log.Error(err, "failed to prepare hcp-cli-download route")
		return err
	}
	// set ownerRef for garbage collection after the hypershift feature is disabled
	route.SetOwnerReferences([]metav1.OwnerReference{*ownerRef})
	_, err = controllerutil.CreateOrUpdate(context.TODO(), hubclient, route, func() error { return nil })
	if err != nil {
		log.Error(err, "failed to create or update hcp-cli-download route")
		return err
	}
	log.Info("hcp-cli-download route was applied successfully")

	// Get the route to get the URL
	err = hubclient.Get(context.TODO(), types.NamespacedName{Namespace: route.Namespace, Name: route.Name}, route)
	if err != nil {
		log.Error(err, "failed to get hcp-cli-download route")
		return err
	}

	// ConsoleCLIDownload is a cluster scoped resource so we need to set the owner reference with a cluster scoped resource
	// The hypershift addon cluster role is a cluster scroped.
	clusterScopedOwnerRef, err := getClusterScopedOwnerRef(hubclient, log)
	if err != nil {
		log.Error(err, "failed to get cluster scoped owner reference for hcp-cli-download. abort.")
		return err
	}

	enableCLIDownload := false
	cliDownloadList := &consolev1.ConsoleCLIDownloadList{}
	err = hubclient.List(context.TODO(), cliDownloadList)
	if err != nil {
		log.Error(err, "failed to get cliDownloadList. Skip installing the hcp CLI download.")
	} else {
		if len(cliDownloadList.Items) > 0 {
			log.Info("found at least one ConsoleCLIDownload resource. Enabling the hypershift ConsoleCLIDownload")
			enableCLIDownload = true
		}
	}

	if enableCLIDownload {
		// Construct and apply ConsoleCLIDownload
		cliDownload, err := getConsoleDownload(route.Spec.Host, log)
		if err != nil {
			log.Error(err, "failed to prepare hcp-cli-download ConsoleCLIDownload")
			return err
		}
		// set ownerRef for garbage collection after the hypershift feature is disabled
		cliDownload.SetOwnerReferences([]metav1.OwnerReference{*clusterScopedOwnerRef})
		_, err = controllerutil.CreateOrUpdate(context.TODO(), hubclient, cliDownload, func() error { return nil })
		if err != nil {
			log.Error(err, "failed to create or update hcp-cli-download ConsoleCLIDownload")
			return err
		}
		log.Info("hcp-cli-download ConsoleCLIDownload was applied successfully")
	}

	return nil
}

func removeHypershiftCLIDownload(hubclient client.Client, installNamespace string, log logr.Logger) {
	// Remove the old version of hypershift CLI resources

	// Remove the old ConsoleCLIDownload if exists
	cliDownload := &consolev1.ConsoleCLIDownload{}
	err := hubclient.Get(context.TODO(), types.NamespacedName{Name: OldCLIDownloadResourceName}, cliDownload)
	if err == nil {
		deleteErr := hubclient.Delete(context.TODO(), cliDownload)
		if deleteErr != nil {
			log.Error(err, "failed to delete hypershift-cli-download ConsoleCLIDownload")
		}
	} else {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "failed to find hypershift-cli-download ConsoleCLIDownload")
		}
	}

	// Remove the old route if exists
	cliRoute := &routev1.Route{}
	err = hubclient.Get(context.TODO(), types.NamespacedName{Namespace: installNamespace, Name: OldCLIDownloadResourceName}, cliRoute)
	if err == nil {
		deleteErr := hubclient.Delete(context.TODO(), cliRoute)
		if deleteErr != nil {
			log.Error(err, "failed to delete hypershift-cli-download Route")
		}
	} else {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "failed to find hypershift-cli-download Route")
		}
	}

	// Remove the old service if exists
	cliService := &corev1.Service{}
	err = hubclient.Get(context.TODO(), types.NamespacedName{Namespace: installNamespace, Name: OldCLIDownloadResourceName}, cliService)
	if err == nil {
		deleteErr := hubclient.Delete(context.TODO(), cliService)
		if deleteErr != nil {
			log.Error(err, "failed to delete hypershift-cli-download Service")
		}
	} else {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "failed to find hypershift-cli-download Service")
		}
	}

	// Remove the old deployment if exists
	cliDeployment := &appsv1.Deployment{}
	err = hubclient.Get(context.TODO(), types.NamespacedName{Namespace: installNamespace, Name: OldCLIDownloadResourceName}, cliDeployment)
	if err == nil {
		deleteErr := hubclient.Delete(context.TODO(), cliDeployment)
		if deleteErr != nil {
			log.Error(err, "failed to delete hypershift-cli-download Deployment")
		}
	} else {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "failed to find hypershift-cli-download Deployment")
		}
	}
}

func getCLIDeployment(cliImage string, envVars []corev1.EnvVar, log logr.Logger, installNamespace string, hubclient client.Client) (*appsv1.Deployment, error) {
	depFile, err := fs.ReadFile("manifests/cli/deployment.yaml")
	if err != nil {
		log.Error(err, "failed to read manifests/cli/deployment.yaml")
		return nil, err
	}

	dep := &appsv1.Deployment{}
	err = yaml.Unmarshal(depFile, &dep)
	if err != nil {
		log.Error(err, "failed to parse manifests/cli/deployment.yaml")
		return nil, err
	}

	dep.SetNamespace(installNamespace)

	// set the deployment with the hypershift_cli image from CSV
	dep.Spec.Template.Spec.Containers[0].Image = cliImage

	tolerations := getAddonManagerTolerations(hubclient, log)
	if tolerations != nil {
		log.Info("adding the following tolerations to the cli deployment")
		for _, tol := range tolerations {
			log.Info("key = " + tol.Key + ", operator = " + string(tol.Operator) + ", effect = " + string(tol.Effect))
		}
		dep.Spec.Template.Spec.Tolerations = tolerations
	}

	// set proxy environment variables if exist in the addon manager deployment
	containerEnvVars := []corev1.EnvVar{}
	for _, envVar := range envVars {
		if strings.HasSuffix(envVar.Name, "_PROXY") {
			containerEnvVars = append(containerEnvVars, corev1.EnvVar{Name: envVar.Name, Value: envVar.Value})
		}
	}

	if len(containerEnvVars) > 0 {
		dep.Spec.Template.Spec.Containers[0].Env = containerEnvVars
	}

	return dep, nil
}

func getAddonManagerTolerations(hubclient client.Client, log logr.Logger) []corev1.Toleration {
	selfPodNamespace := os.Getenv("POD_NAMESPACE")
	if len(selfPodNamespace) == 0 {
		return nil
	}

	selfPodName := os.Getenv("POD_NAME")
	if len(selfPodName) == 0 {
		return nil
	}

	// Get the hypershift addon manager pod's tolerations
	selfPod := &corev1.Pod{}
	err := hubclient.Get(context.TODO(), types.NamespacedName{Namespace: selfPodNamespace, Name: selfPodName}, selfPod)
	if err != nil {
		log.Error(err, "failed to find the hypershift addon manager pod") // is it possible?
		return nil
	}

	return selfPod.Spec.Tolerations
}

func getService(log logr.Logger, installNamespace string) (*corev1.Service, error) {
	serviceFile, err := fs.ReadFile("manifests/cli/service.yaml")
	if err != nil {
		log.Error(err, "failed to read manifests/cli/service.yaml")
		return nil, err
	}

	service := &corev1.Service{}
	err = yaml.Unmarshal(serviceFile, &service)
	if err != nil {
		log.Error(err, "failed to parse manifests/cli/service.yaml")
		return nil, err
	}

	service.SetNamespace(installNamespace)

	return service, nil
}

func getRoute(log logr.Logger, installNamespace string) (*routev1.Route, error) {
	routeFile, err := fs.ReadFile("manifests/cli/route.yaml")
	if err != nil {
		log.Error(err, "failed to read manifests/cli/route.yaml")
		return nil, err
	}

	route := &routev1.Route{}
	err = yaml.Unmarshal(routeFile, &route)
	if err != nil {
		log.Error(err, "failed to parse manifests/cli/route.yaml")
		return nil, err
	}

	route.SetNamespace(installNamespace)

	return route, nil
}

func getConsoleDownload(routeUrl string, log logr.Logger) (*consolev1.ConsoleCLIDownload, error) {
	log.Info("using route URL: " + routeUrl)
	cliDownloadFile, err := fs.ReadFile("manifests/cli/consoledownload.yaml")
	if err != nil {
		log.Error(err, "failed to read manifests/cli/consoledownload.yaml")
		return nil, err
	}

	cliDownload := &consolev1.ConsoleCLIDownload{}
	err = yaml.Unmarshal(cliDownloadFile, &cliDownload)
	if err != nil {
		log.Error(err, "failed to parse manifests/cli/consoledownload.yaml")
		return nil, err
	}

	links := []consolev1.CLIDownloadLink{}

	links = append(links,
		consolev1.CLIDownloadLink{
			Href: "https://" + routeUrl + "/linux/amd64/hcp.tar.gz",
			Text: "Download hcp CLI for Linux for x86_64"},
		consolev1.CLIDownloadLink{
			Href: "https://" + routeUrl + "/darwin/amd64/hcp.tar.gz",
			Text: "Download hcp CLI for Mac for x86_64",
		},
		consolev1.CLIDownloadLink{
			Href: "https://" + routeUrl + "/windows/amd64/hcp.tar.gz",
			Text: "Download hcp CLI for Windows for x86_64",
		},
		consolev1.CLIDownloadLink{
			Href: "https://" + routeUrl + "/linux/arm64/hcp.tar.gz",
			Text: "Download hcp CLI for Linux for ARM 64",
		},
		consolev1.CLIDownloadLink{
			Href: "https://" + routeUrl + "/darwin/arm64/hcp.tar.gz",
			Text: "Download hcp CLI for Mac for ARM 64",
		},
		consolev1.CLIDownloadLink{
			Href: "https://" + routeUrl + "/linux/ppc64/hcp.tar.gz",
			Text: "Download hcp CLI for Linux for IBM Power",
		},
		consolev1.CLIDownloadLink{
			Href: "https://" + routeUrl + "/linux/ppc64le/hcp.tar.gz",
			Text: "Download hcp CLI for Linux for IBM Power, little endian",
		},
		consolev1.CLIDownloadLink{
			Href: "https://" + routeUrl + "/linux/s390x/hcp.tar.gz",
			Text: "Download hcp CLI for Linux for IBM Z",
		})

	cliDownload.Spec.Links = links

	return cliDownload, nil
}

func getOwnerRef(hubclient client.Client, log logr.Logger) (*metav1.OwnerReference, []corev1.EnvVar, string, error) {

	//get mce target namespace for operand deployments
	deploymentNamespace := "multicluster-engine"

	mceList := &mcev1.MultiClusterEngineList{}
	err := hubclient.List(context.TODO(), mceList)
	if err != nil {
		log.Error(err, "failed to get multicluster engine list")
		return nil, nil, "", err
	}

	if len(mceList.Items) == 0 {
		err := errors.New("no MCE found")
		log.Error(err, "no MCE found")
		return nil, nil, "", err
	}

	//Only 1 multicluster engine, select first
	if mceList.Items[0].Spec.TargetNamespace != "" {
		deploymentNamespace = mceList.Items[0].Spec.TargetNamespace
	}

	deployment := &appsv1.Deployment{}
	err = hubclient.Get(context.TODO(), types.NamespacedName{Namespace: deploymentNamespace, Name: "hypershift-addon-manager"}, deployment)
	if err != nil {
		log.Error(err, "failed to get hypershift-addon-manager deployment")
		return nil, nil, "", err
	}

	ownerRef := metav1.NewControllerRef(deployment.GetObjectMeta(), schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"})
	return ownerRef, deployment.Spec.Template.Spec.Containers[0].Env, deploymentNamespace, nil
}

func getClusterScopedOwnerRef(hubclient client.Client, log logr.Logger) (*metav1.OwnerReference, error) {
	clusterRole := &rbacv1.ClusterRole{}
	err := hubclient.Get(context.TODO(), types.NamespacedName{Name: "open-cluster-management:hypershift-preview:hypershift-addon-manager"}, clusterRole)
	if err != nil {
		log.Error(err, "failed to get open-cluster-management:hypershift-preview:hypershift-addon-manager clusterrole")
		return nil, err
	}

	ownerRef := metav1.NewControllerRef(clusterRole.GetObjectMeta(), schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole"})
	return ownerRef, nil
}
