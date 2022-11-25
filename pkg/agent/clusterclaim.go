package agent

import (
	"context"
	"fmt"
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

	hypershiftManagementClusterClaimKey   = "hostingcluster.hypershift.openshift.io"
	hypershiftHostedClusterClaimKey       = "hostedcluster.hypershift.openshift.io"
	hostedClusterCountFullClusterClaimKey = "hostedclustercount.full.hypershift.openshift.io"
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

func (c *agentController) createHostedClusterCountClusterClaim(ctx context.Context, count int) error {
	if count > (util.MaxHostedClusterCount - 1) {
		c.log.Info(fmt.Sprintf("ATTENTION: the hosted cluster count has reached the maximum %s.", strconv.Itoa(util.MaxHostedClusterCount)))
	} else {
		c.log.Info(fmt.Sprintf("the hosted cluster count has not reached the maximum %s yet. current count is %s", strconv.Itoa(util.MaxHostedClusterCount), strconv.Itoa(count)))
	}
	managementClaim := newClusterClaim(hostedClusterCountFullClusterClaimKey, strconv.FormatBool(count > (util.MaxHostedClusterCount-1)))
	return createOrUpdate(ctx, c.spokeClustersClient, managementClaim)
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
