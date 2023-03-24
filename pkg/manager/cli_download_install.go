package manager

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	"github.com/go-logr/logr"
	consolev1 "github.com/openshift/api/console/v1"
	routev1 "github.com/openshift/api/route/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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
		// in upstream build, there is no hypershift CLI download image
		log.Info("the hypershift CLI download image was not found in the CSV. Skip enabling the hypershift CLI download")
		return nil
	}

	err = deployHypershiftCLIDownload(hubclient, cliDownloadImage, log)
	if err != nil {
		log.Error(err, "failed to deploy HypershiftCLIDownload")
		return err
	}

	return err
}

func GetMCECSV(hubclient client.Client, log logr.Logger) (*operatorsv1alpha1.ClusterServiceVersion, error) {
	csvlist := &operatorsv1alpha1.ClusterServiceVersionList{}

	listopts := &client.ListOptions{Namespace: "multicluster-engine"}

	err := hubclient.List(context.TODO(), csvlist, listopts)

	if err != nil {
		log.Error(err, "failed to list CSVs")
		return nil, err
	}

	var names []string
	for _, csv := range csvlist.Items {
		if strings.HasPrefix(csv.Name, "multicluster-engine.") {
			names = append(names, csv.Name)
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
	csvNN := types.NamespacedName{Namespace: "multicluster-engine", Name: names[0]}

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
			log.Info("the hypershift CLI download image was found in the CSV " + relatedImage.Image)
			return relatedImage.Image
		}
	}

	return ""
}

func deployHypershiftCLIDownload(hubclient client.Client, cliImage string, log logr.Logger) error {
	// Set owner reference to the addon manager deployment so that when the feature is disabled, HypershiftCLIDownload
	// is uninstalled
	ownerRef, envVars, err := getOwnerRef(hubclient, log)
	if err != nil {
		log.Error(err, "failed to get owner reference for hypershift-cli-download. abort.")
		return err
	}

	// Deployment
	deployment, err := getCLIDeployment(cliImage, envVars, log)
	if err != nil {
		log.Error(err, "failed to prepare hypershift-cli-download deployment")
		return err
	}
	// set ownerRef for garbage collection after the hypershift feature is disabled
	deployment.SetOwnerReferences([]metav1.OwnerReference{*ownerRef})
	_, err = controllerutil.CreateOrUpdate(context.TODO(), hubclient, deployment, func() error { return nil })
	if err != nil {
		log.Error(err, "failed to create or update hypershift-cli-download deployment")
		return err
	}
	log.Info("hypershift-cli-download deployment was applied successfully")

	// Service
	service, err := getService(log)
	if err != nil {
		log.Error(err, "failed to prepare hypershift-cli-download service")
		return err
	}
	// set ownerRef for garbage collection after the hypershift feature is disabled
	service.SetOwnerReferences([]metav1.OwnerReference{*ownerRef})
	_, err = controllerutil.CreateOrUpdate(context.TODO(), hubclient, service, func() error { return nil })
	if err != nil {
		log.Error(err, "failed to create or update hypershift-cli-download service")
		return err
	}
	log.Info("hypershift-cli-download service was applied successfully")

	// Route
	route, err := getRoute(log)
	if err != nil {
		log.Error(err, "failed to prepare hypershift-cli-download route")
		return err
	}
	// set ownerRef for garbage collection after the hypershift feature is disabled
	route.SetOwnerReferences([]metav1.OwnerReference{*ownerRef})
	_, err = controllerutil.CreateOrUpdate(context.TODO(), hubclient, route, func() error { return nil })
	if err != nil {
		log.Error(err, "failed to create or update hypershift-cli-download route")
		return err
	}
	log.Info("hypershift-cli-download route was applied successfully")

	// Get the route to get the URL
	err = hubclient.Get(context.TODO(), types.NamespacedName{Namespace: route.Namespace, Name: route.Name}, route)
	if err != nil {
		log.Error(err, "failed to get hypershift-cli-download route")
		return err
	}

	// ConsoleCLIDownload is a cluster scoped resource so we need to set the owner reference with a cluster scoped resource
	// The hypershift addon cluster role is a cluster scroped.
	clusterScopedOwnerRef, err := getClusterScopedOwnerRef(hubclient, log)
	if err != nil {
		log.Error(err, "failed to get cluster scoped owner reference for hypershift-cli-download. abort.")
		return err
	}

	// Construct and apply ConsoleCLIDownload
	cliDownload, err := getConsoleDownload(route.Spec.Host, log)
	if err != nil {
		log.Error(err, "failed to prepare hypershift-cli-download ConsoleCLIDownload")
		return err
	}
	// set ownerRef for garbage collection after the hypershift feature is disabled
	cliDownload.SetOwnerReferences([]metav1.OwnerReference{*clusterScopedOwnerRef})
	_, err = controllerutil.CreateOrUpdate(context.TODO(), hubclient, cliDownload, func() error { return nil })
	if err != nil {
		log.Error(err, "failed to create or update hypershift-cli-download ConsoleCLIDownload")
		return err
	}
	log.Info("hypershift-cli-download ConsoleCLIDownload was applied successfully")

	return nil
}

func getCLIDeployment(cliImage string, envVars []corev1.EnvVar, log logr.Logger) (*appsv1.Deployment, error) {
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

	// set the deployment with the hypershift_cli image from CSV
	dep.Spec.Template.Spec.Containers[0].Image = cliImage

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

func getService(log logr.Logger) (*corev1.Service, error) {
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

	return service, nil
}

func getRoute(log logr.Logger) (*routev1.Route, error) {
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

	links := []consolev1.CLIDownloadLink{
		{
			Href: "https://" + routeUrl + "/linux/amd64/hypershift.tar.gz",
			Text: "Download hypershift CLI for Linux for x86_64",
		},
		{
			Href: "https://" + routeUrl + "/darwin/amd64/hypershift.tar.gz",
			Text: "Download hypershift CLI for Mac for x86_64",
		},
		{
			Href: "https://" + routeUrl + "/windows/amd64/hypershift.tar.gz",
			Text: "Download hypershift CLI for Windows for x86_64",
		},
		{
			Href: "https://" + routeUrl + "/linux/arm64/hypershift.tar.gz",
			Text: "Download hypershift CLI for Linux for ARM 64",
		},
		{
			Href: "https://" + routeUrl + "/darwin/arm64/hypershift.tar.gz",
			Text: "Download hypershift CLI for Mac for ARM 64",
		},
	}
	cliDownload.Spec.Links = links

	return cliDownload, nil
}

func getOwnerRef(hubclient client.Client, log logr.Logger) (*metav1.OwnerReference, []corev1.EnvVar, error) {
	deployment := &appsv1.Deployment{}
	err := hubclient.Get(context.TODO(), types.NamespacedName{Namespace: "multicluster-engine", Name: "hypershift-addon-manager"}, deployment)
	if err != nil {
		log.Error(err, "failed to get hypershift-addon-manager deployment")
		return nil, nil, err
	}

	ownerRef := metav1.NewControllerRef(deployment.GetObjectMeta(), schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"})
	return ownerRef, deployment.Spec.Template.Spec.Containers[0].Env, nil
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
