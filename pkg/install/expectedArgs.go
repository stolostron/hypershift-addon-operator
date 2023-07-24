package install

import (
	"fmt"
	"strings"

	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

type expectedConfig struct {
	objectName     string
	deploymentName string
	objectArgs     []expectedArg
	NoObjectArgs   []expectedArg
	objectType     interface{}
}

type expectedArg struct {
	shouldExist bool
	argument    string
}

var (
	expected = []expectedConfig{
		{
			objectName: util.HypershiftBucketSecretName,
			objectType: corev1.Secret{},
			objectArgs: []expectedArg{
				{argument: "--oidc-storage-provider-s3-bucket-name={bucket}", shouldExist: true},
				{argument: "--oidc-storage-provider-s3-region={region}", shouldExist: true},
				{argument: "--oidc-storage-provider-s3-credentials=/etc/oidc-storage-provider-s3-creds/credentials", shouldExist: true},
			},
			NoObjectArgs: []expectedArg{
				{argument: "--oidc-storage-provider-s3-bucket-name=", shouldExist: false},
				{argument: "--oidc-storage-provider-s3-region=", shouldExist: false},
				{argument: "--oidc-storage-provider-s3-credentials=/etc/oidc-storage-provider-s3-creds/credentials", shouldExist: false},
			},
			deploymentName: util.HypershiftOperatorName,
		},
		{
			objectName: util.HypershiftPrivateLinkSecretName,
			objectType: corev1.Secret{},
			objectArgs: []expectedArg{
				{argument: "--private-platform=AWS", shouldExist: true},
				{argument: "--private-platform=None", shouldExist: false},
			},
			NoObjectArgs: []expectedArg{
				{argument: "--private-platform=None", shouldExist: true},
				{argument: "--private-platform=AWS", shouldExist: false},
			},
			deploymentName: util.HypershiftOperatorName,
		},
		{
			objectName: util.HypershiftExternalDNSSecretName,
			objectType: corev1.Secret{},
			objectArgs: []expectedArg{
				{argument: "--domain-filter={domain-filter}", shouldExist: true},
				{argument: "--provider={provider}", shouldExist: true},
			},
			deploymentName: util.HypershiftOperatorExternalDNSName,
		},
		{
			objectName:     util.HypershiftInstallFlagsCM,
			objectType:     corev1.ConfigMap{},
			objectArgs:     []expectedArg{}, // fill out directly from config map
			NoObjectArgs:   []expectedArg{}, // fill out directly from config map
			deploymentName: util.HypershiftOperatorName,
		},
	}
)

func stringToExpectedArg(toAdd []string) []expectedArg {
	var result []expectedArg
	for s := range toAdd {
		result = append(result, expectedArg{shouldExist: true, argument: toAdd[s]})
	}
	return result
}

func (c *UpgradeController) getDeployment(operatorName string) (appsv1.Deployment, error) {
	deployment := &appsv1.Deployment{}
	nsn := types.NamespacedName{Namespace: util.HypershiftOperatorNamespace, Name: operatorName}
	err := c.spokeUncachedClient.Get(c.ctx, nsn, deployment)
	if err != nil {
		c.log.Error(err, fmt.Sprintf("failed to get %s deployment: ", operatorName))
		return *deployment, err
	}

	return *deployment, nil
}

// Match {text} and remove it
// Returns matched text e.g. --oidc-storage-provider-s3-bucket-name={bucket} will become "--oidc-storage-provider-s3-bucket-name=" and return "bucket"
func matchAndTrim(s *string) string {
	i := strings.Index(*s, "{")
	if i >= 0 {
		j := strings.Index(*s, "}")
		if j >= 0 {
			match := (*s)[i+1 : j]
			*s = (*s)[:len(*s)-(len(match)+2)]
			return match
		}
	}
	return ""
}

func getValueFromKey(secret corev1.Secret, key string) string {
	value := secret.Data[key]
	return string(value)
}