package agent

import (
	"context"
	"os"
	"testing"

	"github.com/go-logr/zapr"
	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	testAnnotationEnvTeamTier = "env,team,tier"
	testHubMCName             = "mce-my-hc"
	testAutoCreatedForInfra   = "hypershift.openshift.io/auto-created-for-infra"
)

func strPtr(s string) *string { return &s }

func initLabelSyncClient(t *testing.T) (client.Client, client.Client) {
	scheme := runtime.NewScheme()
	err := clusterv1.AddToScheme(scheme)
	assert.Nil(t, err)
	err = hyperv1beta1.AddToScheme(scheme)
	assert.Nil(t, err)

	spokeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	hubClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	return spokeClient, hubClient
}

func TestLabelPropagation(t *testing.T) {
	tests := []struct {
		name                   string
		spokeHostedCluster     *clusterv1.ManagedCluster
		hubHostedCluster       *clusterv1.ManagedCluster
		importedMCEName        string
		localClusterName       string
		expectedLabels         map[string]string
		notExpectedLabels      []string
		discoveryPrefix        *string
		expectedAnnotationKeys []string
	}{
		{
			name: "new hub labels propagated to spoke",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
					Labels:      map[string]string{},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "imported-mce-hostedcluster",
					Annotations: map[string]string{},
					Labels:      map[string]string{"newLabel": "newValue"},
				},
			},
			importedMCEName: "imported-mce",
			expectedLabels: map[string]string{
				"newLabel": "newValue",
			},
		},
		{
			name: "dont override existing labels that isn't being tracked",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
					Labels:      map[string]string{"existingLabel": "oldValue"},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "imported-mce-hostedcluster",
					Annotations: map[string]string{},
					Labels:      map[string]string{"existingLabel": "newValue"},
				},
			},
			importedMCEName: "imported-mce",
			expectedLabels: map[string]string{
				"existingLabel": "oldValue",
			},
		},
		{
			name: "override existing labels that is being tracked",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift, propagatedLabelAnnotation: "existingLabel"},
					Labels:      map[string]string{"existingLabel": "oldValue"},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "imported-mce-hostedcluster",
					Annotations: map[string]string{},
					Labels:      map[string]string{"existingLabel": "newValue"},
				},
			},
			importedMCEName: "imported-mce",
			expectedLabels: map[string]string{
				"existingLabel": "newValue",
			},
		},
		{
			name: "system labels by exact key are not propagated",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "imported-mce-hostedcluster",
					Labels: map[string]string{"vendor": "OpenShift", "cloud": "Amazon", "clusterID": "abc-123", "env": "prod"},
				},
			},
			importedMCEName: "imported-mce",
			expectedLabels: map[string]string{
				"env": "prod",
			},
			notExpectedLabels: []string{"vendor", "cloud", "clusterID"},
		},
		{
			name: "system labels by prefix are not propagated",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "imported-mce-hostedcluster",
					Labels: map[string]string{
						"feature.open-cluster-management.io/addon-search":      "available",
						"hypershift.open-cluster-management.io/discovery-type": "MultiClusterEngineHCP",
						"team": "backend",
					},
				},
			},
			importedMCEName: "imported-mce",
			expectedLabels: map[string]string{
				"team": "backend",
			},
			notExpectedLabels: []string{
				"feature.open-cluster-management.io/addon-search",
				"hypershift.open-cluster-management.io/discovery-type",
			},
		},
		{
			name: "removed hub label is deleted from spoke",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift, propagatedLabelAnnotation: "env,team"},
					Labels:      map[string]string{"env": "prod", "team": "backend"},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "imported-mce-hostedcluster",
					Labels: map[string]string{"env": "prod"},
				},
			},
			importedMCEName: "imported-mce",
			expectedLabels: map[string]string{
				"env": "prod",
			},
			notExpectedLabels: []string{"team"},
		},
		{
			name: "spoke local labels are preserved alongside propagated labels",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
					Labels:      map[string]string{"local-label": "local-value"},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "imported-mce-hostedcluster",
					Labels: map[string]string{"env": "prod"},
				},
			},
			importedMCEName: "imported-mce",
			expectedLabels: map[string]string{
				"local-label": "local-value",
				"env":         "prod",
			},
		},
		{
			name: "custom discovery prefix - labels propagate",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "custom-hostedcluster",
					Labels: map[string]string{"env": "prod"},
				},
			},
			importedMCEName: "imported-mce",
			discoveryPrefix: strPtr("custom"),
			expectedLabels: map[string]string{
				"env": "prod",
			},
		},
		{
			name: "empty discovery prefix - hub name equals spoke name",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "hostedcluster",
					Labels: map[string]string{"env": "staging"},
				},
			},
			importedMCEName: "imported-mce",
			discoveryPrefix: strPtr(""),
			expectedLabels: map[string]string{
				"env": "staging",
			},
		},
		{
			name: "local-cluster agent skips reconcile",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
					Labels:      map[string]string{"existing": "value"},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "local-cluster-hostedcluster",
					Labels: map[string]string{"env": "prod"},
				},
			},
			importedMCEName:  "local-cluster",
			localClusterName: "local-cluster",
			expectedLabels: map[string]string{
				"existing": "value",
			},
			notExpectedLabels: []string{"env"},
		},
		{
			name: "missing created-via annotation skips reconcile",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "hostedcluster",
					Labels: map[string]string{"existing": "value"},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "imported-mce-hostedcluster",
					Labels: map[string]string{"env": "prod"},
				},
			},
			importedMCEName: "imported-mce",
			expectedLabels: map[string]string{
				"existing": "value",
			},
			notExpectedLabels: []string{"env"},
		},
		{
			name: "hub MC not found - propagated labels removed, local labels kept",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift, propagatedLabelAnnotation: "env,team"},
					Labels:      map[string]string{"existing": "value", "env": "prod", "team": "backend"},
				},
			},
			hubHostedCluster: nil,
			importedMCEName:  "imported-mce",
			expectedLabels: map[string]string{
				"existing": "value",
			},
			notExpectedLabels: []string{"env", "team"},
		},
		{
			name: "hub has nil labels - previously tracked labels removed",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift, propagatedLabelAnnotation: "env"},
					Labels:      map[string]string{"env": "prod", "local": "keep"},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "imported-mce-hostedcluster",
				},
			},
			importedMCEName: "imported-mce",
			expectedLabels: map[string]string{
				"local": "keep",
			},
			notExpectedLabels: []string{"env"},
		},
		{
			name: "spoke has nil labels - hub labels added",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "imported-mce-hostedcluster",
					Labels: map[string]string{"env": "prod"},
				},
			},
			importedMCEName: "imported-mce",
			expectedLabels: map[string]string{
				"env": "prod",
			},
			expectedAnnotationKeys: []string{"env"},
		},
		{
			name: "idempotent - no changes when already synced",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift, propagatedLabelAnnotation: "env"},
					Labels:      map[string]string{"env": "prod"},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "imported-mce-hostedcluster",
					Labels: map[string]string{"env": "prod"},
				},
			},
			importedMCEName: "imported-mce",
			expectedLabels: map[string]string{
				"env": "prod",
			},
		},
		{
			name: "multiple labels propagated and some removed",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift, propagatedLabelAnnotation: "env,team,old-label,deprecated"},
					Labels:      map[string]string{"env": "prod", "team": "backend", "old-label": "remove-me", "deprecated": "remove-me-too", "local": "keep"},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "imported-mce-hostedcluster",
					Labels: map[string]string{"env": "staging", "team": "backend"},
				},
			},
			importedMCEName: "imported-mce",
			expectedLabels: map[string]string{
				"env":   "staging",
				"team":  "backend",
				"local": "keep",
			},
			notExpectedLabels:      []string{"old-label", "deprecated"},
			expectedAnnotationKeys: []string{"env", "team"},
		},
		{
			name: "clusterset label propagated when only on hub",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "imported-mce-hostedcluster",
					Labels: map[string]string{"cluster.open-cluster-management.io/clusterset": "team-a-clusters"},
				},
			},
			importedMCEName: "imported-mce",
			expectedLabels: map[string]string{
				"cluster.open-cluster-management.io/clusterset": "team-a-clusters",
			},
			expectedAnnotationKeys: []string{"cluster.open-cluster-management.io/clusterset"},
		},
		{
			name: "matching labels on both sides are auto-tracked",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
					Labels:      map[string]string{"cluster.open-cluster-management.io/clusterset": "default"},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "imported-mce-hostedcluster",
					Labels: map[string]string{"cluster.open-cluster-management.io/clusterset": "default"},
				},
			},
			importedMCEName: "imported-mce",
			expectedLabels: map[string]string{
				"cluster.open-cluster-management.io/clusterset": "default",
			},
			expectedAnnotationKeys: []string{"cluster.open-cluster-management.io/clusterset"},
		},
		{
			name: "untracked label with different hub value is not overwritten",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
					Labels:      map[string]string{"cluster.open-cluster-management.io/clusterset": "default"},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "imported-mce-hostedcluster",
					Labels: map[string]string{"cluster.open-cluster-management.io/clusterset": "team-a"},
				},
			},
			importedMCEName: "imported-mce",
			expectedLabels: map[string]string{
				"cluster.open-cluster-management.io/clusterset": "default",
			},
		},
		{
			name: "tracked clusterset label updated when hub changes",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift, propagatedLabelAnnotation: "cluster.open-cluster-management.io/clusterset"},
					Labels:      map[string]string{"cluster.open-cluster-management.io/clusterset": "default"},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "imported-mce-hostedcluster",
					Labels: map[string]string{"cluster.open-cluster-management.io/clusterset": "team-a"},
				},
			},
			importedMCEName: "imported-mce",
			expectedLabels: map[string]string{
				"cluster.open-cluster-management.io/clusterset": "team-a",
			},
			expectedAnnotationKeys: []string{"cluster.open-cluster-management.io/clusterset"},
		},
		{
			name: "tracking annotation records propagated keys",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "imported-mce-hostedcluster",
					Labels: map[string]string{"env": "prod", "team": "backend", "vendor": "OpenShift"},
				},
			},
			importedMCEName: "imported-mce",
			expectedLabels: map[string]string{
				"env":  "prod",
				"team": "backend",
			},
			notExpectedLabels:      []string{"vendor"},
			expectedAnnotationKeys: []string{"env", "team"},
		},
		{
			name: "spoke label value changed - corrected back to hub value",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift, propagatedLabelAnnotation: "env"},
					Labels:      map[string]string{"env": "WRONG"},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "imported-mce-hostedcluster",
					Labels: map[string]string{"env": "prod"},
				},
			},
			importedMCEName: "imported-mce",
			expectedLabels: map[string]string{
				"env": "prod",
			},
			expectedAnnotationKeys: []string{"env"},
		},
		{
			name: "spoke label deleted entirely - re-added from hub",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift, propagatedLabelAnnotation: "env,team"},
					Labels:      map[string]string{"local": "keep"},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "imported-mce-hostedcluster",
					Labels: map[string]string{"env": "prod", "team": "platform"},
				},
			},
			importedMCEName: "imported-mce",
			expectedLabels: map[string]string{
				"env":   "prod",
				"team":  "platform",
				"local": "keep",
			},
			expectedAnnotationKeys: []string{"env", "team"},
		},
		{
			name: "spoke label deleted and hub value changed simultaneously",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift, propagatedLabelAnnotation: "env,team"},
					Labels:      map[string]string{"env": "old-value"},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "imported-mce-hostedcluster",
					Labels: map[string]string{"env": "new-value", "team": "backend"},
				},
			},
			importedMCEName: "imported-mce",
			expectedLabels: map[string]string{
				"env":  "new-value",
				"team": "backend",
			},
			expectedAnnotationKeys: []string{"env", "team"},
		},
		{
			name: "all propagated labels deleted from spoke - all re-added from hub",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{
						createdViaAnno:             createdViaHypershift,
						propagatedLabelAnnotation:  testAnnotationEnvTeamTier,
					},
					Labels:      map[string]string{},
				},
			},
			hubHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "imported-mce-hostedcluster",
					Labels: map[string]string{"env": "prod", "team": "platform", "tier": "frontend"},
				},
			},
			importedMCEName: "imported-mce",
			expectedLabels: map[string]string{
				"env":  "prod",
				"team": "platform",
				"tier": "frontend",
			},
			expectedAnnotationKeys: []string{"env", "team", "tier"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			spoke, hub := initLabelSyncClient(t)
			zapLog, _ := zap.NewDevelopment()

			if tt.discoveryPrefix != nil {
				os.Setenv("DISCOVERY_PREFIX", *tt.discoveryPrefix)
				defer os.Unsetenv("DISCOVERY_PREFIX")
			} else {
				os.Unsetenv("DISCOVERY_PREFIX")
			}

			localClusterName := "local-cluster"
			if tt.localClusterName != "" {
				localClusterName = tt.localClusterName
			}

			labelController := &LabelAgent{
				hubClient:        hub,
				spokeClient:      spoke,
				clusterName:      tt.importedMCEName,
				localClusterName: localClusterName,
				log:              zapr.NewLogger(zapLog),
			}

			err := spoke.Create(ctx, tt.spokeHostedCluster)
			assert.Nil(t, err)

			if tt.hubHostedCluster != nil {
				err = hub.Create(ctx, tt.hubHostedCluster)
				assert.Nil(t, err)
			}

			_, err = labelController.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{Name: tt.spokeHostedCluster.Name},
			})
			assert.Nil(t, err)

			retrievedMC := &clusterv1.ManagedCluster{}
			err = spoke.Get(ctx, types.NamespacedName{Name: tt.spokeHostedCluster.Name}, retrievedMC)
			assert.Nil(t, err)

			for key, expectedValue := range tt.expectedLabels {
				assert.Equal(t, expectedValue, retrievedMC.Labels[key], "label %s mismatch", key)
			}

			for _, key := range tt.notExpectedLabels {
				_, exists := retrievedMC.Labels[key]
				assert.False(t, exists, "label %s should not be on spoke", key)
			}

			if tt.expectedAnnotationKeys != nil {
				anno := retrievedMC.Annotations[propagatedLabelAnnotation]
				for _, key := range tt.expectedAnnotationKeys {
					assert.Contains(t, anno, key, "tracking annotation should contain %s", key)
				}
			}
		})
	}
}

type hcLabelTestCase struct {
	name                       string
	spokeMC                    *clusterv1.ManagedCluster
	hc                         *hyperv1beta1.HostedCluster
	hubMC                      *clusterv1.ManagedCluster
	expectedSpokeLabels        map[string]string
	notExpectedSpokeLabels     []string
	expectedHubLabels          map[string]string
	notExpectedHubLabels       []string
	expectedHCAnnoKeysOnSpoke  []string
	expectedHCAnnoKeysOnHub    []string
	expectedHubAnnoKeysOnSpoke []string
}

func hcLabelBasicCases() []hcLabelTestCase {
	return []hcLabelTestCase{
		{
			name: "HC label propagated to spoke and hub",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "my-hc",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
				},
			},
			hc: &hyperv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-hc", Namespace: "clusters",
					Labels: map[string]string{"env": "prod"},
				},
			},
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: testHubMCName},
			},
			expectedSpokeLabels:      map[string]string{"env": "prod"},
			expectedHubLabels:        map[string]string{"env": "prod"},
			expectedHCAnnoKeysOnSpoke: []string{"env"},
			expectedHCAnnoKeysOnHub:   []string{"env"},
		},
		{
			name: "HC skips existing untracked spoke label",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "my-hc",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
					Labels:      map[string]string{"env": "old-local"},
				},
			},
			hc: &hyperv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-hc", Namespace: "clusters",
					Labels: map[string]string{"env": "prod"},
				},
			},
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: testHubMCName},
			},
			expectedSpokeLabels: map[string]string{"env": "old-local"},
		},
		{
			name: "HC skips hub-tracked label on spoke when not HC-tracked",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-hc",
					Annotations: map[string]string{
						createdViaAnno:            createdViaHypershift,
						propagatedLabelAnnotation: "env",
					},
					Labels: map[string]string{"env": "staging"},
				},
			},
			hc: &hyperv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-hc", Namespace: "clusters",
					Labels: map[string]string{"env": "prod"},
				},
			},
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   testHubMCName,
					Labels: map[string]string{"env": "staging"},
				},
			},
			expectedSpokeLabels:       map[string]string{"env": "staging"},
			expectedHubAnnoKeysOnSpoke: []string{"env"},
		},
		{
			name: "HC system labels are not propagated",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "my-hc",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
				},
			},
			hc: &hyperv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-hc", Namespace: "clusters",
					Labels: map[string]string{
						"env":                  "prod",
						testAutoCreatedForInfra: "test-infra",
					},
				},
			},
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: testHubMCName},
			},
			expectedSpokeLabels:      map[string]string{"env": "prod"},
			notExpectedSpokeLabels:    []string{testAutoCreatedForInfra},
			expectedHCAnnoKeysOnSpoke: []string{"env"},
			notExpectedHubLabels:      []string{testAutoCreatedForInfra},
		},
	}
}

func hcLabelAdvancedCases() []hcLabelTestCase {
	return []hcLabelTestCase{
		{
			name: "HC label removed - cleaned from spoke and hub",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-hc",
					Annotations: map[string]string{
						createdViaAnno:              createdViaHypershift,
						hcPropagatedLabelAnnotation: "env",
					},
					Labels: map[string]string{"env": "prod", "local": "keep"},
				},
			},
			hc: &hyperv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-hc", Namespace: "clusters",
				},
			},
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        testHubMCName,
					Annotations: map[string]string{hcPropagatedLabelAnnotation: "env"},
					Labels:      map[string]string{"env": "prod"},
				},
			},
			expectedSpokeLabels:    map[string]string{"local": "keep"},
			notExpectedSpokeLabels: []string{"env"},
			notExpectedHubLabels:   []string{"env"},
		},
		{
			name: "HC and admin hub labels coexist independently",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "my-hc",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
				},
			},
			hc: &hyperv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-hc", Namespace: "clusters",
					Labels: map[string]string{"env": "prod"},
				},
			},
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   testHubMCName,
					Labels: map[string]string{"team": "platform"},
				},
			},
			expectedSpokeLabels:       map[string]string{"env": "prod", "team": "platform"},
			expectedHCAnnoKeysOnSpoke:  []string{"env"},
			expectedHubAnnoKeysOnSpoke: []string{"team"},
		},
		{
			name: "HC with managedcluster-name annotation maps correctly",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "custom-mc-name",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
				},
			},
			hc: &hyperv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-hc", Namespace: "clusters",
					Labels: map[string]string{"env": "prod"},
					Annotations: map[string]string{
						util.ManagedClusterAnnoKey: "custom-mc-name",
					},
				},
			},
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "mce-custom-mc-name"},
			},
			expectedSpokeLabels:      map[string]string{"env": "prod"},
			expectedHCAnnoKeysOnSpoke: []string{"env"},
		},
		{
			name: "HC matched by infraID when MC has auto-created-for-infra label",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-hc",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
					Labels: map[string]string{
						testAutoCreatedForInfra: "my-hc-abc12",
					},
				},
			},
			hc: &hyperv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-hc", Namespace: "clusters",
					Labels: map[string]string{"env": "prod"},
				},
				Spec: hyperv1beta1.HostedClusterSpec{
					InfraID: "my-hc-abc12",
				},
			},
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: testHubMCName},
			},
			expectedSpokeLabels:      map[string]string{"env": "prod"},
			expectedHCAnnoKeysOnSpoke: []string{"env"},
		},
	}
}

func hcLabelTestCases() []hcLabelTestCase {
	return append(hcLabelBasicCases(), hcLabelAdvancedCases()...)
}

func runHCLabelTest(t *testing.T, tt hcLabelTestCase) {
	t.Helper()
	ctx := context.Background()
	spoke, hub := initLabelSyncClient(t)
	zapLog, _ := zap.NewDevelopment()
	os.Unsetenv("DISCOVERY_PREFIX")

	lc := &LabelAgent{
		hubClient:        hub,
		spokeClient:      spoke,
		clusterName:      "mce",
		localClusterName: "local-cluster",
		log:              zapr.NewLogger(zapLog),
	}

	assert.Nil(t, spoke.Create(ctx, tt.spokeMC))
	if tt.hc != nil {
		assert.Nil(t, spoke.Create(ctx, tt.hc))
	}
	if tt.hubMC != nil {
		assert.Nil(t, hub.Create(ctx, tt.hubMC))
	}

	_, err := lc.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: tt.spokeMC.Name},
	})
	assert.Nil(t, err)

	retrievedSpoke := &clusterv1.ManagedCluster{}
	assert.Nil(t, spoke.Get(ctx, types.NamespacedName{Name: tt.spokeMC.Name}, retrievedSpoke))

	for key, val := range tt.expectedSpokeLabels {
		assert.Equal(t, val, retrievedSpoke.Labels[key], "spoke label %s mismatch", key)
	}
	for _, key := range tt.notExpectedSpokeLabels {
		_, exists := retrievedSpoke.Labels[key]
		assert.False(t, exists, "spoke label %s should not exist", key)
	}

	if tt.expectedHCAnnoKeysOnSpoke != nil {
		anno := retrievedSpoke.Annotations[hcPropagatedLabelAnnotation]
		for _, key := range tt.expectedHCAnnoKeysOnSpoke {
			assert.Contains(t, anno, key, "spoke hc-tracking annotation should contain %s", key)
		}
	}
	if tt.expectedHubAnnoKeysOnSpoke != nil {
		anno := retrievedSpoke.Annotations[propagatedLabelAnnotation]
		for _, key := range tt.expectedHubAnnoKeysOnSpoke {
			assert.Contains(t, anno, key, "spoke hub-tracking annotation should contain %s", key)
		}
	}

	if tt.hubMC != nil {
		retrievedHub := &clusterv1.ManagedCluster{}
		assert.Nil(t, hub.Get(ctx, types.NamespacedName{Name: tt.hubMC.Name}, retrievedHub))

		for key, val := range tt.expectedHubLabels {
			assert.Equal(t, val, retrievedHub.Labels[key], "hub label %s mismatch", key)
		}
		for _, key := range tt.notExpectedHubLabels {
			_, exists := retrievedHub.Labels[key]
			assert.False(t, exists, "hub label %s should not exist", key)
		}
		if tt.expectedHCAnnoKeysOnHub != nil {
			anno := retrievedHub.Annotations[hcPropagatedLabelAnnotation]
			for _, key := range tt.expectedHCAnnoKeysOnHub {
				assert.Contains(t, anno, key, "hub hc-tracking annotation should contain %s", key)
			}
		}
	}
}

func TestHCLabelPropagation(t *testing.T) {
	for _, tt := range hcLabelTestCases() {
		t.Run(tt.name, func(t *testing.T) {
			runHCLabelTest(t, tt)
		})
	}
}

func TestHCDeletedCleansUpLabels(t *testing.T) {
	ctx := context.Background()
	spoke, hub := initLabelSyncClient(t)
	zapLog, _ := zap.NewDevelopment()
	os.Unsetenv("DISCOVERY_PREFIX")

	lc := &LabelAgent{
		hubClient:        hub,
		spokeClient:      spoke,
		clusterName:      "mce",
		localClusterName: "local-cluster",
		log:              zapr.NewLogger(zapLog),
	}

	spokeMC := &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-hc",
			Annotations: map[string]string{
				createdViaAnno:              createdViaHypershift,
				hcPropagatedLabelAnnotation: "env,tier",
			},
			Labels: map[string]string{"env": "prod", "tier": "frontend", "local": "keep"},
		},
	}
	hubMC := &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        testHubMCName,
			Annotations: map[string]string{hcPropagatedLabelAnnotation: "env,tier"},
			Labels:      map[string]string{"env": "prod", "tier": "frontend", "admin-label": "keep"},
		},
	}

	assert.Nil(t, spoke.Create(ctx, spokeMC))
	assert.Nil(t, hub.Create(ctx, hubMC))

	_, err := lc.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "my-hc"},
	})
	assert.Nil(t, err)

	retrievedSpoke := &clusterv1.ManagedCluster{}
	assert.Nil(t, spoke.Get(ctx, types.NamespacedName{Name: "my-hc"}, retrievedSpoke))
	assert.Equal(t, "keep", retrievedSpoke.Labels["local"])
	_, envExists := retrievedSpoke.Labels["env"]
	assert.False(t, envExists, "env should be removed from spoke after HC deleted")
	_, tierExists := retrievedSpoke.Labels["tier"]
	assert.False(t, tierExists, "tier should be removed from spoke after HC deleted")

	retrievedHub := &clusterv1.ManagedCluster{}
	assert.Nil(t, hub.Get(ctx, types.NamespacedName{Name: testHubMCName}, retrievedHub))
	assert.Equal(t, "keep", retrievedHub.Labels["admin-label"])
	_, envExists = retrievedHub.Labels["env"]
	assert.False(t, envExists, "env should be removed from hub after HC deleted")
}

func TestHubSyncSkipsHCOwnedLabels(t *testing.T) {
	ctx := context.Background()
	spoke, hub := initLabelSyncClient(t)
	zapLog, _ := zap.NewDevelopment()
	os.Unsetenv("DISCOVERY_PREFIX")

	lc := &LabelAgent{
		hubClient:        hub,
		spokeClient:      spoke,
		clusterName:      "mce",
		localClusterName: "local-cluster",
		log:              zapr.NewLogger(zapLog),
	}

	spokeMC := &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "my-hc",
			Annotations: map[string]string{createdViaAnno: createdViaHypershift},
		},
	}
	hc := &hyperv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-hc", Namespace: "clusters",
			Labels: map[string]string{"env": "prod"},
		},
	}
	hubMC := &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:   testHubMCName,
			Labels: map[string]string{"env": "staging", "team": "platform"},
		},
	}

	assert.Nil(t, spoke.Create(ctx, spokeMC))
	assert.Nil(t, spoke.Create(ctx, hc))
	assert.Nil(t, hub.Create(ctx, hubMC))

	_, err := lc.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "my-hc"},
	})
	assert.Nil(t, err)

	retrieved := &clusterv1.ManagedCluster{}
	assert.Nil(t, spoke.Get(ctx, types.NamespacedName{Name: "my-hc"}, retrieved))

	// HC env=prod is new to spoke (spoke had no env) → HC adds it and tracks it
	assert.Equal(t, "prod", retrieved.Labels["env"], "HC env should be added since spoke had no env")
	assert.Equal(t, "platform", retrieved.Labels["team"], "admin hub label should be propagated")

	assert.Contains(t, retrieved.Annotations[hcPropagatedLabelAnnotation], "env")
	assert.Contains(t, retrieved.Annotations[propagatedLabelAnnotation], "team")
	assert.NotContains(t, retrieved.Annotations[propagatedLabelAnnotation], "env",
		"env should not be in hub tracking since HC owns it")
}

func TestMultiClusterLabelPropagation(t *testing.T) {
	ctx := context.Background()
	spoke, hub := initLabelSyncClient(t)
	zapLog, _ := zap.NewDevelopment()
	os.Unsetenv("DISCOVERY_PREFIX")

	labelController := &LabelAgent{
		hubClient:        hub,
		spokeClient:      spoke,
		clusterName:      "cluster-mce",
		localClusterName: "local-cluster",
		log:              zapr.NewLogger(zapLog),
	}

	type clusterPair struct {
		spoke          *clusterv1.ManagedCluster
		hub            *clusterv1.ManagedCluster
		expectedLabels map[string]string
		notExpected    []string
	}

	clusters := []clusterPair{
		{
			spoke: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hc-app1",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
					Labels:      map[string]string{"local-app1": "keep"},
				},
			},
			hub: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "cluster-mce-hc-app1",
					Labels: map[string]string{"env": "prod", "team": "platform", "vendor": "OpenShift"},
				},
			},
			expectedLabels: map[string]string{
				"env":        "prod",
				"team":       "platform",
				"local-app1": "keep",
			},
			notExpected: []string{"vendor"},
		},
		{
			spoke: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hc-app2",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
				},
			},
			hub: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster-mce-hc-app2",
					Labels: map[string]string{
						"env":  "staging",
						"tier": "backend",
						"cluster.open-cluster-management.io/clusterset": "team-b",
					},
				},
			},
			expectedLabels: map[string]string{
				"env":  "staging",
				"tier": "backend",
				"cluster.open-cluster-management.io/clusterset": "team-b",
			},
			notExpected: []string{"team"},
		},
		{
			spoke: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hc-app3",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
					Labels:      map[string]string{"cost-center": "local-value"},
				},
			},
			hub: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "cluster-mce-hc-app3",
					Labels: map[string]string{"region": "us-east-1", "cost-center": "hub-value", "compliance": "hipaa"},
				},
			},
			expectedLabels: map[string]string{
				"cost-center": "local-value",
				"compliance":  "hipaa",
			},
			notExpected: []string{"region"},
		},
	}

	for _, cp := range clusters {
		err := spoke.Create(ctx, cp.spoke)
		assert.Nil(t, err)
		err = hub.Create(ctx, cp.hub)
		assert.Nil(t, err)
	}

	for _, cp := range clusters {
		_, err := labelController.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: cp.spoke.Name},
		})
		assert.Nil(t, err)
	}

	for _, cp := range clusters {
		t.Run(cp.spoke.Name, func(t *testing.T) {
			retrieved := &clusterv1.ManagedCluster{}
			err := spoke.Get(ctx, types.NamespacedName{Name: cp.spoke.Name}, retrieved)
			assert.Nil(t, err)

			for key, val := range cp.expectedLabels {
				assert.Equal(t, val, retrieved.Labels[key], "%s: label %s mismatch", cp.spoke.Name, key)
			}

			for _, key := range cp.notExpected {
				_, exists := retrieved.Labels[key]
				assert.False(t, exists, "%s: label %s should not be present", cp.spoke.Name, key)
			}

			anno := retrieved.Annotations[propagatedLabelAnnotation]
			assert.NotEmpty(t, anno, "%s: tracking annotation should be set", cp.spoke.Name)
		})
	}
}

func TestReconcileDisableDiscovery(t *testing.T) {
	ctx := context.Background()
	spoke, hub := initLabelSyncClient(t)
	zapLog, _ := zap.NewDevelopment()

	os.Setenv("DISABLE_HC_DISCOVERY", "true")
	defer os.Unsetenv("DISABLE_HC_DISCOVERY")

	spokeMC := &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "hc1",
			Annotations: map[string]string{createdViaAnno: createdViaHypershift},
			Labels:      map[string]string{"existing": "value"},
		},
	}
	hubMC := &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "mce-hc1",
			Labels: map[string]string{"env": "prod"},
		},
	}

	assert.Nil(t, spoke.Create(ctx, spokeMC))
	assert.Nil(t, hub.Create(ctx, hubMC))

	lc := &LabelAgent{
		hubClient:        hub,
		spokeClient:      spoke,
		clusterName:      "mce",
		localClusterName: "local-cluster",
		log:              zapr.NewLogger(zapLog),
	}

	result, err := lc.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "hc1"},
	})
	assert.Nil(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	retrieved := &clusterv1.ManagedCluster{}
	assert.Nil(t, spoke.Get(ctx, types.NamespacedName{Name: "hc1"}, retrieved))
	_, exists := retrieved.Labels["env"]
	assert.False(t, exists, "labels should not propagate when discovery is disabled")
}

func TestReconcileSpokeNotFound(t *testing.T) {
	ctx := context.Background()
	spoke, hub := initLabelSyncClient(t)
	zapLog, _ := zap.NewDevelopment()
	os.Unsetenv("DISCOVERY_PREFIX")

	lc := &LabelAgent{
		hubClient:        hub,
		spokeClient:      spoke,
		clusterName:      "mce",
		localClusterName: "local-cluster",
		log:              zapr.NewLogger(zapLog),
	}

	result, err := lc.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "does-not-exist"},
	})
	assert.Nil(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestGetSpokeMCName(t *testing.T) {
	zapLog, _ := zap.NewDevelopment()

	tests := []struct {
		name            string
		hubMCName       string
		clusterName     string
		discoveryPrefix *string
		expected        string
	}{
		{
			name:        "default prefix - matching hub MC",
			hubMCName:   "cluster-mce-hc-app1",
			clusterName: "cluster-mce",
			expected:    "hc-app1",
		},
		{
			name:        "default prefix - non-matching hub MC returns empty",
			hubMCName:   "other-mce-hc-app1",
			clusterName: "cluster-mce",
			expected:    "",
		},
		{
			name:            "custom prefix - matching hub MC",
			hubMCName:       "custom-hc-web",
			clusterName:     "cluster-mce",
			discoveryPrefix: strPtr("custom"),
			expected:        "hc-web",
		},
		{
			name:            "custom prefix - non-matching hub MC returns empty",
			hubMCName:       "other-hc-web",
			clusterName:     "cluster-mce",
			discoveryPrefix: strPtr("custom"),
			expected:        "",
		},
		{
			name:            "empty prefix - hub name equals spoke name",
			hubMCName:       "hc-app1",
			clusterName:     "cluster-mce",
			discoveryPrefix: strPtr(""),
			expected:        "hc-app1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.discoveryPrefix != nil {
				os.Setenv("DISCOVERY_PREFIX", *tt.discoveryPrefix)
				defer os.Unsetenv("DISCOVERY_PREFIX")
			} else {
				os.Unsetenv("DISCOVERY_PREFIX")
			}

			lc := &LabelAgent{
				clusterName: tt.clusterName,
				log:         zapr.NewLogger(zapLog),
			}
			result := lc.getSpokeMCName(tt.hubMCName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMapHubMCToSpokeMC(t *testing.T) {
	zapLog, _ := zap.NewDevelopment()
	os.Unsetenv("DISCOVERY_PREFIX")

	lc := &LabelAgent{
		clusterName: "cluster-mce",
		log:         zapr.NewLogger(zapLog),
	}

	t.Run("matching hub MC returns reconcile request", func(t *testing.T) {
		hubMC := &clusterv1.ManagedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-mce-hc-app1"},
		}
		requests := lc.mapHubMCToSpokeMC(context.Background(), hubMC)
		assert.Equal(t, []reconcile.Request{
			{NamespacedName: types.NamespacedName{Name: "hc-app1"}},
		}, requests)
	})

	t.Run("non-matching hub MC returns nil", func(t *testing.T) {
		hubMC := &clusterv1.ManagedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "other-mce-hc-app1"},
		}
		requests := lc.mapHubMCToSpokeMC(context.Background(), hubMC)
		assert.Nil(t, requests)
	})
}

func TestMapHCToSpokeMC(t *testing.T) {
	zapLog, _ := zap.NewDevelopment()
	lc := &LabelAgent{log: zapr.NewLogger(zapLog)}

	t.Run("HC name maps to spoke MC name", func(t *testing.T) {
		hc := &hyperv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "clusters"},
		}
		requests := lc.mapHCToSpokeMC(context.Background(), hc)
		assert.Equal(t, []reconcile.Request{
			{NamespacedName: types.NamespacedName{Name: "my-hc"}},
		}, requests)
	})

	t.Run("HC with managedcluster-name annotation uses annotation value", func(t *testing.T) {
		hc := &hyperv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: "my-hc", Namespace: "clusters",
				Annotations: map[string]string{util.ManagedClusterAnnoKey: "custom-name"},
			},
		}
		requests := lc.mapHCToSpokeMC(context.Background(), hc)
		assert.Equal(t, []reconcile.Request{
			{NamespacedName: types.NamespacedName{Name: "custom-name"}},
		}, requests)
	})
}

func TestLabelEventFilters(t *testing.T) {
	pred := labelEventFilters()

	hostedMC := &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "hcp-cluster",
			Annotations: map[string]string{createdViaAnno: createdViaHypershift},
			Labels:      map[string]string{"env": "prod"},
		},
	}

	nonHostedMC := &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "regular-cluster",
			Labels: map[string]string{"env": "prod"},
		},
	}

	t.Run("CreateFunc accepts hosted cluster MC", func(t *testing.T) {
		result := pred.Create(event.CreateEvent{Object: hostedMC})
		assert.True(t, result)
	})

	t.Run("CreateFunc rejects non-hosted cluster MC", func(t *testing.T) {
		result := pred.Create(event.CreateEvent{Object: nonHostedMC})
		assert.False(t, result)
	})

	t.Run("UpdateFunc accepts hosted cluster MC with label change", func(t *testing.T) {
		oldMC := hostedMC.DeepCopy()
		oldMC.Labels = map[string]string{"env": "staging"}
		result := pred.Update(event.UpdateEvent{
			ObjectOld: oldMC,
			ObjectNew: hostedMC,
		})
		assert.True(t, result)
	})

	t.Run("UpdateFunc rejects hosted cluster MC with no label change", func(t *testing.T) {
		result := pred.Update(event.UpdateEvent{
			ObjectOld: hostedMC.DeepCopy(),
			ObjectNew: hostedMC,
		})
		assert.False(t, result)
	})

	t.Run("UpdateFunc rejects non-hosted cluster MC", func(t *testing.T) {
		oldMC := nonHostedMC.DeepCopy()
		oldMC.Labels = map[string]string{"env": "staging"}
		result := pred.Update(event.UpdateEvent{
			ObjectOld: oldMC,
			ObjectNew: nonHostedMC,
		})
		assert.False(t, result)
	})

	t.Run("UpdateFunc accepts when propagated-labels annotation is removed", func(t *testing.T) {
		oldMC := hostedMC.DeepCopy()
		oldMC.Annotations[propagatedLabelAnnotation] = "env,team"
		result := pred.Update(event.UpdateEvent{
			ObjectOld: oldMC,
			ObjectNew: hostedMC,
		})
		assert.True(t, result)
	})

	t.Run("UpdateFunc accepts when hc-propagated-labels annotation is removed", func(t *testing.T) {
		oldMC := hostedMC.DeepCopy()
		oldMC.Annotations[hcPropagatedLabelAnnotation] = "env"
		result := pred.Update(event.UpdateEvent{
			ObjectOld: oldMC,
			ObjectNew: hostedMC,
		})
		assert.True(t, result)
	})

	t.Run("UpdateFunc accepts when created-via annotation is added", func(t *testing.T) {
		oldMC := hostedMC.DeepCopy()
		delete(oldMC.Annotations, createdViaAnno)
		result := pred.Update(event.UpdateEvent{
			ObjectOld: oldMC,
			ObjectNew: hostedMC,
		})
		assert.True(t, result)
	})

	t.Run("CreateFunc rejects non-ManagedCluster object", func(t *testing.T) {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "some-pod"}}
		result := pred.Create(event.CreateEvent{Object: pod})
		assert.False(t, result)
	})

	t.Run("UpdateFunc rejects non-ManagedCluster object", func(t *testing.T) {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "some-pod"}}
		result := pred.Update(event.UpdateEvent{
			ObjectOld: pod,
			ObjectNew: pod,
		})
		assert.False(t, result)
	})

	t.Run("DeleteFunc always returns false", func(t *testing.T) {
		result := pred.Delete(event.DeleteEvent{Object: hostedMC})
		assert.False(t, result)
	})

	t.Run("GenericFunc always returns false", func(t *testing.T) {
		result := pred.Generic(event.GenericEvent{Object: hostedMC})
		assert.False(t, result)
	})
}

func TestHCLabelEventFilters(t *testing.T) {
	pred := hcLabelEventFilters()

	hc := &hyperv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-hc", Namespace: "clusters",
			Labels: map[string]string{"env": "prod"},
		},
	}

	t.Run("CreateFunc accepts HC", func(t *testing.T) {
		result := pred.Create(event.CreateEvent{Object: hc})
		assert.True(t, result)
	})

	t.Run("UpdateFunc accepts HC with label change", func(t *testing.T) {
		oldHC := hc.DeepCopy()
		oldHC.Labels = map[string]string{"env": "staging"}
		result := pred.Update(event.UpdateEvent{
			ObjectOld: oldHC,
			ObjectNew: hc,
		})
		assert.True(t, result)
	})

	t.Run("UpdateFunc rejects HC with no label change", func(t *testing.T) {
		result := pred.Update(event.UpdateEvent{
			ObjectOld: hc.DeepCopy(),
			ObjectNew: hc,
		})
		assert.False(t, result)
	})

	t.Run("DeleteFunc accepts HC deletion", func(t *testing.T) {
		result := pred.Delete(event.DeleteEvent{Object: hc})
		assert.True(t, result)
	})

	t.Run("GenericFunc returns false", func(t *testing.T) {
		result := pred.Generic(event.GenericEvent{Object: hc})
		assert.False(t, result)
	})
}

func TestParseAnnotation(t *testing.T) {
	t.Run("empty string returns empty map", func(t *testing.T) {
		result := parseAnnotation("")
		assert.Empty(t, result)
	})

	t.Run("single key", func(t *testing.T) {
		result := parseAnnotation("env")
		assert.Equal(t, map[string]bool{"env": true}, result)
	})

	t.Run("multiple keys", func(t *testing.T) {
		result := parseAnnotation(testAnnotationEnvTeamTier)
		assert.Equal(t, map[string]bool{"env": true, "team": true, "tier": true}, result)
	})
}

func TestJoinSortedKeys(t *testing.T) {
	t.Run("empty map returns empty string", func(t *testing.T) {
		result := joinSortedKeys(map[string]bool{})
		assert.Equal(t, "", result)
	})

	t.Run("keys are sorted alphabetically", func(t *testing.T) {
		result := joinSortedKeys(map[string]bool{"team": true, "env": true, "tier": true})
		assert.Equal(t, testAnnotationEnvTeamTier, result)
	})
}

func TestFindHostedCluster(t *testing.T) {
	t.Run("duplicate HC names skips propagation", func(t *testing.T) {
		spoke, _ := initLabelSyncClient(t)
		zapLog, _ := zap.NewDevelopment()
		ctx := context.Background()

		lc := &LabelAgent{
			spokeClient: spoke,
			log:         zapr.NewLogger(zapLog),
		}

		spokeMC := &clusterv1.ManagedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "my-hc",
				Annotations: map[string]string{createdViaAnno: createdViaHypershift},
			},
		}
		hc1 := &hyperv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "ns-a"},
		}
		hc2 := &hyperv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "ns-b"},
		}

		assert.Nil(t, spoke.Create(ctx, hc1))
		assert.Nil(t, spoke.Create(ctx, hc2))

		result, err := lc.findHostedCluster(ctx, spokeMC)
		assert.Nil(t, err)
		assert.Nil(t, result, "should return nil when multiple HCs match by name")
	})

	t.Run("infraID match takes priority over name", func(t *testing.T) {
		spoke, _ := initLabelSyncClient(t)
		zapLog, _ := zap.NewDevelopment()
		ctx := context.Background()

		lc := &LabelAgent{
			spokeClient: spoke,
			log:         zapr.NewLogger(zapLog),
		}

		spokeMC := &clusterv1.ManagedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "my-hc",
				Annotations: map[string]string{createdViaAnno: createdViaHypershift},
				Labels:      map[string]string{testAutoCreatedForInfra: "my-hc-xyz99"},
			},
		}
		hc := &hyperv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "clusters"},
			Spec:       hyperv1beta1.HostedClusterSpec{InfraID: "my-hc-xyz99"},
		}

		assert.Nil(t, spoke.Create(ctx, hc))

		result, err := lc.findHostedCluster(ctx, spokeMC)
		assert.Nil(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "my-hc", result.Name)
		assert.Equal(t, "clusters", result.Namespace)
	})

	t.Run("annotation match takes priority over infraID", func(t *testing.T) {
		spoke, _ := initLabelSyncClient(t)
		zapLog, _ := zap.NewDevelopment()
		ctx := context.Background()

		lc := &LabelAgent{
			spokeClient: spoke,
			log:         zapr.NewLogger(zapLog),
		}

		spokeMC := &clusterv1.ManagedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "custom-name",
				Annotations: map[string]string{createdViaAnno: createdViaHypershift},
				Labels:      map[string]string{testAutoCreatedForInfra: "my-hc-xyz99"},
			},
		}
		hcByAnno := &hyperv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: "my-hc", Namespace: "clusters",
				Annotations: map[string]string{
					util.ManagedClusterAnnoKey: "custom-name",
				},
			},
		}
		hcByInfra := &hyperv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "other-hc", Namespace: "other-ns"},
			Spec:       hyperv1beta1.HostedClusterSpec{InfraID: "my-hc-xyz99"},
		}

		assert.Nil(t, spoke.Create(ctx, hcByAnno))
		assert.Nil(t, spoke.Create(ctx, hcByInfra))

		result, err := lc.findHostedCluster(ctx, spokeMC)
		assert.Nil(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "my-hc", result.Name, "annotation match should win")
	})
}
