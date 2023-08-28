package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/zapr"
	hyperv1beta1 "github.com/openshift/hypershift/api/v1beta1"
	operatorv1 "github.com/operator-framework/api/pkg/operators/v1"
	agent "github.com/stolostron/klusterlet-addon-controller/pkg/apis"
	agentv1 "github.com/stolostron/klusterlet-addon-controller/pkg/apis/agent/v1"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	clusterv1alpha1 "open-cluster-management.io/api/cluster/v1alpha1"
	operatorapiv1 "open-cluster-management.io/api/operator/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// test cases:
//
//	no addondeploymentconfig
//	addondeploymentconfig with true/false
//	acm instaled, not installed
func TestNoACMAutoImport(t *testing.T) {
	ctx := context.Background()
	client, _ := initAIClient()
	zapLog, _ := zap.NewDevelopment()

	AICtrl := &AutoImportController{
		spokeClient: client,
		hubClient:   client,
		log:         zapr.NewLogger(zapLog),
	}

	apiService := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: "clusters-auto-import",
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

	hcNN := types.NamespacedName{Name: "auto-import", Namespace: "clusters"}

	err := AICtrl.hubClient.Create(ctx, apiService)
	assert.Nil(t, err, "err nil when kube-apiserver service is created successfully")
	defer AICtrl.hubClient.Delete(ctx, apiService)

	// create addondeployment config
	aodc := getAddonDeploymentConfig(false)
	fmt.Println(aodc)
	err = AICtrl.hubClient.Create(ctx, aodc)
	assert.Nil(t, err, "err nil when addondeploymentconfig is created successfully")

	// Create hosted cluster
	hc := getHostedCluster(hcNN)
	err = AICtrl.hubClient.Create(ctx, hc)
	assert.Nil(t, err, "err nil when hosted cluster is created successfully")

	AICtrl.Reconcile(ctx, ctrl.Request{NamespacedName: hcNN})

	//check managedcluster exists
	gotMC := &clusterv1.ManagedCluster{}
	err = AICtrl.hubClient.Get(ctx, types.NamespacedName{Name: hcNN.Name}, gotMC)
	assert.Nil(t, err, "err nil if managed cluster is found")

	// Check that the created-by annotation is set to hypershift
	annotations := gotMC.GetAnnotations()
	assert.Equal(t, createdViaHypershift, annotations[createdViaAnno])

	//check klusterletaddonconfing doesnt exist
	gotKAC := &agentv1.KlusterletAddonConfig{}
	err = AICtrl.hubClient.Get(ctx, types.NamespacedName{Name: hcNN.Name, Namespace: hcNN.Name}, gotKAC)
	assert.NotNil(t, err, "err not nil if klusterletaddonconfig is found")

}

func TestACMAutoImport(t *testing.T) {
	ctx := context.Background()
	client, _ := initAIClient()
	zapLog, _ := zap.NewDevelopment()

	AICtrl := &AutoImportController{
		spokeClient: client,
		hubClient:   client,
		log:         zapr.NewLogger(zapLog),
	}

	apiService := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: "clusters-auto-import",
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

	// create api service
	err := AICtrl.hubClient.Create(ctx, apiService)
	assert.Nil(t, err, "err nil when kube-apiserver service is created successfully")
	defer AICtrl.hubClient.Delete(ctx, apiService)

	// create addondeployment config
	aodc := getAddonDeploymentConfig(false)
	err = AICtrl.hubClient.Create(ctx, aodc)
	assert.Nil(t, err, "err nil when addondeploymentconfig is created successfully")

	// create acm operator
	acmOperator := operatorv1.Operator{
		ObjectMeta: metav1.ObjectMeta{Name: acmOperatorNamePrefix + "ocm"}}
	err = AICtrl.hubClient.Create(ctx, &acmOperator)
	assert.Nil(t, err, "err nil when acm operator is created successfully")

	// create hosted cluster
	hcNN := types.NamespacedName{Name: "auto-import", Namespace: "clusters"}
	hc := getHostedCluster(hcNN)
	err = AICtrl.hubClient.Create(ctx, hc)
	assert.Nil(t, err, "err nil when hosted cluster is created successfully")

	// create ns for klusterletaddonconfig
	ns := corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: hcNN.Name}}
	err = AICtrl.hubClient.Create(ctx, &ns)
	assert.Nil(t, err, "err nil if ns is created")

	AICtrl.Reconcile(ctx, ctrl.Request{NamespacedName: hcNN})

	//check managedcluster exists
	gotMC := &clusterv1.ManagedCluster{}
	err = AICtrl.hubClient.Get(ctx, types.NamespacedName{Name: hcNN.Name}, gotMC)
	assert.Nil(t, err, "err nil if managed cluster is found")

	// Check that the created-by annotation is set to hypershift
	annotations := gotMC.GetAnnotations()
	assert.Equal(t, createdViaHypershift, annotations[createdViaAnno])

	//check klusterletaddonconfing exists
	gotKAC := &agentv1.KlusterletAddonConfig{}
	err = AICtrl.hubClient.Get(ctx, types.NamespacedName{Name: hcNN.Name, Namespace: hcNN.Name}, gotKAC)
	assert.Nil(t, err, "err not nil if klusterletaddonconfig is found")

}

func TestToggleAutoImport(t *testing.T) {
	ctx := context.Background()
	client, _ := initAIClient()
	zapLog, _ := zap.NewDevelopment()

	AICtrl := &AutoImportController{
		spokeClient: client,
		hubClient:   client,
		log:         zapr.NewLogger(zapLog),
	}

	apiService := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: "clusters-auto-import",
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

	// create api service
	err := AICtrl.hubClient.Create(ctx, apiService)
	assert.Nil(t, err, "err nil when kube-apiserver service is created successfully")
	defer AICtrl.hubClient.Delete(ctx, apiService)

	// create addondeployment config
	aodc := getAddonDeploymentConfig(false)
	err = AICtrl.hubClient.Create(ctx, aodc)
	assert.Nil(t, err, "err nil when addondeploymentconfig is created successfully")

	// create acm operator
	acmOperator := operatorv1.Operator{
		ObjectMeta: metav1.ObjectMeta{Name: acmOperatorNamePrefix + ".ocm"}}
	err = AICtrl.hubClient.Create(ctx, &acmOperator)
	assert.Nil(t, err, "err nil when acm operator is created successfully")

	// create hosted cluster
	hcNN := types.NamespacedName{Name: "auto-import", Namespace: "clusters"}
	hc := getHostedCluster(hcNN)
	err = AICtrl.hubClient.Create(ctx, hc)
	assert.Nil(t, err, "err nil when hosted cluster is created successfully")

	ns := corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: hcNN.Name}}
	err = AICtrl.hubClient.Create(ctx, &ns)
	assert.Nil(t, err, "err nil if ns is created")

	AICtrl.Reconcile(ctx, ctrl.Request{NamespacedName: hcNN})

	//check managedcluster exists
	gotMC := &clusterv1.ManagedCluster{}
	err = AICtrl.hubClient.Get(ctx, types.NamespacedName{Name: hcNN.Name}, gotMC)
	assert.Nil(t, err, "err nil if managed cluster is found")

	//check klusterletaddonconfing exists
	gotKAC := &agentv1.KlusterletAddonConfig{}
	err = AICtrl.hubClient.Get(ctx, types.NamespacedName{Name: hcNN.Name, Namespace: hcNN.Name}, gotKAC)
	assert.Nil(t, err, "err not nil if klusterletaddonconfig is found")

	// disable auto import
	AICtrl.hubClient.Delete(ctx, aodc)
	assert.Nil(t, err, "err  nil if addondeploymentconfig is deleted")
	aodc = getAddonDeploymentConfig(true)
	err = AICtrl.hubClient.Create(ctx, aodc) // could just update but too lazy
	assert.Nil(t, err, "err nil when addondeploymentconfig is updated successfully")

	// create hosted cluster
	hcDisableNN := types.NamespacedName{Name: "auto-import-disable", Namespace: "clusters"}
	hcDisable := getHostedCluster(hcDisableNN)
	err = AICtrl.hubClient.Create(ctx, hcDisable)
	assert.Nil(t, err, "err nil when hosted cluster is created successfully")

	AICtrl.Reconcile(ctx, ctrl.Request{NamespacedName: hcDisableNN})

	//check managedcluster exists
	gotMC = &clusterv1.ManagedCluster{}
	err = AICtrl.hubClient.Get(ctx, types.NamespacedName{Name: hcDisableNN.Name}, gotMC)
	assert.NotNil(t, err, "err not nil if managed cluster is not found")

}

func TestHCPUnavailable(t *testing.T) {
	ctx := context.Background()
	client, _ := initAIClient()
	zapLog, _ := zap.NewDevelopment()

	AICtrl := &AutoImportController{
		spokeClient: client,
		hubClient:   client,
		log:         zapr.NewLogger(zapLog),
	}

	apiService := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: "clusters-auto-import",
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

	// create api service
	err := AICtrl.hubClient.Create(ctx, apiService)
	assert.Nil(t, err, "err nil when kube-apiserver service is created successfully")
	defer AICtrl.hubClient.Delete(ctx, apiService)

	// create hosted cluster
	hcNN := types.NamespacedName{Name: "auto-import", Namespace: "clusters"}
	hc := getHostedCluster(hcNN)
	hc.Status.Conditions = nil
	err = AICtrl.hubClient.Create(ctx, hc)
	assert.Nil(t, err, "err nil when hosted cluster is created successfully")

	res, err := AICtrl.Reconcile(ctx, ctrl.Request{NamespacedName: hcNN})
	assert.Nil(t, err, "no error when waiting for control plane")
	checkRes := ctrl.Result{Requeue: true, RequeueAfter: time.Duration(1) * time.Minute}
	assert.EqualValues(t, checkRes, res, "should requeue")

}

func initAIClient() (client.Client, *runtime.Scheme) {
	scheme := runtime.NewScheme()
	appsv1.AddToScheme(scheme)
	corev1.AddToScheme(scheme)
	metav1.AddMetaToScheme(scheme)
	clusterv1alpha1.AddToScheme(scheme)
	clusterv1.AddToScheme(scheme)
	operatorapiv1.AddToScheme(scheme)
	addonv1alpha1.AddToScheme(scheme)
	operatorv1.AddToScheme(scheme)
	hyperv1beta1.AddToScheme(scheme)
	agent.AddToScheme(scheme)

	ncb := fake.NewClientBuilder()
	ncb.WithScheme(scheme)
	return ncb.Build(), scheme

}

func getAddonDeploymentConfig(disable bool) *addonv1alpha1.AddOnDeploymentConfig {

	aodc := &addonv1alpha1.AddOnDeploymentConfig{}
	aodc.Name = addOnDeploymentConfigName
	aodc.Namespace = "multicluster-engine"

	if disable {
		aodc.Spec.CustomizedVariables = append(aodc.Spec.CustomizedVariables,
			addonv1alpha1.CustomizedVariable{Name: "autoImportDisabled", Value: "true"})
	}

	return aodc
}
