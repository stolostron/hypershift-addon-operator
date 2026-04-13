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
	testHubMCName           = "mce-my-hc"
	testAutoCreatedForInfra = "hypershift.openshift.io/auto-created-for-infra"
	testCustomMCName        = "custom-name"
	testLocalClusterName    = "local-cluster"
	testHubMCNameDefault    = "mce-hc1"
	testLocalVal            = "local-val"
	testAnnotationEnvTeam   = "env,team"
	testClusterMCE          = "cluster-mce"
	testInfraID             = "my-hc-xyz99"
)

func strPtr(s string) *string { return &s }

func initLabelSyncClient(t *testing.T) (client.Client, client.Client) {
	scheme := runtime.NewScheme()
	assert.Nil(t, clusterv1.AddToScheme(scheme))
	assert.Nil(t, hyperv1beta1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).Build(),
		fake.NewClientBuilder().WithScheme(scheme).Build()
}

func newLabelAgent(t *testing.T, spoke, hub client.Client, clusterName string) *LabelAgent {
	zapLog, _ := zap.NewDevelopment()
	return &LabelAgent{
		hubClient:        hub,
		spokeClient:      spoke,
		clusterName:      clusterName,
		localClusterName: testLocalClusterName,
		log:              zapr.NewLogger(zapLog),
	}
}

func reconcileAndGet(
	t *testing.T, lc *LabelAgent, spoke client.Client, name string,
) *clusterv1.ManagedCluster {
	_, err := lc.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: name},
	})
	assert.Nil(t, err)
	mc := &clusterv1.ManagedCluster{}
	assert.Nil(t, spoke.Get(context.Background(), types.NamespacedName{Name: name}, mc))
	return mc
}

// --- Hub-to-spoke label propagation ---

type hubLabelTestCase struct {
	name             string
	spokeMC          *clusterv1.ManagedCluster
	hubMC            *clusterv1.ManagedCluster
	clusterName      string
	localClusterName string
	discoveryPrefix  *string
	expectedLabels   map[string]string
	notExpectedLabels []string
	expectedAnnoKeys []string
}

func hubLabelSyncCases() []hubLabelTestCase {
	return []hubLabelTestCase{
		{
			name: "hub labels propagated to spoke",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hc1",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
				},
			},
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   testHubMCNameDefault,
					Labels: map[string]string{"env": "prod", "team": "backend"},
				},
			},
			clusterName:      "mce",
			expectedLabels:   map[string]string{"env": "prod", "team": "backend"},
			expectedAnnoKeys: []string{"env", "team"},
		},
		{
			name: "untracked label not overwritten",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hc1",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
					Labels:      map[string]string{"env": testLocalVal},
				},
			},
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   testHubMCNameDefault,
					Labels: map[string]string{"env": "hub-val"},
				},
			},
			clusterName:    "mce",
			expectedLabels: map[string]string{"env": testLocalVal},
		},
		{
			name: "tracked label overridden by hub",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hc1",
					Annotations: map[string]string{
						createdViaAnno:            createdViaHypershift,
						propagatedLabelAnnotation: "env",
					},
					Labels: map[string]string{"env": "old"},
				},
			},
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   testHubMCNameDefault,
					Labels: map[string]string{"env": "new"},
				},
			},
			clusterName:    "mce",
			expectedLabels: map[string]string{"env": "new"},
		},
		{
			name: "system labels filtered by key and prefix",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hc1",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
				},
			},
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: testHubMCNameDefault,
					Labels: map[string]string{
						"vendor": "OpenShift", "cloud": "Amazon", "region": "us-east-1",
						"feature.open-cluster-management.io/addon-search": "available",
						"env": "prod",
					},
				},
			},
			clusterName:       "mce",
			expectedLabels:    map[string]string{"env": "prod"},
			notExpectedLabels: []string{"vendor", "cloud", "region", "feature.open-cluster-management.io/addon-search"},
		},
	}
}

func hubLabelCleanupCases() []hubLabelTestCase {
	return []hubLabelTestCase{
		{
			name: "hub label removed from spoke",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hc1",
					Annotations: map[string]string{
						createdViaAnno:            createdViaHypershift,
						propagatedLabelAnnotation: testAnnotationEnvTeam,
					},
					Labels: map[string]string{"env": "prod", "team": "backend", "local": "keep"},
				},
			},
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   testHubMCNameDefault,
					Labels: map[string]string{"env": "prod"},
				},
			},
			clusterName:       "mce",
			expectedLabels:    map[string]string{"env": "prod", "local": "keep"},
			notExpectedLabels: []string{"team"},
		},
		{
			name: "hub MC not found cleans up tracked labels",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hc1",
					Annotations: map[string]string{
						createdViaAnno:            createdViaHypershift,
						propagatedLabelAnnotation: testAnnotationEnvTeam,
					},
					Labels: map[string]string{"env": "prod", "team": "backend", "local": "keep"},
				},
			},
			hubMC:             nil,
			clusterName:       "mce",
			expectedLabels:    map[string]string{"local": "keep"},
			notExpectedLabels: []string{"env", "team"},
		},
		{
			name: "multiple labels with partial removal",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hc1",
					Annotations: map[string]string{
						createdViaAnno:            createdViaHypershift,
						propagatedLabelAnnotation: "env,team,old,deprecated",
					},
					Labels: map[string]string{
						"env": "prod", "team": "backend",
						"old": "rm", "deprecated": "rm", "local": "keep",
					},
				},
			},
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   testHubMCNameDefault,
					Labels: map[string]string{"env": "staging", "team": "backend"},
				},
			},
			clusterName:       "mce",
			expectedLabels:    map[string]string{"env": "staging", "team": "backend", "local": "keep"},
			notExpectedLabels: []string{"old", "deprecated"},
			expectedAnnoKeys:  []string{"env", "team"},
		},
	}
}

func hubLabelEdgeCases() []hubLabelTestCase {
	return []hubLabelTestCase{
		{
			name: "custom discovery prefix",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hc1",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
				},
			},
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "custom-hc1",
					Labels: map[string]string{"env": "prod"},
				},
			},
			clusterName:     "mce",
			discoveryPrefix: strPtr("custom"),
			expectedLabels:  map[string]string{"env": "prod"},
		},
		{
			name: "local-cluster skips reconcile",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hc1",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
					Labels:      map[string]string{"existing": "value"},
				},
			},
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "local-cluster-hc1",
					Labels: map[string]string{"env": "prod"},
				},
			},
			clusterName:       testLocalClusterName,
			localClusterName:  testLocalClusterName,
			expectedLabels:    map[string]string{"existing": "value"},
			notExpectedLabels: []string{"env"},
		},
		{
			name: "missing annotation skips reconcile",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "hc1",
					Labels: map[string]string{"existing": "value"},
				},
			},
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   testHubMCNameDefault,
					Labels: map[string]string{"env": "prod"},
				},
			},
			clusterName:       "mce",
			expectedLabels:    map[string]string{"existing": "value"},
			notExpectedLabels: []string{"env"},
		},
		{
			name: "idempotent when already synced",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hc1",
					Annotations: map[string]string{
						createdViaAnno:            createdViaHypershift,
						propagatedLabelAnnotation: "env",
					},
					Labels: map[string]string{"env": "prod"},
				},
			},
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   testHubMCNameDefault,
					Labels: map[string]string{"env": "prod"},
				},
			},
			clusterName:    "mce",
			expectedLabels: map[string]string{"env": "prod"},
		},
		{
			name: "matching labels auto-tracked",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hc1",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
					Labels:      map[string]string{"env": "prod"},
				},
			},
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   testHubMCNameDefault,
					Labels: map[string]string{"env": "prod"},
				},
			},
			clusterName:      "mce",
			expectedLabels:   map[string]string{"env": "prod"},
			expectedAnnoKeys: []string{"env"},
		},
	}
}

func TestLabelPropagation(t *testing.T) {
	var tests []hubLabelTestCase
	tests = append(tests, hubLabelSyncCases()...)
	tests = append(tests, hubLabelCleanupCases()...)
	tests = append(tests, hubLabelEdgeCases()...)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			spoke, hub := initLabelSyncClient(t)

			if tt.discoveryPrefix != nil {
				os.Setenv("DISCOVERY_PREFIX", *tt.discoveryPrefix)
				defer os.Unsetenv("DISCOVERY_PREFIX")
			} else {
				os.Unsetenv("DISCOVERY_PREFIX")
			}

			localCluster := testLocalClusterName
			if tt.localClusterName != "" {
				localCluster = tt.localClusterName
			}

			lc := newLabelAgent(t, spoke, hub, tt.clusterName)
			lc.localClusterName = localCluster

			assert.Nil(t, spoke.Create(ctx, tt.spokeMC))
			if tt.hubMC != nil {
				assert.Nil(t, hub.Create(ctx, tt.hubMC))
			}

			mc := reconcileAndGet(t, lc, spoke, tt.spokeMC.Name)

			for key, val := range tt.expectedLabels {
				assert.Equal(t, val, mc.Labels[key], "label %s", key)
			}
			for _, key := range tt.notExpectedLabels {
				_, exists := mc.Labels[key]
				assert.False(t, exists, "label %s should not exist", key)
			}
			if tt.expectedAnnoKeys != nil {
				tracked := parseAnnotation(mc.Annotations[propagatedLabelAnnotation])
				assert.Len(t, tracked, len(tt.expectedAnnoKeys))
				for _, key := range tt.expectedAnnoKeys {
					assert.True(t, tracked[key], "tracked key %s missing", key)
				}
			}
		})
	}
}

// --- HostedCluster-to-ManagedCluster label propagation ---

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

func hcLabelSyncCases() []hcLabelTestCase {
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
					Labels:      map[string]string{"env": testLocalVal},
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
			expectedSpokeLabels: map[string]string{"env": testLocalVal},
		},
		{
			name: "HC skips hub-tracked label when not HC-tracked",
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
	}
}

func hcLabelFilterAndMatchCases() []hcLabelTestCase {
	return []hcLabelTestCase{
		{
			name: "HC takes ownership from hub tracking",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-hc",
					Annotations: map[string]string{
						createdViaAnno:            createdViaHypershift,
						propagatedLabelAnnotation: "env",
					},
					Labels: map[string]string{"env": "prod"},
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
					Labels: map[string]string{"env": "prod"},
				},
			},
			expectedSpokeLabels:       map[string]string{"env": "prod"},
			expectedHCAnnoKeysOnSpoke:  []string{"env"},
			expectedHubAnnoKeysOnSpoke: []string{},
		},
		{
			name: "HC system labels not propagated",
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
		{
			name: "HC annotation match maps to correct MC",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "custom-mc-name",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
				},
			},
			hc: &hyperv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-hc", Namespace: "clusters",
					Labels:      map[string]string{"env": "prod"},
					Annotations: map[string]string{util.ManagedClusterAnnoKey: "custom-mc-name"},
				},
			},
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "mce-custom-mc-name"},
			},
			expectedSpokeLabels:      map[string]string{"env": "prod"},
			expectedHCAnnoKeysOnSpoke: []string{"env"},
		},
	}
}

func hcLabelCleanupCases() []hcLabelTestCase {
	return []hcLabelTestCase{
		{
			name: "HC label removed cleans up spoke and hub",
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
				ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "clusters"},
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
			name: "HC and hub labels coexist independently",
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
			name: "HC matched by infraID",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "my-hc",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
					Labels:      map[string]string{testAutoCreatedForInfra: "my-hc-abc12"},
				},
			},
			hc: &hyperv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-hc", Namespace: "clusters",
					Labels: map[string]string{"env": "prod"},
				},
				Spec: hyperv1beta1.HostedClusterSpec{InfraID: "my-hc-abc12"},
			},
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: testHubMCName},
			},
			expectedSpokeLabels:      map[string]string{"env": "prod"},
			expectedHCAnnoKeysOnSpoke: []string{"env"},
		},
	}
}

func hcLabelInteractionCases() []hcLabelTestCase {
	return []hcLabelTestCase{
		{
			name: "HC deleted cleans up tracked labels",
			spokeMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-hc",
					Annotations: map[string]string{
						createdViaAnno:              createdViaHypershift,
						hcPropagatedLabelAnnotation: "env,tier",
					},
					Labels: map[string]string{"env": "prod", "tier": "frontend", "local": "keep"},
				},
			},
			hc: nil,
			hubMC: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        testHubMCName,
					Annotations: map[string]string{hcPropagatedLabelAnnotation: "env,tier"},
					Labels:      map[string]string{"env": "prod", "tier": "frontend", "admin": "keep"},
				},
			},
			expectedSpokeLabels:    map[string]string{"local": "keep"},
			notExpectedSpokeLabels: []string{"env", "tier"},
			expectedHubLabels:      map[string]string{"admin": "keep"},
			notExpectedHubLabels:   []string{"env", "tier"},
		},
		{
			name: "hub sync skips HC-owned labels",
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
					Labels: map[string]string{"env": "staging", "team": "platform"},
				},
			},
			expectedSpokeLabels:       map[string]string{"env": "prod", "team": "platform"},
			expectedHCAnnoKeysOnSpoke:  []string{"env"},
			expectedHubAnnoKeysOnSpoke: []string{"team"},
		},
	}
}

func hcLabelTestCases() []hcLabelTestCase {
	cases := hcLabelSyncCases()
	cases = append(cases, hcLabelFilterAndMatchCases()...)
	cases = append(cases, hcLabelCleanupCases()...)
	return append(cases, hcLabelInteractionCases()...)
}

func runHCLabelTest(t *testing.T, tt hcLabelTestCase) {
	t.Helper()
	ctx := context.Background()
	spoke, hub := initLabelSyncClient(t)
	os.Unsetenv("DISCOVERY_PREFIX")
	lc := newLabelAgent(t, spoke, hub, "mce")

	assert.Nil(t, spoke.Create(ctx, tt.spokeMC))
	if tt.hc != nil {
		assert.Nil(t, spoke.Create(ctx, tt.hc))
	}
	if tt.hubMC != nil {
		assert.Nil(t, hub.Create(ctx, tt.hubMC))
	}

	mc := reconcileAndGet(t, lc, spoke, tt.spokeMC.Name)

	for key, val := range tt.expectedSpokeLabels {
		assert.Equal(t, val, mc.Labels[key], "spoke label %s", key)
	}
	for _, key := range tt.notExpectedSpokeLabels {
		_, exists := mc.Labels[key]
		assert.False(t, exists, "spoke label %s should not exist", key)
	}
	if tt.expectedHCAnnoKeysOnSpoke != nil {
		tracked := parseAnnotation(mc.Annotations[hcPropagatedLabelAnnotation])
		assert.Len(t, tracked, len(tt.expectedHCAnnoKeysOnSpoke))
		for _, key := range tt.expectedHCAnnoKeysOnSpoke {
			assert.True(t, tracked[key], "spoke hc-tracked key %s missing", key)
		}
	}
	if tt.expectedHubAnnoKeysOnSpoke != nil {
		tracked := parseAnnotation(mc.Annotations[propagatedLabelAnnotation])
		assert.Len(t, tracked, len(tt.expectedHubAnnoKeysOnSpoke))
		for _, key := range tt.expectedHubAnnoKeysOnSpoke {
			assert.True(t, tracked[key], "spoke hub-tracked key %s missing", key)
		}
	}

	if tt.hubMC != nil {
		hubMC := &clusterv1.ManagedCluster{}
		assert.Nil(t, hub.Get(ctx, types.NamespacedName{Name: tt.hubMC.Name}, hubMC))
		for key, val := range tt.expectedHubLabels {
			assert.Equal(t, val, hubMC.Labels[key], "hub label %s", key)
		}
		for _, key := range tt.notExpectedHubLabels {
			_, exists := hubMC.Labels[key]
			assert.False(t, exists, "hub label %s should not exist", key)
		}
		if tt.expectedHCAnnoKeysOnHub != nil {
			tracked := parseAnnotation(hubMC.Annotations[hcPropagatedLabelAnnotation])
			assert.Len(t, tracked, len(tt.expectedHCAnnoKeysOnHub))
			for _, key := range tt.expectedHCAnnoKeysOnHub {
				assert.True(t, tracked[key], "hub hc-tracked key %s missing", key)
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

// --- Multi-cluster propagation ---

func TestMultiClusterLabelPropagation(t *testing.T) {
	ctx := context.Background()
	spoke, hub := initLabelSyncClient(t)
	os.Unsetenv("DISCOVERY_PREFIX")
	lc := newLabelAgent(t, spoke, hub, testClusterMCE)

	type pair struct {
		spoke          *clusterv1.ManagedCluster
		hub            *clusterv1.ManagedCluster
		expectedLabels map[string]string
		notExpected    []string
	}

	clusters := []pair{
		{
			spoke: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hc-app1",
					Annotations: map[string]string{createdViaAnno: createdViaHypershift},
					Labels:      map[string]string{"local": "keep"},
				},
			},
			hub: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "cluster-mce-hc-app1",
					Labels: map[string]string{"env": "prod", "vendor": "OpenShift"},
				},
			},
			expectedLabels: map[string]string{"env": "prod", "local": "keep"},
			notExpected:    []string{"vendor"},
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
					Name:   "cluster-mce-hc-app2",
					Labels: map[string]string{"env": "staging", "tier": "backend"},
				},
			},
			expectedLabels: map[string]string{"env": "staging", "tier": "backend"},
		},
	}

	for _, cp := range clusters {
		assert.Nil(t, spoke.Create(ctx, cp.spoke))
		assert.Nil(t, hub.Create(ctx, cp.hub))
	}

	for _, cp := range clusters {
		t.Run(cp.spoke.Name, func(t *testing.T) {
			mc := reconcileAndGet(t, lc, spoke, cp.spoke.Name)
			for key, val := range cp.expectedLabels {
				assert.Equal(t, val, mc.Labels[key], "%s: label %s", cp.spoke.Name, key)
			}
			for _, key := range cp.notExpected {
				_, exists := mc.Labels[key]
				assert.False(t, exists, "%s: label %s should not exist", cp.spoke.Name, key)
			}
		})
	}
}

// --- Edge cases ---

func TestReconcileDisableDiscovery(t *testing.T) {
	spoke, hub := initLabelSyncClient(t)
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
			Name:   testHubMCNameDefault,
			Labels: map[string]string{"env": "prod"},
		},
	}

	ctx := context.Background()
	assert.Nil(t, spoke.Create(ctx, spokeMC))
	assert.Nil(t, hub.Create(ctx, hubMC))

	lc := newLabelAgent(t, spoke, hub, "mce")
	result, err := lc.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "hc1"},
	})
	assert.Nil(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	mc := &clusterv1.ManagedCluster{}
	assert.Nil(t, spoke.Get(ctx, types.NamespacedName{Name: "hc1"}, mc))
	_, exists := mc.Labels["env"]
	assert.False(t, exists, "labels should not propagate when discovery is disabled")
}

func TestReconcileSpokeNotFound(t *testing.T) {
	spoke, hub := initLabelSyncClient(t)
	os.Unsetenv("DISCOVERY_PREFIX")
	lc := newLabelAgent(t, spoke, hub, "mce")

	result, err := lc.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "does-not-exist"},
	})
	assert.Nil(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

// --- Unit tests for helpers ---

func TestGetSpokeMCName(t *testing.T) {
	tests := []struct {
		name            string
		hubMCName       string
		clusterName     string
		discoveryPrefix *string
		expected        string
	}{
		{"default prefix match", "cluster-mce-hc1", testClusterMCE, nil, "hc1"},
		{"default prefix no match", "other-mce-hc1", testClusterMCE, nil, ""},
		{"custom prefix match", "custom-hc-web", testClusterMCE, strPtr("custom"), "hc-web"},
		{"custom prefix no match", "other-hc-web", testClusterMCE, strPtr("custom"), ""},
		{"empty prefix returns name", "hc1", testClusterMCE, strPtr(""), "hc1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.discoveryPrefix != nil {
				os.Setenv("DISCOVERY_PREFIX", *tt.discoveryPrefix)
				defer os.Unsetenv("DISCOVERY_PREFIX")
			} else {
				os.Unsetenv("DISCOVERY_PREFIX")
			}
			zapLog, _ := zap.NewDevelopment()
			lc := &LabelAgent{clusterName: tt.clusterName, log: zapr.NewLogger(zapLog)}
			assert.Equal(t, tt.expected, lc.getSpokeMCName(tt.hubMCName))
		})
	}
}

func TestMapHubMCToSpokeMC(t *testing.T) {
	zapLog, _ := zap.NewDevelopment()
	os.Unsetenv("DISCOVERY_PREFIX")
	lc := &LabelAgent{clusterName: testClusterMCE, log: zapr.NewLogger(zapLog)}

	t.Run("matching hub MC returns request", func(t *testing.T) {
		mc := &clusterv1.ManagedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster-mce-hc1"}}
		reqs := lc.mapHubMCToSpokeMC(context.Background(), mc)
		assert.Equal(t, []reconcile.Request{{NamespacedName: types.NamespacedName{Name: "hc1"}}}, reqs)
	})

	t.Run("non-matching hub MC returns nil", func(t *testing.T) {
		mc := &clusterv1.ManagedCluster{ObjectMeta: metav1.ObjectMeta{Name: "other-hc1"}}
		assert.Nil(t, lc.mapHubMCToSpokeMC(context.Background(), mc))
	})
}

func TestMapHCToSpokeMC(t *testing.T) {
	zapLog, _ := zap.NewDevelopment()
	lc := &LabelAgent{log: zapr.NewLogger(zapLog)}

	t.Run("HC name maps to spoke MC", func(t *testing.T) {
		hc := &hyperv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "clusters"},
		}
		reqs := lc.mapHCToSpokeMC(context.Background(), hc)
		assert.Equal(t, []reconcile.Request{{NamespacedName: types.NamespacedName{Name: "my-hc"}}}, reqs)
	})

	t.Run("HC with annotation uses annotation value", func(t *testing.T) {
		hc := &hyperv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: "my-hc", Namespace: "clusters",
				Annotations: map[string]string{util.ManagedClusterAnnoKey: testCustomMCName},
			},
		}
		reqs := lc.mapHCToSpokeMC(context.Background(), hc)
		assert.Equal(t, []reconcile.Request{{NamespacedName: types.NamespacedName{Name: testCustomMCName}}}, reqs)
	})
}

// --- Predicate tests ---

func TestLabelEventFilters(t *testing.T) {
	pred := labelEventFilters()

	hosted := &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "hcp-cluster",
			Annotations: map[string]string{createdViaAnno: createdViaHypershift},
			Labels:      map[string]string{"env": "prod"},
		},
	}
	nonHosted := &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "regular",
			Labels: map[string]string{"env": "prod"},
		},
	}

	t.Run("Create accepts hosted MC", func(t *testing.T) {
		assert.True(t, pred.Create(event.CreateEvent{Object: hosted}))
	})
	t.Run("Create rejects non-hosted MC", func(t *testing.T) {
		assert.False(t, pred.Create(event.CreateEvent{Object: nonHosted}))
	})
	t.Run("Update accepts label change", func(t *testing.T) {
		old := hosted.DeepCopy()
		old.Labels = map[string]string{"env": "staging"}
		assert.True(t, pred.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: hosted}))
	})
	t.Run("Update rejects no change", func(t *testing.T) {
		assert.False(t, pred.Update(event.UpdateEvent{ObjectOld: hosted.DeepCopy(), ObjectNew: hosted}))
	})
	t.Run("Update rejects non-hosted MC", func(t *testing.T) {
		old := nonHosted.DeepCopy()
		old.Labels = map[string]string{"env": "staging"}
		assert.False(t, pred.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: nonHosted}))
	})
	t.Run("Update accepts propagated-labels annotation removed", func(t *testing.T) {
		old := hosted.DeepCopy()
		old.Annotations[propagatedLabelAnnotation] = testAnnotationEnvTeam
		assert.True(t, pred.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: hosted}))
	})
	t.Run("Update accepts hc-propagated-labels annotation removed", func(t *testing.T) {
		old := hosted.DeepCopy()
		old.Annotations[hcPropagatedLabelAnnotation] = "env"
		assert.True(t, pred.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: hosted}))
	})
	t.Run("Update accepts created-via annotation added", func(t *testing.T) {
		old := hosted.DeepCopy()
		delete(old.Annotations, createdViaAnno)
		assert.True(t, pred.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: hosted}))
	})
	t.Run("Create rejects non-MC object", func(t *testing.T) {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod"}}
		assert.False(t, pred.Create(event.CreateEvent{Object: pod}))
	})
	t.Run("Delete returns false", func(t *testing.T) {
		assert.False(t, pred.Delete(event.DeleteEvent{Object: hosted}))
	})
	t.Run("Generic returns false", func(t *testing.T) {
		assert.False(t, pred.Generic(event.GenericEvent{Object: hosted}))
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

	t.Run("Create accepts HC", func(t *testing.T) {
		assert.True(t, pred.Create(event.CreateEvent{Object: hc}))
	})
	t.Run("Update accepts label change", func(t *testing.T) {
		old := hc.DeepCopy()
		old.Labels = map[string]string{"env": "staging"}
		assert.True(t, pred.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: hc}))
	})
	t.Run("Update rejects no change", func(t *testing.T) {
		assert.False(t, pred.Update(event.UpdateEvent{ObjectOld: hc.DeepCopy(), ObjectNew: hc}))
	})
	t.Run("Delete accepts", func(t *testing.T) {
		assert.True(t, pred.Delete(event.DeleteEvent{Object: hc}))
	})
	t.Run("Generic returns false", func(t *testing.T) {
		assert.False(t, pred.Generic(event.GenericEvent{Object: hc}))
	})
}

// --- Utility tests ---

func TestParseAnnotation(t *testing.T) {
	assert.Empty(t, parseAnnotation(""))
	assert.Equal(t, map[string]bool{"env": true}, parseAnnotation("env"))
	assert.Equal(t, map[string]bool{"env": true, "team": true, "tier": true}, parseAnnotation("env,team,tier"))
}

func TestJoinSortedKeys(t *testing.T) {
	assert.Equal(t, "", joinSortedKeys(map[string]bool{}))
	assert.Equal(t, "env,team,tier", joinSortedKeys(map[string]bool{"team": true, "env": true, "tier": true}))
}

// --- findHostedCluster tests ---

func TestFindHostedCluster(t *testing.T) {
	t.Run("duplicate HC names returns ambiguous error", func(t *testing.T) {
		spoke, _ := initLabelSyncClient(t)
		ctx := context.Background()
		lc := newLabelAgent(t, spoke, nil, "mce")

		spokeMC := &clusterv1.ManagedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "my-hc",
				Annotations: map[string]string{createdViaAnno: createdViaHypershift},
			},
		}
		assert.Nil(t, spoke.Create(ctx, &hyperv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "ns-a"},
		}))
		assert.Nil(t, spoke.Create(ctx, &hyperv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "ns-b"},
		}))

		result, err := lc.findHostedCluster(ctx, spokeMC)
		assert.ErrorIs(t, err, errAmbiguousHCMatch)
		assert.Nil(t, result)
	})

	t.Run("infraID match takes priority over name", func(t *testing.T) {
		spoke, _ := initLabelSyncClient(t)
		ctx := context.Background()
		lc := newLabelAgent(t, spoke, nil, "mce")

		spokeMC := &clusterv1.ManagedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "my-hc",
				Annotations: map[string]string{createdViaAnno: createdViaHypershift},
				Labels:      map[string]string{testAutoCreatedForInfra: testInfraID},
			},
		}
		// Name-only match (no infraID, no annotation)
		assert.Nil(t, spoke.Create(ctx, &hyperv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "wrong-ns"},
		}))
		// InfraID match with a different name
		assert.Nil(t, spoke.Create(ctx, &hyperv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "infra-hc", Namespace: "clusters"},
			Spec:       hyperv1beta1.HostedClusterSpec{InfraID: testInfraID},
		}))

		result, err := lc.findHostedCluster(ctx, spokeMC)
		assert.Nil(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "infra-hc", result.Name, "infraID match should win over name")
		assert.Equal(t, "clusters", result.Namespace)
	})

	t.Run("annotation match takes priority over infraID", func(t *testing.T) {
		spoke, _ := initLabelSyncClient(t)
		ctx := context.Background()
		lc := newLabelAgent(t, spoke, nil, "mce")

		spokeMC := &clusterv1.ManagedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:        testCustomMCName,
				Annotations: map[string]string{createdViaAnno: createdViaHypershift},
				Labels:      map[string]string{testAutoCreatedForInfra: testInfraID},
			},
		}
		assert.Nil(t, spoke.Create(ctx, &hyperv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: "my-hc", Namespace: "clusters",
				Annotations: map[string]string{util.ManagedClusterAnnoKey: testCustomMCName},
			},
		}))
		assert.Nil(t, spoke.Create(ctx, &hyperv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "other-hc", Namespace: "other-ns"},
			Spec:       hyperv1beta1.HostedClusterSpec{InfraID: testInfraID},
		}))

		result, err := lc.findHostedCluster(ctx, spokeMC)
		assert.Nil(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "my-hc", result.Name, "annotation match should win")
	})
}
