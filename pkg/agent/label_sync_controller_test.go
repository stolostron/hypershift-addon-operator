package agent

import (
	"context"
	"os"
	"testing"

	"github.com/go-logr/zapr"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func strPtr(s string) *string { return &s }

func initLabelSyncClient(t *testing.T) (client.Client, client.Client) {
	scheme := runtime.NewScheme()
	err := clusterv1.AddToScheme(scheme)
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
			name: "hub MC not found - spoke labels unchanged",
			spokeHostedCluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hostedcluster",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
					Labels:      map[string]string{"existing": "value"},
				},
			},
			hubHostedCluster: nil,
			importedMCEName:  "imported-mce",
			expectedLabels: map[string]string{
				"existing": "value",
			},
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
			expectedAnnotationKeys: []string{"cluster.open-cluster-management.io/clusterset"},
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
					Annotations: map[string]string{createdViaAnno: createdViaHypershift, propagatedLabelAnnotation: "env,team,tier"},
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

	t.Run("UpdateFunc accepts hosted cluster MC", func(t *testing.T) {
		result := pred.Update(event.UpdateEvent{
			ObjectOld: hostedMC.DeepCopy(),
			ObjectNew: hostedMC,
		})
		assert.True(t, result)
	})

	t.Run("UpdateFunc rejects non-hosted cluster MC", func(t *testing.T) {
		result := pred.Update(event.UpdateEvent{
			ObjectOld: nonHostedMC.DeepCopy(),
			ObjectNew: nonHostedMC,
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
