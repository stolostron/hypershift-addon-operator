package agent

import (
	"reflect"
	"testing"
	"time"

	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
)

var defaultReplicas *int32 = new(int32)
var multipleReplicas *int32 = new(int32)

func Test_containsHypershiftAddonDeployment(t *testing.T) {
	type args struct {
		deployment appsv1.Deployment
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "operator name",
			args: args{
				appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      util.HypershiftOperatorName,
						Namespace: util.HypershiftOperatorNamespace,
					},
				},
			},
			want: true,
		},
		{
			name: "external dns name",
			args: args{
				appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      util.HypershiftOperatorExternalDNSName,
						Namespace: util.HypershiftOperatorNamespace,
					},
				},
			},
			want: true,
		},
		{
			name: "not operator name",
			args: args{
				appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "not" + util.HypershiftOperatorName,
						Namespace: util.HypershiftOperatorNamespace,
					},
				},
			},
			want: false,
		},
		{
			name: "not external dns name",
			args: args{
				appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "not" + util.HypershiftOperatorExternalDNSName,
						Namespace: util.HypershiftOperatorNamespace,
					},
				},
			},
			want: false,
		},
		{
			name: "empty deployment name",
			args: args{
				appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "",
						Namespace: util.HypershiftOperatorNamespace,
					},
				},
			},
			want: false,
		},
		{
			name: "empty deployment namespace",
			args: args{
				appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      util.HypershiftOperatorName,
						Namespace: "",
					},
				},
			},
			want: false,
		},
		{
			name: "invalid deployment namespace",
			args: args{
				appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      util.HypershiftOperatorName,
						Namespace: "not" + util.HypershiftOperatorNamespace,
					},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsHypershiftAddonDeployment(tt.args.deployment); got != tt.want {
				t.Errorf("containsHypershiftAddonDeployment() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_checkDeployments(t *testing.T) {
	*defaultReplicas = 1
	*multipleReplicas = 3
	type args struct {
		checkExtDNSDeploy     bool
		operatorDeployment    *appsv1.Deployment
		externalDNSDeployment *appsv1.Deployment
	}
	tests := []struct {
		name string
		args args
		want metav1.Condition
	}{
		{
			name: "hypershift deployed with external dns",
			args: args{
				checkExtDNSDeploy: true,
				operatorDeployment: &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name: util.HypershiftOperatorName,
					},
					Spec: appsv1.DeploymentSpec{
						Replicas: defaultReplicas,
					},
					Status: appsv1.DeploymentStatus{
						AvailableReplicas: 1,
					},
				},
				externalDNSDeployment: &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name: util.HypershiftOperatorExternalDNSName,
					},
					Spec: appsv1.DeploymentSpec{
						Replicas: defaultReplicas,
					},
					Status: appsv1.DeploymentStatus{
						AvailableReplicas: 1,
					},
				},
			},
			want: metav1.Condition{
				Type:    addonv1alpha1.ManagedClusterAddOnConditionDegraded,
				Status:  metav1.ConditionFalse,
				Reason:  degradedReasonHypershiftDeployed,
				Message: degradedReasonHypershiftDeployedMessage,
			},
		},
		{
			name: "hypershift deployed without external dns",
			args: args{
				checkExtDNSDeploy: false,
				operatorDeployment: &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name: util.HypershiftOperatorName,
					},
					Spec: appsv1.DeploymentSpec{
						Replicas: defaultReplicas,
					},
					Status: appsv1.DeploymentStatus{
						AvailableReplicas: 1,
					},
				},
			},
			want: metav1.Condition{
				Type:    addonv1alpha1.ManagedClusterAddOnConditionDegraded,
				Status:  metav1.ConditionFalse,
				Reason:  degradedReasonHypershiftDeployed,
				Message: degradedReasonHypershiftDeployedMessage,
			},
		},
		{
			name: "no operator and no external dns deployments with check external dns",
			args: args{
				checkExtDNSDeploy: true,
			},
			want: metav1.Condition{
				Type:    addonv1alpha1.ManagedClusterAddOnConditionDegraded,
				Status:  metav1.ConditionTrue,
				Reason:  degradedReasonOperatorNotFound + "," + degradedReasonExternalDNSNotFound,
				Message: degradedReasonOperatorNotFoundMessage + "\n" + degradedReasonExternalDNSNotFoundMessage,
			},
		},
		{
			name: "no operator without check external dns",
			args: args{
				checkExtDNSDeploy: false,
			},
			want: metav1.Condition{
				Type:    addonv1alpha1.ManagedClusterAddOnConditionDegraded,
				Status:  metav1.ConditionTrue,
				Reason:  degradedReasonOperatorNotFound,
				Message: degradedReasonOperatorNotFoundMessage,
			},
		},
		{
			name: "deleted operator and deleted external dns deployments with check external dns",
			args: args{
				checkExtDNSDeploy: true,
				operatorDeployment: &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:              util.HypershiftOperatorName,
						DeletionTimestamp: &metav1.Time{Time: time.Now()},
					},
				},
				externalDNSDeployment: &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:              util.HypershiftOperatorExternalDNSName,
						DeletionTimestamp: &metav1.Time{Time: time.Now()},
					},
				},
			},
			want: metav1.Condition{
				Type:    addonv1alpha1.ManagedClusterAddOnConditionDegraded,
				Status:  metav1.ConditionTrue,
				Reason:  degradedReasonOperatorDeleted + "," + degradedReasonExternalDNSDeleted,
				Message: degradedReasonOperatorDeletedMessage + "\n" + degradedReasonExternalDNSDeletedMessage,
			},
		},
		{
			name: "deleted operator and deleted external dns deployments without check external dns",
			args: args{
				checkExtDNSDeploy: false,
				operatorDeployment: &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:              util.HypershiftOperatorName,
						DeletionTimestamp: &metav1.Time{Time: time.Now()},
					},
				},
				externalDNSDeployment: &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:              util.HypershiftOperatorExternalDNSName,
						DeletionTimestamp: &metav1.Time{Time: time.Now()},
					},
				},
			},
			want: metav1.Condition{
				Type:    addonv1alpha1.ManagedClusterAddOnConditionDegraded,
				Status:  metav1.ConditionTrue,
				Reason:  degradedReasonOperatorDeleted,
				Message: degradedReasonOperatorDeletedMessage,
			},
		},
		{
			name: "not all available operator and external dns deployments with check external dns",
			args: args{
				checkExtDNSDeploy: true,
				operatorDeployment: &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name: util.HypershiftOperatorName,
					},
					Spec: appsv1.DeploymentSpec{
						Replicas: defaultReplicas,
					},
					Status: appsv1.DeploymentStatus{
						AvailableReplicas: 0,
					},
				},
				externalDNSDeployment: &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name: util.HypershiftOperatorExternalDNSName,
					},
					Spec: appsv1.DeploymentSpec{
						Replicas: multipleReplicas,
					},
					Status: appsv1.DeploymentStatus{
						AvailableReplicas: 2,
					},
				},
			},
			want: metav1.Condition{
				Type:    addonv1alpha1.ManagedClusterAddOnConditionDegraded,
				Status:  metav1.ConditionTrue,
				Reason:  degradedReasonOperatorNotAllAvailableReplicas + "," + degradedReasonExternalDNSNotAllAvailableReplicas,
				Message: degradedReasonOperatorNotAllAvailableReplicasMessage + "\n" + degradedReasonExternalDNSNotAllAvailableReplicasMessage,
			},
		},
		{
			name: "not all available operator and external dns deployments without check external dns",
			args: args{
				checkExtDNSDeploy: false,
				operatorDeployment: &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name: util.HypershiftOperatorName,
					},
					Spec: appsv1.DeploymentSpec{
						Replicas: defaultReplicas,
					},
					Status: appsv1.DeploymentStatus{
						AvailableReplicas: 0,
					},
				},
				externalDNSDeployment: &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name: util.HypershiftOperatorExternalDNSName,
					},
					Spec: appsv1.DeploymentSpec{
						Replicas: multipleReplicas,
					},
					Status: appsv1.DeploymentStatus{
						AvailableReplicas: 2,
					},
				},
			},
			want: metav1.Condition{
				Type:    addonv1alpha1.ManagedClusterAddOnConditionDegraded,
				Status:  metav1.ConditionTrue,
				Reason:  degradedReasonOperatorNotAllAvailableReplicas,
				Message: degradedReasonOperatorNotAllAvailableReplicasMessage,
			},
		},
		{
			name: "available operator but not external dns deployments with check external dns",
			args: args{
				checkExtDNSDeploy: true,
				operatorDeployment: &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name: util.HypershiftOperatorName,
					},
					Spec: appsv1.DeploymentSpec{
						Replicas: defaultReplicas,
					},
					Status: appsv1.DeploymentStatus{
						AvailableReplicas: 1,
					},
				},
				externalDNSDeployment: &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name: util.HypershiftOperatorExternalDNSName,
					},
					Spec: appsv1.DeploymentSpec{
						Replicas: multipleReplicas,
					},
					Status: appsv1.DeploymentStatus{
						AvailableReplicas: 2,
					},
				},
			},
			want: metav1.Condition{
				Type:    addonv1alpha1.ManagedClusterAddOnConditionDegraded,
				Status:  metav1.ConditionTrue,
				Reason:  degradedReasonExternalDNSNotAllAvailableReplicas,
				Message: degradedReasonExternalDNSNotAllAvailableReplicasMessage,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkDeployments(tt.args.checkExtDNSDeploy, tt.args.operatorDeployment, tt.args.externalDNSDeployment); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("checkDeployments() = %v, want %v", got, tt.want)
			}
		})
	}
}
