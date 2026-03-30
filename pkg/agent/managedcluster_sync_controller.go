package agent

import (
	"context"
	"maps"
	"os"
	"sort"
	"strings"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const propagatedLabelAnnotation = "hypershift.open-cluster-management.io/propagated-labels"

type LabelAgent struct {
	hubClient        client.Client
	spokeClient      client.Client
	hubCache         cache.Cache
	clusterName      string
	localClusterName string
	log              logr.Logger
}

// SetupWithManager sets up the controller with the Manager.
func (c *LabelAgent) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&clusterv1.ManagedCluster{}, builder.WithPredicates(labelEventFilters())).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		WatchesRawSource(
			source.Kind(c.hubCache, &clusterv1.ManagedCluster{},
				handler.TypedEnqueueRequestsFromMapFunc(c.mapHubMCToSpokeMC)),
		).
		Complete(c)
}

// Reconcile syncs non-system labels from the ACM hub ManagedCluster to the
// corresponding spoke ManagedCluster, correcting drift and tracking propagated keys.
func (c *LabelAgent) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c.log.Info("reconciling label sync", "managedcluster", req.Name)

	if strings.EqualFold(os.Getenv("DISABLE_HC_DISCOVERY"), "true") {
		c.log.Info("hosted cluster discovery is disabled, skip labeling")
		return ctrl.Result{}, nil
	}

	// if this agent is for self managed cluster aka local-cluster, skip label sync
	if strings.EqualFold(c.clusterName, c.localClusterName) {
		c.log.Info("this is local cluster agent, skip label checking")
		return ctrl.Result{}, nil
	}

	// Get the spoke MC by name from req.NamespacedName using c.spokeClient
	spokeMC := &clusterv1.ManagedCluster{}
	if err := c.spokeClient.Get(ctx, req.NamespacedName, spokeMC); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Verify this is a hosted cluster MC (check created-via annotation)
	if spokeMC.Annotations[createdViaAnno] != createdViaHypershift {
		c.log.Info("this is not a hosted cluster or is missing the hypershift annotation, skip label checking")
		return ctrl.Result{}, nil
	}

	// Compute the hub MC name using getHubMCName(spokeMC.Name)
	hubMCName := c.getHubMCName(spokeMC.Name)

	// Fetch the hub MC using c.hubClient.Get()
	// If not found, log and requeue
	hubMC := &clusterv1.ManagedCluster{}
	if err := c.hubClient.Get(ctx, types.NamespacedName{Name: hubMCName}, hubMC); err != nil {
		if apierrors.IsNotFound(err) {
			c.log.Info("hub ManagedCluster not found, removing propagated labels", "name", hubMCName)
			return ctrl.Result{}, c.removePropagatedLabels(ctx, spokeMC)
		}
		c.log.Error(err, "error getting ManagedCluster from ACM hub", "name", hubMCName)
		return ctrl.Result{}, err
	}

	if err := c.syncLabelsFromHub(ctx, spokeMC, hubMC); err != nil {
		c.log.Error(err, "error syncing labels", "hubMC", hubMCName, "spokeMC", spokeMC.Name)
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// isSystemLabel returns true if the label key is managed by OCM/system
// and should never be propagated. The cluster.open-cluster-management.io/
// prefix is explicitly allowed for clusterset label propagation.
func isSystemLabel(key string) bool {
	var systemLabelKeys = map[string]bool{
		"name":                          true,
		"vendor":                        true,
		"cloud":                         true,
		"openshiftVersion":              true,
		"openshiftVersion-major":        true,
		"openshiftVersion-major-minor":  true,
		"clusterID":                     true,
		"local-cluster":                 true,
		"velero.io/exclude-from-backup": true,
		"region":                        true,
	}

	if systemLabelKeys[key] {
		return true
	}

	if strings.HasPrefix(key, "cluster.open-cluster-management.io/") {
		return false
	}

	var systemPrefixes = []string{
		"open-cluster-management.io/",
		"kubernetes.io/",
		"openshift.io/",
		"k8s.io/",
	}

	for _, p := range systemPrefixes {
		if strings.Contains(key, p) {
			return true
		}
	}
	return false
}

// syncLabelsFromHub applies non-system labels from the hub MC to the spoke MC,
// using the tracking annotation to manage additions, updates, and removals.
func (c *LabelAgent) syncLabelsFromHub(
	ctx context.Context, spokeMC, hubMC *clusterv1.ManagedCluster,
) error {
	// Read the current tracking annotation (propagatedLabelsAnno) from spokeMC
	// Parse the comma-separated list of previously propagated label keys
	previousAnnotationTracking := map[string]bool{}
	if annotations := spokeMC.Annotations[propagatedLabelAnnotation]; annotations != "" {
		for _, key := range strings.Split(annotations, ",") {
			previousAnnotationTracking[key] = true
		}
	}

	// Build the set of labels to propagate from hubMC
	// For each hub label, skip if isSystemLabel(key)

	// Apply sync rules:
	// - Hub has label, Spoke does not -> add to spoke, record in tracking
	// - Hub has label, Spoke has different value, key IS in tracking -> update spoke
	// - Hub has label, Spoke has different value, key NOT in tracking -> skip (locally set)
	// - Key in tracking but no longer on hub -> remove from spoke and tracking
	labelsToPropagate := map[string]bool{}
	spokeLabels := spokeMC.DeepCopy().Labels
	if spokeLabels == nil {
		spokeLabels = map[string]string{}
	}
	changed := false
	for hubKey, hubValue := range hubMC.Labels {
		if isSystemLabel(hubKey) {
			continue
		}

		spokeValue, existsOnSpoke := spokeLabels[hubKey]
		managed := false
		if !existsOnSpoke {
			spokeLabels[hubKey] = hubValue
			changed = true
			managed = true
		} else if hubValue != spokeValue {
			if previousAnnotationTracking[hubKey] {
				spokeLabels[hubKey] = hubValue
				changed = true
				managed = true
			}
		} else {
			managed = true
		}
		if managed {
			labelsToPropagate[hubKey] = true
		}
	}

	// go through the previous tracking labels and if they dont exist on the current hub labels
	// then remove the previously tracked labels
	for key := range previousAnnotationTracking {
		if _, exists := hubMC.Labels[key]; !exists {
			delete(spokeLabels, key)
			changed = true
		}
	}

	// Check if the tracking annotation needs updating in case labels match on hub and spoke but isnt tracked
	if !changed && len(labelsToPropagate) == len(previousAnnotationTracking) {
		allMatch := true
		for key := range labelsToPropagate {
			if !previousAnnotationTracking[key] {
				allMatch = false
				break
			}
		}
		if allMatch {
			return nil
		}
	}

	// If changes were made, patch the spoke MC using client.MergeFrom()
	// Update both labels and the tracking annotation
	keys := make([]string, 0, len(labelsToPropagate))
	for key := range labelsToPropagate {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	// create a patch
	patch := client.MergeFrom(spokeMC.DeepCopy())
	// set the labels calculated to the spoke
	spokeMC.Labels = spokeLabels
	if spokeMC.Annotations == nil {
		spokeMC.Annotations = map[string]string{}
	}
	// add labels propagated in the annotations
	spokeMC.Annotations[propagatedLabelAnnotation] = strings.Join(keys, ",")

	// patch the spoke with labels and annotations
	return c.spokeClient.Patch(ctx, spokeMC, patch)
}

// removePropagatedLabels removes all previously propagated labels from the spoke
// ManagedCluster and clears the tracking annotation. Called when the hub MC is deleted.
func (c *LabelAgent) removePropagatedLabels(ctx context.Context, spokeMC *clusterv1.ManagedCluster) error {
	anno := spokeMC.Annotations[propagatedLabelAnnotation]
	if anno == "" {
		return nil
	}

	patch := client.MergeFrom(spokeMC.DeepCopy())
	for _, key := range strings.Split(anno, ",") {
		delete(spokeMC.Labels, key)
	}
	delete(spokeMC.Annotations, propagatedLabelAnnotation)

	c.log.Info("removing propagated labels from spoke", "spokeMC", spokeMC.Name, "labels", anno)
	return c.spokeClient.Patch(ctx, spokeMC, patch)
}

// mapHubMCToSpokeMC translates a hub ManagedCluster event into a reconcile
// request for the corresponding spoke ManagedCluster.
func (c *LabelAgent) mapHubMCToSpokeMC(ctx context.Context, hubMC *clusterv1.ManagedCluster) []reconcile.Request {
	spokeMCName := c.getSpokeMCName(hubMC.Name)
	if spokeMCName == "" {
		return nil
	}
	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: spokeMCName}},
	}
}

// getSpokeMCName derives the spoke ManagedCluster name from a hub MC name
// by stripping the discovery prefix (default, custom, or empty).
func (c *LabelAgent) getSpokeMCName(hubMCName string) string {
	prefix, set := os.LookupEnv("DISCOVERY_PREFIX")
	if set {
		// case where prefix is custom
		if len(prefix) > 0 {
			p := prefix + "-"
			if spokeName, found := strings.CutPrefix(hubMCName, p); found {
				return spokeName
			}
			// case where a non HostedCluster might have the same prefix as a HostedCluster
			return ""
		}
		// case where prefix is empty
		return hubMCName
	}
	// case where prefix is default
	if spokeName, found := strings.CutPrefix(hubMCName, c.clusterName+"-"); found {
		return spokeName
	}
	return ""
}

// getHubMCName returns the hub ManagedCluster name for a given spoke MC name.
func (c *LabelAgent) getHubMCName(spokeMCName string) string {
	return getDiscoveredClusterName(c.clusterName, spokeMCName, c.log)
}

// labelEventFilters filters spoke ManagedCluster events to only process hosted clusters.
// Both create and update events are handled to correct spoke-side label drift.
func labelEventFilters() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			mc, ok := e.Object.(*clusterv1.ManagedCluster)

			if !ok {
				return false
			}

			return mc.Annotations[createdViaAnno] == createdViaHypershift
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			mc, ok := e.ObjectNew.(*clusterv1.ManagedCluster)
			if !ok {
				return false
			}
			if maps.Equal(e.ObjectOld.GetLabels(), e.ObjectNew.GetLabels()) {
				return false
			}
			return mc.Annotations[createdViaAnno] == createdViaHypershift
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}
}
