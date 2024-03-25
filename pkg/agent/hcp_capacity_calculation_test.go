package agent

import (
	"context"
	"testing"

	"github.com/go-logr/zapr"
	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stolostron/hypershift-addon-operator/pkg/metrics"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clustercsfake "open-cluster-management.io/api/client/cluster/clientset/versioned/fake"
)

func TestCalculateCapacitiesToHostHCPs(t *testing.T) {
	//ctx := context.Background()
	client := initClient()
	zapLog, _ := zap.NewDevelopment()

	fakeClusterCS := clustercsfake.NewSimpleClientset()

	aCtrl := &agentController{
		spokeClustersClient: fakeClusterCS,
		spokeUncachedClient: client,
		spokeClient:         client,
		hubClient:           client,
		log:                 zapr.NewLogger(zapLog),
	}

	aCtrl.SetHCPSizingBaseline(context.TODO())

	availableHCP := &hyperv1beta1.HostedControlPlane{
		TypeMeta: metav1.TypeMeta{
			Kind:       "HostedControlPlane",
			APIVersion: "hypershift.openshift.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp1",
			Namespace: "hcp1",
		},
		Spec: hyperv1beta1.HostedControlPlaneSpec{
			Platform: hyperv1beta1.PlatformSpec{
				Type: hyperv1beta1.AWSPlatform,
			},
			Networking: hyperv1beta1.ClusterNetworking{
				NetworkType: hyperv1beta1.OpenShiftSDN,
			},
			Services: []hyperv1beta1.ServicePublishingStrategyMapping{},
			Etcd: hyperv1beta1.EtcdSpec{
				ManagementType: hyperv1beta1.Managed,
			},
			InfraID: "hcp1-abcdef",
		},
		Status: hyperv1beta1.HostedControlPlaneStatus{
			Ready:   true,
			Version: "4.14.0",
		},
	}

	unavailableHCP := &hyperv1beta1.HostedControlPlane{
		TypeMeta: metav1.TypeMeta{
			Kind:       "HostedControlPlane",
			APIVersion: "hypershift.openshift.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp2",
			Namespace: "hcp2",
		},
		Spec: hyperv1beta1.HostedControlPlaneSpec{
			Platform: hyperv1beta1.PlatformSpec{
				Type: hyperv1beta1.AWSPlatform,
			},
			Networking: hyperv1beta1.ClusterNetworking{
				NetworkType: hyperv1beta1.OpenShiftSDN,
			},
			Services: []hyperv1beta1.ServicePublishingStrategyMapping{},
			Etcd: hyperv1beta1.EtcdSpec{
				ManagementType: hyperv1beta1.Managed,
			},
			InfraID: "hcp1-abcdef",
		},
		Status: hyperv1beta1.HostedControlPlaneStatus{
			Ready:   false,
			Version: "4.14.3",
		},
	}

	err := aCtrl.spokeUncachedClient.Create(ctx, availableHCP)
	assert.Nil(t, err, "err nil when hosted control plane hcp1 is created successfully")

	err = aCtrl.spokeUncachedClient.Create(ctx, unavailableHCP)
	assert.Nil(t, err, "err nil when hosted control plane hcp2 is created successfully")

	err = aCtrl.calculateCapacitiesToHostHCPs()
	assert.Nil(t, err, "err nil when calculateCapacitiesToHostHCPs was successful")

	// There is no worker node so all capacity values are expected to be 0.
	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.CapacityOfAverageQPSHCPs))
	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.CapacityOfHighQPSHCPs))
	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.CapacityOfHighQPSHCPs))
	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.CapacityOfLowQPSHCPs))
	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.CapacityOfMediumQPSHCPs))
	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.CapacityOfRequestBasedHCPs))
}
