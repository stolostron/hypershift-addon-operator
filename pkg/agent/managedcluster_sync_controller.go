package agent

import (
	"context"
	"maps"
	"os"
	"sort"
	"strings"

	"github.com/go-logr/logr"
	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
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

const (
	propagatedLabelAnnotation   = "hypershift.open-cluster-management.io/propagated-labels"
	hcPropagatedLabelAnnotation = "hypershift.open-cluster-management.io/hc-propagated-labels"
)

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
		Watches(&hyperv1beta1.HostedCluster{},
			handler.EnqueueRequestsFromMapFunc(c.mapHCToSpokeMC),
			builder.WithPredicates(hcLabelEventFilters()),
		).
		Complete(c)
}

// Reconcile syncs labels from the HostedCluster and ACM hub ManagedCluster to the
// corresponding spoke ManagedCluster. HC labels have the highest priority, followed
// by admin-added hub labels.
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

	// HC sync (highest priority -- HC labels override everything)
	hc, err := c.findHostedCluster(ctx, spokeMC.Name)
	if err != nil {
		c.log.Error(err, "error finding HostedCluster", "spokeMC", spokeMC.Name)
		return ctrl.Result{}, err
	}

	if hc != nil {
		if err := c.syncHCLabelsToSpoke(ctx, spokeMC, hc); err != nil {
			c.log.Error(err, "error syncing HC labels to spoke", "hc", hc.Name, "spokeMC", spokeMC.Name)
			return ctrl.Result{}, err
		}

		hubMCName := c.getHubMCName(spokeMC.Name)
		hubMC := &clusterv1.ManagedCluster{}
		if err := c.hubClient.Get(ctx, types.NamespacedName{Name: hubMCName}, hubMC); err == nil {
			if err := c.syncHCLabelsToHub(ctx, hubMC, hc); err != nil {
				c.log.Error(err, "error syncing HC labels to hub", "hc", hc.Name, "hubMC", hubMCName)
				return ctrl.Result{}, err
			}
		} else if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	} else {
		if err := c.removeHCPropagatedLabels(ctx, spokeMC); err != nil {
			c.log.Error(err, "error removing HC propagated labels from spoke", "spokeMC", spokeMC.Name)
			return ctrl.Result{}, err
		}
		hubMCName := c.getHubMCName(spokeMC.Name)
		hubMC := &clusterv1.ManagedCluster{}
		if err := c.hubClient.Get(ctx, types.NamespacedName{Name: hubMCName}, hubMC); err == nil {
			if err := c.removeHCPropagatedLabelsFromHub(ctx, hubMC); err != nil {
				c.log.Error(err, "error removing HC propagated labels from hub", "hubMC", hubMCName)
				return ctrl.Result{}, err
			}
		}
	}

	// Re-fetch spoke MC to get latest state after HC sync may have modified it
	if err := c.spokeClient.Get(ctx, req.NamespacedName, spokeMC); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Hub sync (only admin-added labels, skip HC-owned keys on hub)
	hubMCName := c.getHubMCName(spokeMC.Name)
	hubMC := &clusterv1.ManagedCluster{}
	if err := c.hubClient.Get(ctx, types.NamespacedName{Name: hubMCName}, hubMC); err != nil {
		if apierrors.IsNotFound(err) {
			c.log.Info("hub ManagedCluster not found, removing hub propagated labels", "name", hubMCName)
			return ctrl.Result{}, c.removeHubPropagatedLabels(ctx, spokeMC)
		}
		c.log.Error(err, "error getting ManagedCluster from ACM hub", "name", hubMCName)
		return ctrl.Result{}, err
	}

	if err := c.syncHubLabelsToSpoke(ctx, spokeMC, hubMC); err != nil {
		c.log.Error(err, "error syncing hub labels to spoke", "hubMC", hubMCName, "spokeMC", spokeMC.Name)
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// findHostedCluster searches for the HostedCluster that corresponds to the given
// spoke ManagedCluster name. Checks the managedcluster-name annotation first,
// then falls back to name match.
func (c *LabelAgent) findHostedCluster(
	ctx context.Context, mcName string,
) (*hyperv1beta1.HostedCluster, error) {
	hcList := &hyperv1beta1.HostedClusterList{}
	if err := c.spokeClient.List(ctx, hcList); err != nil {
		return nil, err
	}
	for i := range hcList.Items {
		hc := &hcList.Items[i]
		if annoName := hc.Annotations[util.ManagedClusterAnnoKey]; len(annoName) > 0 {
			if annoName == mcName {
				return hc, nil
			}
		} else if hc.Name == mcName {
			return hc, nil
		}
	}
	return nil, nil
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

// syncHCLabelsToSpoke applies non-system HostedCluster labels to the spoke MC.
// HC is the highest-priority source and always overwrites existing values.
// Tracked via the hc-propagated-labels annotation. If a key was previously
// hub-tracked (in propagated-labels), HC takes ownership by removing it.
func (c *LabelAgent) syncHCLabelsToSpoke(
	ctx context.Context, spokeMC *clusterv1.ManagedCluster, hc *hyperv1beta1.HostedCluster,
) error {
	previousHCTracking := parseAnnotation(spokeMC.Annotations[hcPropagatedLabelAnnotation])

	hcLabelsToTrack := map[string]bool{}
	spokeLabels := spokeMC.DeepCopy().Labels
	if spokeLabels == nil {
		spokeLabels = map[string]string{}
	}
	changed := false

	for key, value := range hc.Labels {
		if isSystemLabel(key) {
			continue
		}
		existingValue, exists := spokeLabels[key]
		if !exists || existingValue != value {
			spokeLabels[key] = value
			changed = true
		}
		hcLabelsToTrack[key] = true
	}

	// Remove labels previously HC-tracked but no longer on the HC
	for key := range previousHCTracking {
		if !hcLabelsToTrack[key] {
			delete(spokeLabels, key)
			changed = true
		}
	}

	// If HC is taking over any hub-tracked keys, remove them from hub tracking
	previousHubTracking := parseAnnotation(spokeMC.Annotations[propagatedLabelAnnotation])
	updatedHubTracking := map[string]bool{}
	hubTrackingModified := false
	for key := range previousHubTracking {
		if hcLabelsToTrack[key] {
			hubTrackingModified = true
		} else {
			updatedHubTracking[key] = true
		}
	}

	hcTrackingChanged := !maps.Equal(hcLabelsToTrack, previousHCTracking)

	if !changed && !hcTrackingChanged && !hubTrackingModified {
		return nil
	}

	patch := client.MergeFrom(spokeMC.DeepCopy())
	spokeMC.Labels = spokeLabels
	if spokeMC.Annotations == nil {
		spokeMC.Annotations = map[string]string{}
	}

	setOrDeleteAnnotation(spokeMC, hcPropagatedLabelAnnotation, joinSortedKeys(hcLabelsToTrack))

	if hubTrackingModified {
		setOrDeleteAnnotation(spokeMC, propagatedLabelAnnotation, joinSortedKeys(updatedHubTracking))
	}

	return c.spokeClient.Patch(ctx, spokeMC, patch)
}

// syncHCLabelsToHub applies non-system HostedCluster labels to the ACM hub MC.
// HC always overwrites existing values. Tracked via the hc-propagated-labels
// annotation on the hub MC, which hub sync uses to skip HC-owned keys.
func (c *LabelAgent) syncHCLabelsToHub(
	ctx context.Context, hubMC *clusterv1.ManagedCluster, hc *hyperv1beta1.HostedCluster,
) error {
	previousHCTracking := parseAnnotation(hubMC.Annotations[hcPropagatedLabelAnnotation])

	hcLabelsToTrack := map[string]bool{}
	hubLabels := hubMC.DeepCopy().Labels
	if hubLabels == nil {
		hubLabels = map[string]string{}
	}
	changed := false

	for key, value := range hc.Labels {
		if isSystemLabel(key) {
			continue
		}
		existingValue, exists := hubLabels[key]
		if !exists || existingValue != value {
			hubLabels[key] = value
			changed = true
		}
		hcLabelsToTrack[key] = true
	}

	for key := range previousHCTracking {
		if !hcLabelsToTrack[key] {
			delete(hubLabels, key)
			changed = true
		}
	}

	hcTrackingChanged := !maps.Equal(hcLabelsToTrack, previousHCTracking)

	if !changed && !hcTrackingChanged {
		return nil
	}

	patch := client.MergeFrom(hubMC.DeepCopy())
	hubMC.Labels = hubLabels
	if hubMC.Annotations == nil {
		hubMC.Annotations = map[string]string{}
	}

	setOrDeleteAnnotation(hubMC, hcPropagatedLabelAnnotation, joinSortedKeys(hcLabelsToTrack))

	return c.hubClient.Patch(ctx, hubMC, patch)
}

// syncHubLabelsToSpoke applies non-system labels from the hub MC to the spoke MC,
// using the tracking annotation to manage additions, updates, and removals.
// Labels owned by HC (in hc-propagated-labels on the hub MC) are skipped.
func (c *LabelAgent) syncHubLabelsToSpoke(
	ctx context.Context, spokeMC, hubMC *clusterv1.ManagedCluster,
) error {
	previousAnnotationTracking := parseAnnotation(spokeMC.Annotations[propagatedLabelAnnotation])
	hcOwnedOnHub := parseAnnotation(hubMC.Annotations[hcPropagatedLabelAnnotation])

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
		if hcOwnedOnHub[hubKey] {
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

	if !changed && maps.Equal(labelsToPropagate, previousAnnotationTracking) {
		return nil
	}

	patch := client.MergeFrom(spokeMC.DeepCopy())
	spokeMC.Labels = spokeLabels
	if spokeMC.Annotations == nil {
		spokeMC.Annotations = map[string]string{}
	}
	spokeMC.Annotations[propagatedLabelAnnotation] = joinSortedKeys(labelsToPropagate)

	return c.spokeClient.Patch(ctx, spokeMC, patch)
}

// removeHubPropagatedLabels removes all hub-propagated labels from the spoke
// ManagedCluster and clears the tracking annotation. Called when the hub MC is deleted.
func (c *LabelAgent) removeHubPropagatedLabels(ctx context.Context, spokeMC *clusterv1.ManagedCluster) error {
	anno := spokeMC.Annotations[propagatedLabelAnnotation]
	if anno == "" {
		return nil
	}

	patch := client.MergeFrom(spokeMC.DeepCopy())
	for _, key := range strings.Split(anno, ",") {
		delete(spokeMC.Labels, key)
	}
	delete(spokeMC.Annotations, propagatedLabelAnnotation)

	c.log.Info("removing hub propagated labels from spoke", "spokeMC", spokeMC.Name, "labels", anno)
	return c.spokeClient.Patch(ctx, spokeMC, patch)
}

// removeHCPropagatedLabels removes all HC-propagated labels from the spoke
// ManagedCluster and clears the hc-propagated-labels annotation.
func (c *LabelAgent) removeHCPropagatedLabels(ctx context.Context, spokeMC *clusterv1.ManagedCluster) error {
	anno := spokeMC.Annotations[hcPropagatedLabelAnnotation]
	if anno == "" {
		return nil
	}

	patch := client.MergeFrom(spokeMC.DeepCopy())
	for _, key := range strings.Split(anno, ",") {
		delete(spokeMC.Labels, key)
	}
	delete(spokeMC.Annotations, hcPropagatedLabelAnnotation)

	c.log.Info("removing HC propagated labels from spoke", "spokeMC", spokeMC.Name, "labels", anno)
	return c.spokeClient.Patch(ctx, spokeMC, patch)
}

// removeHCPropagatedLabelsFromHub removes all HC-propagated labels from the hub
// ManagedCluster and clears the hc-propagated-labels annotation.
func (c *LabelAgent) removeHCPropagatedLabelsFromHub(ctx context.Context, hubMC *clusterv1.ManagedCluster) error {
	anno := hubMC.Annotations[hcPropagatedLabelAnnotation]
	if anno == "" {
		return nil
	}

	patch := client.MergeFrom(hubMC.DeepCopy())
	for _, key := range strings.Split(anno, ",") {
		delete(hubMC.Labels, key)
	}
	delete(hubMC.Annotations, hcPropagatedLabelAnnotation)

	c.log.Info("removing HC propagated labels from hub", "hubMC", hubMC.Name, "labels", anno)
	return c.hubClient.Patch(ctx, hubMC, patch)
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

// mapHCToSpokeMC translates a HostedCluster event into a reconcile request
// for the corresponding spoke ManagedCluster. Checks the managedcluster-name
// annotation first, then falls back to HC name.
func (c *LabelAgent) mapHCToSpokeMC(ctx context.Context, obj client.Object) []reconcile.Request {
	if annoName := obj.GetAnnotations()[util.ManagedClusterAnnoKey]; len(annoName) > 0 {
		return []reconcile.Request{
			{NamespacedName: types.NamespacedName{Name: annoName}},
		}
	}
	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: obj.GetName()}},
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
// Triggers on label changes, tracking annotation changes, or created-via annotation changes.
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
			if mc.Annotations[createdViaAnno] != createdViaHypershift {
				return false
			}
			if !maps.Equal(e.ObjectOld.GetLabels(), e.ObjectNew.GetLabels()) {
				return true
			}
			oldAnno := e.ObjectOld.GetAnnotations()
			newAnno := e.ObjectNew.GetAnnotations()
			return oldAnno[propagatedLabelAnnotation] != newAnno[propagatedLabelAnnotation] ||
				oldAnno[hcPropagatedLabelAnnotation] != newAnno[hcPropagatedLabelAnnotation] ||
				oldAnno[createdViaAnno] != newAnno[createdViaAnno]
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}
}

// hcLabelEventFilters filters HostedCluster events to trigger on label changes
// and deletions (for cleanup of HC-propagated labels).
func hcLabelEventFilters() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return !maps.Equal(e.ObjectOld.GetLabels(), e.ObjectNew.GetLabels())
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return true
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}
}

func parseAnnotation(anno string) map[string]bool {
	result := map[string]bool{}
	if anno != "" {
		for _, key := range strings.Split(anno, ",") {
			result[key] = true
		}
	}
	return result
}

func joinSortedKeys(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func setOrDeleteAnnotation(mc *clusterv1.ManagedCluster, key, value string) {
	if value != "" {
		mc.Annotations[key] = value
	} else {
		delete(mc.Annotations, key)
	}
}
