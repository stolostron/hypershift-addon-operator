package install

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/stolostron/hypershift-addon-operator/pkg/util"
)

type UpgradeController struct {
	hubClient                 client.Client
	spokeUncachedClient       client.Client
	log                       logr.Logger
	addonName                 string
	addonNamespace            string
	clusterName               string
	operatorImage             string
	pullSecret                string
	withOverride              bool
	hypershiftInstallExecutor HypershiftInstallExecutorInterface
}

func NewUpgradeController(hubClient, spokeClient client.Client, logger logr.Logger, addonName, addonNamespace, clusterName, operatorImage,
	pullSecretName string, withOverride bool) *UpgradeController {
	return &UpgradeController{
		hubClient:                 hubClient,
		spokeUncachedClient:       spokeClient,
		log:                       logger,
		addonName:                 addonName,
		addonNamespace:            addonNamespace,
		clusterName:               clusterName,
		operatorImage:             operatorImage,
		pullSecret:                pullSecretName,
		withOverride:              withOverride,
		hypershiftInstallExecutor: &HypershiftLibExecutor{},
	}
}

func (c *UpgradeController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c.log.Info(fmt.Sprintf("Reconciling hypershift update images configmap %s", req))
	defer c.log.Info(fmt.Sprintf("Done hypershift update images configmap %s", req))

	upgradeRequired, err := c.upgradeImageCheck()
	if err != nil {
		return ctrl.Result{}, err
	}

	if upgradeRequired {
		c.log.Info("image changes detected, upgrade the HyperShift operator")
		if err := c.RunHypershiftInstall(ctx); err != nil {
			c.log.Error(err, "failed to install hypershift Operator")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (c *UpgradeController) upgradeImageCheck() (bool, error) {
	hsOperatorKey := types.NamespacedName{
		Name:      util.HypershiftOperatorName,
		Namespace: util.HypershiftOperatorNamespace,
	}

	hsOperator := &appsv1.Deployment{}
	if err := c.spokeUncachedClient.Get(context.TODO(), hsOperatorKey, hsOperator); err != nil {
		return false, fmt.Errorf("failed to get the hypershift operator deployment, err: %w", err)
	}

	if len(hsOperator.Spec.Template.Spec.Containers) != 1 {
		c.log.Info("no containers found for HyperShift operator deployment, skip upgrade")
		return false, nil
	}

	if hsOperator.Annotations[util.HypershiftAddonAnnotationKey] != util.AddonControllerName {
		c.log.Info("HyperShift operator deployment not deployed by the HyperShift addon, skip upgrade")
		return false, nil
	}

	imageOverrideMap, err := c.getImageOverrideMap()
	if err != nil {
		if apierrors.IsNotFound(err) {
			c.log.Info("image override configmap is deleted, re-install HyperShift operator using imagestream images")
			return true, nil
		}

		return false, fmt.Errorf("failed to get the image override configmap, err: %w", err)
	}

	hsOperatorContainer := hsOperator.Spec.Template.Spec.Containers[0]
	for k, v := range imageOverrideMap {
		switch k {
		case util.ImageStreamAgentCapiProvider:
			if v != getContainerEnvVar(hsOperatorContainer.Env, util.HypershiftEnvVarImageAgentCapiProvider) {
				return true, nil
			}
		case util.ImageStreamAwsCapiProvider:
			if v != getContainerEnvVar(hsOperatorContainer.Env, util.HypershiftEnvVarImageAwsCapiProvider) {
				return true, nil
			}
		case util.ImageStreamAwsEncyptionProvider:
			if v != getContainerEnvVar(hsOperatorContainer.Env, util.HypershiftEnvVarImageAwsEncyptionProvider) {
				return true, nil
			}
		case util.ImageStreamAzureCapiProvider:
			if v != getContainerEnvVar(hsOperatorContainer.Env, util.HypershiftEnvVarImageAzureCapiProvider) {
				return true, nil
			}
		case util.ImageStreamClusterApi:
			if v != getContainerEnvVar(hsOperatorContainer.Env, util.HypershiftEnvVarImageClusterApi) {
				return true, nil
			}
		case util.ImageStreamKonnectivity:
			if v != getContainerEnvVar(hsOperatorContainer.Env, util.HypershiftEnvVarImageKonnectivity) {
				return true, nil
			}
		case util.ImageStreamKubevertCapiProvider:
			if v != getContainerEnvVar(hsOperatorContainer.Env, util.HypershiftEnvVarImageKubevertCapiProvider) {
				return true, nil
			}
		case util.ImageStreamHypershiftOperator:
			if v != hsOperatorContainer.Image {
				return true, nil
			}
		}
	}

	return false, nil
}

func (c *UpgradeController) SetupWithManager(mgr ctrl.Manager) error {
	filterByCM := func(obj client.Object) bool {
		if obj.GetName() == util.HypershiftOverrideImagesCM && obj.GetNamespace() == c.addonNamespace {
			return true
		}

		return false
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		WithEventFilter(predicate.NewPredicateFuncs(filterByCM)).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(c)
}

func (c *UpgradeController) getImageOverrideMap() (map[string]string, error) {
	overrideImagesCm := &corev1.ConfigMap{}
	overrideImagesCmKey := types.NamespacedName{Name: util.HypershiftOverrideImagesCM, Namespace: c.addonNamespace}
	if err := c.spokeUncachedClient.Get(context.TODO(), overrideImagesCmKey, overrideImagesCm); err != nil {
		return nil, err
	}

	return overrideImagesCm.Data, nil
}

func getContainerEnvVar(envVars []corev1.EnvVar, imageName string) string {
	for _, ev := range envVars {
		if ev.Name == imageName {
			return ev.Value
		}
	}
	return ""
}
