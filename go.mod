module github.com/stolostron/hypershift-addon-operator

go 1.23.6

toolchain go1.23.9

require (
	github.com/ghodss/yaml v1.0.1-0.20220118164431-d8423dcdf344
	github.com/go-logr/logr v1.4.2
	github.com/go-logr/zapr v1.3.0
	github.com/hashicorp/go-version v1.7.0
	github.com/onsi/ginkgo/v2 v2.22.2
	github.com/onsi/gomega v1.36.2
	github.com/openshift/api v0.0.0-20250327202946-4521905c611e
	github.com/openshift/hypershift v0.1.56
	github.com/openshift/hypershift/api v0.0.0-20250402075621-3283bb995a26
	github.com/openshift/library-go v0.0.0-20250203131244-80620876b7c2
	github.com/operator-framework/api v0.23.0
	github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring v0.76.0
	github.com/prometheus/client_golang v1.20.5
	github.com/prometheus/common v0.63.0
	github.com/rung/go-safecast v1.0.1
	github.com/spf13/cobra v1.8.1
	github.com/spf13/pflag v1.0.6
	github.com/stolostron/backplane-operator v0.0.0-20250227134641-0389b06daaff
	github.com/stolostron/discovery v0.0.0-20240429203137-9ccf847f7f5d
	github.com/stolostron/klusterlet-addon-controller v0.0.0-20250526040617-f0c4cfd51078
	github.com/stretchr/testify v1.10.0
	go.uber.org/zap v1.27.0
	go.withmatt.com/size v0.0.0-20221118222007-0d9da7819356
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.32.3
	k8s.io/apimachinery v0.32.3
	k8s.io/client-go v0.32.2
	k8s.io/component-base v0.32.2
	k8s.io/utils v0.0.0-20241210054802-24370beab758
	open-cluster-management.io/addon-framework v0.12.0
	open-cluster-management.io/api v0.16.0
	sigs.k8s.io/controller-runtime v0.19.6
)

require (
	cel.dev/expr v0.19.1 // indirect
	dario.cat/mergo v1.0.1 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.17.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.8.1 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.10.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5 v5.7.0 // indirect
	github.com/AzureAD/microsoft-authentication-library-for-go v1.3.2 // indirect
	github.com/BurntSushi/toml v1.4.0 // indirect
	github.com/Masterminds/goutils v1.1.1 // indirect
	github.com/Masterminds/semver/v3 v3.3.0 // indirect
	github.com/Masterminds/sprig/v3 v3.3.0 // indirect
	github.com/NYTimes/gziphandler v1.1.1 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/asaskevich/govalidator v0.0.0-20230301143203-a9d515a09cc2 // indirect
	github.com/aws/aws-sdk-go v1.55.6 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blang/semver v3.5.1+incompatible // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/coreos/go-semver v0.3.1 // indirect
	github.com/coreos/go-systemd/v22 v22.5.0 // indirect
	github.com/coreos/ignition/v2 v2.18.0 // indirect
	github.com/coreos/vcontext v0.0.0-20231102161604-685dc7299dc5 // indirect
	github.com/cyphar/filepath-securejoin v0.2.4 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/distribution/reference v0.6.0 // indirect
	github.com/docker/distribution v2.8.3+incompatible // indirect
	github.com/docker/go-metrics v0.0.1 // indirect
	github.com/docker/libtrust v0.0.0-20160708172513-aabc10ec26b7 // indirect
	github.com/emicklei/go-restful/v3 v3.12.1 // indirect
	github.com/evanphx/json-patch v5.9.0+incompatible // indirect
	github.com/evanphx/json-patch/v5 v5.9.11 // indirect
	github.com/fatih/structs v1.1.0 // indirect
	github.com/felixge/fgprof v0.9.4 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/fsnotify/fsnotify v1.8.0 // indirect
	github.com/fxamacker/cbor/v2 v2.7.0 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/jsonreference v0.21.0 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/go-task/slim-sprig/v3 v3.0.0 // indirect
	github.com/gobuffalo/flect v1.0.3 // indirect
	github.com/gobwas/glob v0.2.3 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang-jwt/jwt/v5 v5.2.2 // indirect
	github.com/golang/groupcache v0.0.0-20241129210726-2c02b8208cf8 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/btree v1.1.2 // indirect
	github.com/google/cel-go v0.22.0 // indirect
	github.com/google/gnostic-models v0.6.9-0.20230804172637-c7be7c783f49 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/pprof v0.0.0-20250317173921-a4b03ec1a45e // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gophercloud/gophercloud/v2 v2.4.0 // indirect
	github.com/gorilla/mux v1.8.1 // indirect
	github.com/grpc-ecosystem/go-grpc-prometheus v1.2.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.24.0 // indirect
	github.com/huandu/xstrings v1.5.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/k-orc/openstack-resource-controller v1.0.0 // indirect
	github.com/kubernetes-csi/external-snapshotter/client/v6 v6.3.0 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.0 // indirect
	github.com/openshift/client-go v0.0.0-20250330132942-bc2e3c2af6e1 // indirect
	github.com/openshift/cluster-api-provider-agent/api v0.0.0-20250324094320-633957e81ebd // indirect
	github.com/openshift/custom-resource-status v1.1.3-0.20220503160415-f2fdb4999d87 // indirect
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pkg/profile v1.7.0 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/prometheus/procfs v0.15.1 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/spf13/cast v1.7.0 // indirect
	github.com/stoewer/go-strcase v1.3.0 // indirect
	github.com/stolostron/cluster-lifecycle-api v0.0.0-20240813023109-42b5c115d0a3 // indirect
	github.com/vincent-petithory/dataurl v1.0.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/xeipuuv/gojsonpointer v0.0.0-20190905194746-02993c407bfb // indirect
	github.com/xeipuuv/gojsonreference v0.0.0-20180127040603-bd5ef7bd5415 // indirect
	github.com/xeipuuv/gojsonschema v1.2.0 // indirect
	go.etcd.io/etcd/api/v3 v3.5.19 // indirect
	go.etcd.io/etcd/client/pkg/v3 v3.5.19 // indirect
	go.etcd.io/etcd/client/v3 v3.5.19 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.54.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.54.0 // indirect
	go.opentelemetry.io/otel v1.34.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.33.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.33.0 // indirect
	go.opentelemetry.io/otel/metric v1.34.0 // indirect
	go.opentelemetry.io/otel/sdk v1.34.0 // indirect
	go.opentelemetry.io/otel/trace v1.34.0 // indirect
	go.opentelemetry.io/proto/otlp v1.4.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/crypto v0.36.0 // indirect
	golang.org/x/exp v0.0.0-20250210185358-939b2ce775ac // indirect
	golang.org/x/mod v0.23.0 // indirect
	golang.org/x/net v0.37.0 // indirect
	golang.org/x/oauth2 v0.28.0 // indirect
	golang.org/x/sync v0.12.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	golang.org/x/term v0.30.0 // indirect
	golang.org/x/text v0.23.0 // indirect
	golang.org/x/time v0.11.0 // indirect
	golang.org/x/tools v0.30.0 // indirect
	gomodules.xyz/jsonpatch/v2 v2.4.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20250106144421-5f5ef82da422 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250115164207-1a7da9e5054f // indirect
	google.golang.org/grpc v1.71.0 // indirect
	google.golang.org/protobuf v1.36.5 // indirect
	gopkg.in/evanphx/json-patch.v4 v4.12.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	helm.sh/helm/v3 v3.14.3 // indirect
	k8s.io/apiextensions-apiserver v0.32.2 // indirect
	k8s.io/apiserver v0.32.2 // indirect
	k8s.io/klog/v2 v2.130.1 // indirect
	k8s.io/kms v0.32.2 // indirect
	k8s.io/kube-aggregator v0.32.2 // indirect
	k8s.io/kube-openapi v0.0.0-20241105132330-32ad38e42d3f // indirect
	kubevirt.io/api v1.4.0 // indirect
	kubevirt.io/containerized-data-importer-api v1.61.1 // indirect
	kubevirt.io/controller-lifecycle-operator-sdk/api v0.0.0-20220329064328-f3cc58c6ed90 // indirect
	open-cluster-management.io/sdk-go v0.16.0 // indirect
	sigs.k8s.io/apiserver-network-proxy/konnectivity-client v0.31.0 // indirect
	sigs.k8s.io/cluster-api v1.9.5 // indirect
	sigs.k8s.io/cluster-api-provider-aws/v2 v2.7.1 // indirect
	sigs.k8s.io/cluster-api-provider-azure v1.18.0 // indirect
	sigs.k8s.io/cluster-api-provider-ibmcloud v0.7.0 // indirect
	sigs.k8s.io/cluster-api-provider-kubevirt v0.1.9 // indirect
	sigs.k8s.io/cluster-api-provider-openstack v0.12.1 // indirect
	sigs.k8s.io/json v0.0.0-20241014173422-cfa47c3a1cc8 // indirect
	sigs.k8s.io/kube-storage-version-migrator v0.0.6-0.20230721195810-5c8923c5ff96 // indirect
	sigs.k8s.io/secrets-store-csi-driver v1.4.8 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.6.0 // indirect
	sigs.k8s.io/yaml v1.4.0 // indirect
)

// Copied from hypershift
replace (
	cloud.google.com/go/compute => cloud.google.com/go v0.93.3
	github.com/prometheus/client_golang => github.com/prometheus/client_golang v1.18.0
)
