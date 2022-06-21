package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/openshift/library-go/pkg/operator/events/eventstesting"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hyperv1alpha1 "github.com/openshift/hypershift/api/v1alpha1"
	clusterclientset "open-cluster-management.io/api/client/cluster/clientset/versioned"
	clustercsfake "open-cluster-management.io/api/client/cluster/clientset/versioned/fake"
)

var (
	testscheme = clientgoscheme.Scheme
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(testscheme))
	utilruntime.Must(hyperv1alpha1.AddToScheme(testscheme))
}

func TestCreateManagementClusterClaim(t *testing.T) {
	cases := []struct {
		name         string
		validateFunc func(t *testing.T, clusterClient clusterclientset.Interface)
	}{
		{
			name: "create management cluster claim successfully",
			validateFunc: func(t *testing.T, clusterClient clusterclientset.Interface) {
				cc, err := clusterClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hypershiftManagementClusterClaimKey, metav1.GetOptions{})
				assert.Nil(t, err)
				assert.Equal(t, "true", cc.Spec.Value)
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeClusterCS := clustercsfake.NewSimpleClientset()
			ctrl := &agentController{
				spokeClustersClient: fakeClusterCS,
				recorder:            eventstesting.NewTestingEventRecorder(t),
			}

			err := ctrl.createManagementClusterClaim(context.TODO())
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			c.validateFunc(t, fakeClusterCS)
		})
	}
}

func TestCreateHostedClusterClaim(t *testing.T) {
	cases := []struct {
		name                   string
		startObjs              []ctrlclient.Object
		hostedclusterName      string
		hostedclusterNamespace string
		expectErr              string
		validateFunc           func(t *testing.T, runtimeClient ctrlclient.Client, clusterClient clusterclientset.Interface)
	}{
		{
			name:                   "create hosted cluster claim successfully",
			hostedclusterName:      "hc1",
			hostedclusterNamespace: "clusters",
			startObjs: []ctrlclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hc1-admin-kubeconfig",
						Namespace: "clusters",
					},
					Data: map[string][]byte{
						"kubeconfig": []byte("test"),
					},
				},
			},
			validateFunc: func(t *testing.T, runtimeClient ctrlclient.Client, clusterClient clusterclientset.Interface) {
				cc, err := clusterClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hypershiftHostedClusterClaimKey, metav1.GetOptions{})
				assert.Nil(t, err)
				assert.Equal(t, "true", cc.Spec.Value)
			},
		},
		{
			name:                   "create hosted cluster claim failed, secret not found",
			hostedclusterName:      "hc1",
			hostedclusterNamespace: "clusters",
			expectErr:              "not found",
			validateFunc: func(t *testing.T, runtimeClient ctrlclient.Client, clusterClient clusterclientset.Interface) {
				_, err := clusterClient.ClusterV1alpha1().ClusterClaims().Get(context.TODO(), hypershiftHostedClusterClaimKey, metav1.GetOptions{})
				assert.True(t, errors.IsNotFound(err))
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client := fake.NewClientBuilder().WithScheme(testscheme).WithObjects(c.startObjs...).Build()
			ctrl := &agentController{
				spokeClient: client,
				recorder:    eventstesting.NewTestingEventRecorder(t),
			}

			fakeClusterCS := clustercsfake.NewSimpleClientset()
			err := ctrl.createHostedClusterClaim(context.TODO(), types.NamespacedName{Namespace: c.hostedclusterNamespace, Name: fmt.Sprintf("%s-admin-kubeconfig", c.hostedclusterName)},
				func(secret *corev1.Secret) (clusterclientset.Interface, error) {
					return fakeClusterCS, nil
				})
			if len(c.expectErr) == 0 && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if len(c.expectErr) != 0 && (err == nil || !strings.Contains(err.Error(), c.expectErr)) {
				t.Errorf("expected error: %v, but got: %v", c.expectErr, err)
			}

			c.validateFunc(t, ctrl.spokeClient, fakeClusterCS)
		})
	}
}

func TestGenerateClusterClientFromSecret(t *testing.T) {
	ctx := context.Background()
	client := initClient()

	kcSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubeconfig",
			Namespace: "clusters",
		},
		Data: map[string][]byte{
			"kubeconfig": []byte(`fail`),
		},
	}
	client.Create(ctx, kcSecret)

	_, err := generateClusterClientFromSecret(kcSecret)
	assert.NotNil(t, err, "is not nil if it fails to get a cluster client")
}
