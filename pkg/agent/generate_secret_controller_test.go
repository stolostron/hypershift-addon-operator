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

	//Create klusterlet (import)
	err = ESCtrl.hubClient.Create(ctx, kl)
	assert.Nil(t, err, "err nil when klusterlet is created successfully")

	//Should create annotation
	_, err = ESCtrl.Reconcile(ctx, ctrl.Request{NamespacedName: klusterletNamespaceNsn})
	assert.Nil(t, err, "err nil when reconcile is successful")

	//Check annotation
	gotH := &hyperv1beta1.HostedCluster{}
	err = ESCtrl.hubClient.Get(ctx, HCNamespaceNsn, gotH)
	assert.Nil(t, err, "err nil if hostedcluster is found")
	firstTimestamp, ok := gotH.ObjectMeta.Annotations["create-external-hub-kubeconfig"]
	assert.True(t, ok, "true if annotation exists")

	//Delete klusterlet
	kl.ObjectMeta.Finalizers = []string{}
	err = ESCtrl.hubClient.Update(ctx, kl)
	assert.Nil(t, err, "err nil if successfully removed finalizers")
	err = ESCtrl.hubClient.Delete(ctx, kl)
	assert.Nil(t, err, "err nil if klusterlet is successfully deleted")

	//Nothing should happen to hosted cluster
	_, err = ESCtrl.Reconcile(ctx, ctrl.Request{NamespacedName: klusterletNamespaceNsn})
	assert.Nil(t, err, "err nil when reconcile is successful")

	//Annotation should still exist and be unchanged
	err = ESCtrl.hubClient.Get(ctx, HCNamespaceNsn, gotH)
	assert.Nil(t, err, "err nil if hostedcluster is found")
	secondTimestamp, found := gotH.ObjectMeta.Annotations["create-external-hub-kubeconfig"]
	assert.True(t, firstTimestamp == secondTimestamp && found, "true if annotation is unchanged")

	kl = &operatorapiv1.Klusterlet{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "klusterlet-hd-1",
			Finalizers: []string{"operator.open-cluster-management.io/klusterlet-hosted-cleanup"},
		},
	}

	time.Sleep(1 * time.Second) //Sleep one second to ensure different timestamp

	//Recreate klusterlet (reimport)
	err = ESCtrl.hubClient.Create(ctx, kl)
	assert.Nil(t, err, "err nil when klusterlet is created successfully")

	//Annotation should be updated to current time
	_, err = ESCtrl.Reconcile(ctx, ctrl.Request{NamespacedName: klusterletNamespaceNsn})
	assert.Nil(t, err, "err nil when reconcile is successful")

	//Annotation should have newer timestamp
	err = ESCtrl.hubClient.Get(ctx, HCNamespaceNsn, gotH)
	assert.Nil(t, err, "err nil if hostedcluster is found")
	secondTimestamp, found = gotH.ObjectMeta.Annotations["create-external-hub-kubeconfig"]
	assert.True(t, firstTimestamp != secondTimestamp && found, "true if annotation exists and is changed")

}
