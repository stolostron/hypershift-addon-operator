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

func (c *UpgradeController) getDeployment(operatorName string) (appsv1.Deployment, error) {
	deployment := &appsv1.Deployment{}
	nsn := types.NamespacedName{Namespace: util.HypershiftOperatorNamespace, Name: operatorName}
	err := c.spokeUncachedClient.Get(c.ctx, nsn, deployment)
	if err != nil {
		c.log.Info(fmt.Sprintf("failed to get %s deployment", operatorName))
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

func argMismatch(args []expectedArg, deployArgs []string) bool {
	for _, a := range args {
		if argInList(a.argument, deployArgs) != a.shouldExist {
			return true
		}
	}
	return false
}

func argInList(arg string, list []string) bool {
	for _, a := range list {
		if strings.Contains(a, arg) {
			return true
		}
	}
	return false
}
