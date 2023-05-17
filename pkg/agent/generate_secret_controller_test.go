package agent

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/zapr"
	hyperv1beta1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	operatorapiv1 "open-cluster-management.io/api/operator/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

//Things to test
//klusterlet with no managed cluster, expect info message
//klusterlet created, klusterlet should have finalizer and hostedcluster should be annotated
//klusterlet deleted, klusterlet should no longer have finalizer and hostedcluster annotation should be removed

func TestKlusterletReconcile(t *testing.T) {
	ctx := context.Background()
	client := initClient()
	zapLog, _ := zap.NewDevelopment()

	ESCtrl := &ExternalSecretController{
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
			Namespace: "clusters-hd-1",
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

	klusterletNamespaceNsn := types.NamespacedName{
		Name:      "klusterlet-hd-1",
		Namespace: "",
	}
	HCNamespaceNsn := types.NamespacedName{
		Name:      "hd-1",
		Namespace: "clusters",
	}

	kl := &operatorapiv1.Klusterlet{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "klusterlet-hd-1",
			Finalizers: []string{"operator.open-cluster-management.io/klusterlet-hosted-cleanup"},
		},
	}
	
	hcNN := types.NamespacedName{Name: "hd-1", Namespace: "clusters"}

	err := ESCtrl.hubClient.Create(ctx, apiService)
	assert.Nil(t, err, "err nil when kube-apiserver service is created successfully")
	defer ESCtrl.hubClient.Delete(ctx, apiService)

	// Create hosted cluster
	hc := getHostedCluster(hcNN)
	err = ESCtrl.hubClient.Create(ctx, hc)
	assert.Nil(t, err, "err nil when hosted cluster is created successfully")

	err = ESCtrl.hubClient.Create(ctx, kl)
	assert.Nil(t, err, "err nil when klusterlet is created successfully")

	//Should create annotation and finalizer
	_, err = ESCtrl.Reconcile(ctx, ctrl.Request{NamespacedName: klusterletNamespaceNsn})
	assert.Nil(t, err, "err nil when reconcile is successful")
	
	//Check finalizer
	gotKl := &operatorapiv1.Klusterlet{}
	err = client.Get(ctx, klusterletNamespaceNsn, gotKl)
	assert.Nil(t, err, "err nil if klusterlet is found")
	hasFinalizer := controllerutil.ContainsFinalizer(gotKl, "operator.open-cluster-management.io/hc-secret-annotation")
	assert.True(t, hasFinalizer, "true if finalizer exists")

	//Check annotation
	gotH := &hyperv1beta1.HostedCluster{}
	err = client.Get(ctx, HCNamespaceNsn, gotH)
	assert.Nil(t, err, "err nil if hostedcluster is found")
	_, ok := gotH.ObjectMeta.Annotations["create-external-hub-kubeconfig"]
	assert.True(t, ok, "true if annotation exists")


	//Add deletion timestamp to klusterlet
	gotKl.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	err = client.Update(ctx, gotKl)
	assert.Nil(t, err, "err nil if update is successful")

	//Should remove annotation and finalizer
	_, err = ESCtrl.Reconcile(ctx, ctrl.Request{NamespacedName: klusterletNamespaceNsn})
	assert.Nil(t, err, "err nil when reconcile is successful")

	//Check finalizer
	gotKl = &operatorapiv1.Klusterlet{}
	err = client.Get(ctx, klusterletNamespaceNsn, gotKl)
	assert.Nil(t, err, "err nil if klusterlet is found")
	hasFinalizer = controllerutil.ContainsFinalizer(gotKl, "operator.open-cluster-management.io/hc-secret-annotation")
	assert.False(t, hasFinalizer, "false if finalizer doesn't exist")

	//Check annotation
	gotH = &hyperv1beta1.HostedCluster{}
	err = client.Get(ctx, HCNamespaceNsn, gotH)
	assert.Nil(t, err, "err nil if hostedcluster is found")
	_, ok = gotH.ObjectMeta.Annotations["create-external-hub-kubeconfig"]
	assert.False(t, ok, "false if annotation doesn't exist")


}
