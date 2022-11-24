package manager

import (
	"testing"

	"github.com/go-logr/zapr"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/runtime"
	fakeaddon "open-cluster-management.io/api/client/addon/clientset/versioned/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	nodeSelector = map[string]string{"kubernetes.io/os": "linux"}
	tolerations  = []corev1.Toleration{{Key: "foo", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute}}
)

func Test_getAgentAddon(t *testing.T) {
	controllerContext := &controllercmd.ControllerContext{}
	componentName := "manager"

	configs := []runtime.Object{}
	fakeAddonClient := fakeaddon.NewSimpleClientset(configs...)
	client := initClient()
	zapLog, _ := zap.NewDevelopment()
	o := &override{
		Client:            client,
		log:               zapr.NewLogger(zapLog),
		operatorNamespace: controllerContext.OperatorNamespace,
		withOverride:      false,
	}

	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "Get agent addon",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getAgentAddon(componentName, o, controllerContext, fakeAddonClient)
			if (err != nil) != tt.wantErr {
				t.Errorf("getAgentAddon() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			assert.NotNil(t, got, "agent addon is not nil")
		})
	}
}

func initClient() client.Client {
	scheme := runtime.NewScheme()
	//corev1.AddToScheme(scheme)

	ncb := fake.NewClientBuilder()
	ncb.WithScheme(scheme)
	return ncb.Build()

}
