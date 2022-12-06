package agent

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strconv"

	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	clusterclientset "open-cluster-management.io/api/client/cluster/clientset/versioned"

	clusterv1alpha1 "open-cluster-management.io/api/cluster/v1alpha1"
)

const (
	// labelExcludeBackup is true for the local-cluster will not be backed up into velero
	labelExcludeBackup = "velero.io/exclude-from-backup"

	hypershiftManagementClusterClaimKey             = "hostingcluster.hypershift.openshift.io"
	hypershiftHostedClusterClaimKey                 = "hostedcluster.hypershift.openshift.io"
	hostedClusterCountFullClusterClaimKey           = "full.hostedclustercount.hypershift.openshift.io"
	hostedClusterCountAboveThresholdClusterClaimKey = "above.threshold.hostedclustercount.hypershift.openshift.io"
	hostedClusterCountZeroClusterClaimKey           = "zero.hostedclustercount.hypershift.openshift.io"
)

func newClusterClaim(name, value string) *clusterv1alpha1.ClusterClaim {
	return &clusterv1alpha1.ClusterClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{labelExcludeBackup: "true"},
		},
		Spec: clusterv1alpha1.ClusterClaimSpec{
			Value: value,
		},
	}
}

func createOrUpdate(ctx context.Context, client clusterclientset.Interface, newClaim *clusterv1alpha1.ClusterClaim) error {
	oldClaim, err := client.ClusterV1alpha1().ClusterClaims().Get(ctx, newClaim.Name, metav1.GetOptions{})
	switch {
	case errors.IsNotFound(err):
		_, err := client.ClusterV1alpha1().ClusterClaims().Create(ctx, newClaim, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("unable to create ClusterClaim: %v, %w", newClaim, err)
		}
	case err != nil:
		return fmt.Errorf("unable to get ClusterClaim %q: %w", newClaim.Name, err)
	case !reflect.DeepEqual(oldClaim.Spec, newClaim.Spec):
		oldClaim.Spec = newClaim.Spec
		_, err := client.ClusterV1alpha1().ClusterClaims().Update(ctx, oldClaim, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("unable to update ClusterClaim %q: %w", oldClaim.Name, err)
		}
	}

	return nil
}

func (c *agentController) createManagementClusterClaim(ctx context.Context) error {
	managementClaim := newClusterClaim(hypershiftManagementClusterClaimKey, "true")
	return createOrUpdate(ctx, c.spokeClustersClient, managementClaim)
}

func (c *agentController) createHostedClusterFullClusterClaim(ctx context.Context, count int) error {
	if count >= c.maxHostedClusterCount {
		c.log.Info(fmt.Sprintf("ATTENTION: the hosted cluster count has reached the maximum %s.", strconv.Itoa(c.maxHostedClusterCount)))
	} else {
		c.log.Info(fmt.Sprintf("the hosted cluster count has not reached the maximum %s yet. current count is %s", strconv.Itoa(c.maxHostedClusterCount), strconv.Itoa(count)))
	}
	hcFullClaim := newClusterClaim(hostedClusterCountFullClusterClaimKey, strconv.FormatBool(count >= c.maxHostedClusterCount))
	return createOrUpdate(ctx, c.spokeClustersClient, hcFullClaim)
}

func (c *agentController) createHostedClusterThresholdClusterClaim(ctx context.Context, count int) error {
	hcThresholdClaim := newClusterClaim(hostedClusterCountAboveThresholdClusterClaimKey, strconv.FormatBool(count >= c.thresholdHostedClusterCount))
	return createOrUpdate(ctx, c.spokeClustersClient, hcThresholdClaim)
}

func (c *agentController) createHostedClusterZeroClusterClaim(ctx context.Context, count int) error {
	hcZeroClaim := newClusterClaim(hostedClusterCountZeroClusterClaimKey, strconv.FormatBool(count == 0))
	return createOrUpdate(ctx, c.spokeClustersClient, hcZeroClaim)
}

func (c *agentController) createHostedClusterClaim(ctx context.Context, secretKey types.NamespacedName,
	generateClusterClientFromSecret func(secret *corev1.Secret) (clusterclientset.Interface, error)) error {
	secret := &corev1.Secret{}
	err := c.spokeClient.Get(ctx, secretKey, secret)
	if err != nil {
		return fmt.Errorf("unable to get hosted secret %s/%s, err: %w", secretKey.Namespace, secretKey.Name, err)
	}

	clusterClient, err := generateClusterClientFromSecret(secret)
	if err != nil {
		return fmt.Errorf("failed to create spoke clusters client, err: %w", err)
	}

	hostedClaim := newClusterClaim(hypershiftHostedClusterClaimKey, "true")
	err = createOrUpdate(ctx, clusterClient, hostedClaim)
	if err != nil {
		return err
	}
	return nil
}

func generateClusterClientFromSecret(secret *corev1.Secret) (clusterclientset.Interface, error) {
	config, err := util.GenerateClientConfigFromSecret(secret)
	if err != nil {
		return nil, fmt.Errorf("unable to generate client config from secret: %w", err)
	}

	clusterClient, err := clusterclientset.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create spoke clusters client, err: %w", err)
	}

	return clusterClient, nil
}

// As the number of hosted cluster count reaches the max and threshold hosted cluster counts
// are used to generate two cluster claims:
// "above.threshold.hostedclustercount.hypershift.openshift.io" = true when the count > threshold
// "full.hostedclustercount.hypershift.openshift.io" = true when the count >= max
// Both max and threshold numbers should be valid positive integer numbers and max >= threshold.
// If not, they default to 80 max and 60 threshold.
func (c *agentController) getMaxAndThresholdHCCount() (int, int) {
	maxNum := util.DefaultMaxHostedClusterCount
	envMax := os.Getenv("HC_MAX_NUMBER")
	if envMax == "" {
		c.log.Info("env variable HC_MAX_NUMBER not found, defaulting to 80")
	}

	maxNum, err := strconv.Atoi(envMax)
	if err != nil {
		c.log.Error(nil, fmt.Sprintf("failed to convert env variable HC_MAX_NUMBER %s to integer, defaulting to 80", envMax))
		maxNum = util.DefaultMaxHostedClusterCount
	}

	if maxNum < 1 {
		c.log.Error(nil, fmt.Sprintf("invalid HC_MAX_NUMBER %s, defaulting to 80", envMax))
		maxNum = util.DefaultMaxHostedClusterCount
	}

	thresholdNum := util.DefaultThresholdHostedClusterCount
	envThreshold := os.Getenv("HC_THRESHOLD_NUMBER")
	if envThreshold == "" {
		c.log.Info("env variable HC_THRESHOLD_NUMBER not found, defaulting to 60")
	}

	thresholdNum, err = strconv.Atoi(envThreshold)
	if err != nil {
		c.log.Error(nil, fmt.Sprintf("failed to convert env variable HC_THRESHOLD_NUMBER %s to integer, defaulting to 60", envThreshold))
		thresholdNum = util.DefaultThresholdHostedClusterCount
	}

	if thresholdNum < 1 {
		c.log.Error(nil, fmt.Sprintf("invalid HC_MAX_NUMBER %s, defaulting to 60", envThreshold))
		thresholdNum = util.DefaultThresholdHostedClusterCount
	}

	if maxNum < thresholdNum {
		c.log.Error(nil, fmt.Sprintf(
			"invalid HC_MAX_NUMBER %s HC_THRESHOLD_NUMBER %s: HC_MAX_NUMBER must be equal or bigger than HC_THRESHOLD_NUMBER, defaulting to 80 and 60 for max and threshold counts",
			envMax, envThreshold))
		maxNum = util.DefaultMaxHostedClusterCount
		thresholdNum = util.DefaultThresholdHostedClusterCount
	}

	return maxNum, thresholdNum
}
