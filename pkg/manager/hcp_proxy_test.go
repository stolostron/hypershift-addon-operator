package manager

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	tlspkg "github.com/openshift/controller-runtime-common/pkg/tls"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	mcev1 "github.com/stolostron/backplane-operator/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// recordingLogSink captures errors passed to log.Error for assertions.
type recordingLogSink struct {
	errors []error
}

func (s *recordingLogSink) Init(logr.RuntimeInfo)                            {}
func (s *recordingLogSink) Enabled(level int) bool                           { return true }
func (s *recordingLogSink) Info(level int, msg string, keysAndValues ...any) {}
func (s *recordingLogSink) Error(err error, msg string, keysAndValues ...any) {
	s.errors = append(s.errors, err)
}
func (s *recordingLogSink) WithValues(keysAndValues ...any) logr.LogSink { return s }
func (s *recordingLogSink) WithName(name string) logr.LogSink            { return s }

func Test_logSpokeHTTPFailure_DoesNotLogSpokeErrorText(t *testing.T) {
	sink := &recordingLogSink{}
	p := &hcpProxy{log: logr.New(sink)}
	p.logSpokeHTTPFailure("failed to create extra object", "object", "Role/foo", "spoke", "spoke-1")
	require.Len(t, sink.errors, 1)
	assert.Equal(t, errSpokeLogged, sink.errors[0])
	assert.NotContains(t, sink.errors[0].Error(), "user@evil.com")
}

// setCertPaths overrides the package-level cert/key file paths for testing.
func setCertPaths(cert, key string) {
	certFilePath = cert
	keyFilePath = key
}

// tlsCertToPEM re-encodes a tls.Certificate back to PEM bytes.
// The first Certificate block is the leaf; the second (if present) is the CA.
func tlsCertToPEM(c tls.Certificate) (certPEM, keyPEM []byte, err error) {
	for _, derBlock := range c.Certificate {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBlock})...)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(c.PrivateKey)
	if err != nil {
		return nil, nil, err
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// newTestRESTMapper returns a discovery-backed mapper with the API groups used in
// extra-object unit tests (core, RBAC, networking, HyperShift).
func newTestRESTMapper() meta.RESTMapper {
	groups := []*restmapper.APIGroupResources{
		{
			Group: metav1.APIGroup{
				Name:             "",
				Versions:         []metav1.GroupVersionForDiscovery{{Version: "v1"}},
				PreferredVersion: metav1.GroupVersionForDiscovery{Version: "v1"},
			},
			VersionedResources: map[string][]metav1.APIResource{
				"v1": {
					{Name: "configmaps", Namespaced: true, Kind: "ConfigMap"},
					{Name: "secrets", Namespaced: true, Kind: "Secret"},
					{Name: "namespaces", Namespaced: false, Kind: "Namespace"},
				},
			},
		},
		{
			Group: metav1.APIGroup{
				Name:             "rbac.authorization.k8s.io",
				Versions:         []metav1.GroupVersionForDiscovery{{Version: "v1"}},
				PreferredVersion: metav1.GroupVersionForDiscovery{Version: "v1"},
			},
			VersionedResources: map[string][]metav1.APIResource{
				"v1": {
					{Name: "roles", Namespaced: true, Kind: "Role"},
				},
			},
		},
		{
			Group: metav1.APIGroup{
				Name:             "networking.k8s.io",
				Versions:         []metav1.GroupVersionForDiscovery{{Version: "v1"}},
				PreferredVersion: metav1.GroupVersionForDiscovery{Version: "v1"},
			},
			VersionedResources: map[string][]metav1.APIResource{
				"v1": {
					{Name: "networkpolicies", Namespaced: true, Kind: "NetworkPolicy"},
				},
			},
		},
		{
			Group: metav1.APIGroup{
				Name:             hypershiftv1beta1.GroupVersion.Group,
				Versions:         []metav1.GroupVersionForDiscovery{{Version: hypershiftv1beta1.GroupVersion.Version}},
				PreferredVersion: metav1.GroupVersionForDiscovery{Version: hypershiftv1beta1.GroupVersion.Version},
			},
			VersionedResources: map[string][]metav1.APIResource{
				hypershiftv1beta1.GroupVersion.Version: {
					{Name: "hostedclusters", Namespaced: true, Kind: "HostedCluster"},
					{Name: "nodepools", Namespaced: true, Kind: "NodePool"},
				},
			},
		},
	}
	return restmapper.NewDiscoveryRESTMapper(groups)
}

// newTestProxy creates a minimal hcpProxy wired to the provided fake client.
func newTestProxy(t *testing.T, objs ...runtime.Object) *hcpProxy {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, clusterv1.AddToScheme(scheme))
	require.NoError(t, mcev1.AddToScheme(scheme))
	require.NoError(t, hypershiftv1beta1.AddToScheme(scheme))

	builder := fake.NewClientBuilder().WithScheme(scheme)
	for _, obj := range objs {
		builder = builder.WithRuntimeObjects(obj)
	}
	hubClient := builder.Build()

	// Use Intermediate TLS profile defaults (TLS 1.2+) in unit tests —
	// no cluster APIServer available to fetch the real profile.
	defaultProfile, _ := tlspkg.GetTLSProfileSpec(nil)

	cfg := &rest.Config{}
	hubDynClient, err := dynamic.NewForConfig(cfg)
	require.NoError(t, err)

	zapLog, _ := zap.NewDevelopment()
	return &hcpProxy{
		hubConfig:         cfg,
		hubClient:         hubClient,
		hubDynClient:      hubDynClient,
		restMapper:        newTestRESTMapper(),
		operatorNamespace: "multicluster-engine",
		profileSpec:       defaultProfile,
		log:               zapr.NewLogger(zapLog),
	}
}

// --- generateSelfSignedCert ---

func Test_generateSelfSignedCert_WhenCalled_ItShouldReturnValidCertificate(t *testing.T) {
	cert, err := generateSelfSignedCert("multicluster-engine")
	require.NoError(t, err)
	require.NotEmpty(t, cert.Certificate)

	// cert.Certificate[0] is the leaf (server) cert; [1] is the signing CA.
	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	require.NoError(t, err)

	// library-go sets CommonName to the first sorted hostname (alphabetically "127.0.0.1").
	// What matters for TLS is the SAN extension, not the CN — assert on SANs.
	assert.Contains(t, x509Cert.DNSNames, hcpProxyServiceName)
	assert.Contains(t, x509Cert.DNSNames, hcpProxyServiceName+".multicluster-engine.svc")
	// 127.0.0.1 is split into IPAddresses by library-go's IPAddressesDNSNames helper.
	require.Len(t, x509Cert.IPAddresses, 1)
	assert.Equal(t, "127.0.0.1", x509Cert.IPAddresses[0].String())
}

func Test_generateSelfSignedCert_WhenNamespaceVaries_ItShouldIncludeCorrectSANs(t *testing.T) {
	cert, err := generateSelfSignedCert("custom-ns")
	require.NoError(t, err)

	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	require.NoError(t, err)
	assert.Contains(t, x509Cert.DNSNames, hcpProxyServiceName+".custom-ns.svc")
	assert.Contains(t, x509Cert.DNSNames, hcpProxyServiceName+".custom-ns.svc.cluster.local")
}

// --- loadOrGenerateCert ---

func Test_loadOrGenerateCert_WhenServiceCACertFilePresent_ItShouldLoadFromFile(t *testing.T) {
	// Generate a real cert to write to disk so tls.LoadX509KeyPair succeeds.
	generated, err := generateSelfSignedCert("multicluster-engine")
	require.NoError(t, err)

	// Write the PEM files to a temp directory that mimics the mounted Secret.
	dir := t.TempDir()
	certFile := filepath.Join(dir, "tls.crt")
	keyFile := filepath.Join(dir, "tls.key")

	certPEM, keyPEM, err := tlsCertToPEM(generated)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(certFile, certPEM, 0600))
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0600))

	// Point the package-level path variables at the temp dir for this test.
	origCert, origKey := certFilePath, keyFilePath
	setCertPaths(certFile, keyFile)
	t.Cleanup(func() { setCertPaths(origCert, origKey) })

	zapLog, _ := zap.NewDevelopment()
	log := zapr.NewLogger(zapLog)

	cert, err := loadOrGenerateCert("multicluster-engine", log)
	require.NoError(t, err)
	require.NotEmpty(t, cert.Certificate, "expected cert loaded from file")
}

func Test_loadOrGenerateCert_WhenServiceCACertFileAbsent_ItShouldGenerateFallback(t *testing.T) {
	origCert, origKey := certFilePath, keyFilePath
	setCertPaths("/nonexistent/tls.crt", "/nonexistent/tls.key")
	t.Cleanup(func() { setCertPaths(origCert, origKey) })

	zapLog, _ := zap.NewDevelopment()
	log := zapr.NewLogger(zapLog)

	cert, err := loadOrGenerateCert("multicluster-engine", log)
	require.NoError(t, err)
	require.NotEmpty(t, cert.Certificate, "expected fallback self-signed cert")
}

// --- certCache ---

func Test_certCache_WhenConcurrentHandshakes_ItShouldReturnSameFallbackCert(t *testing.T) {
	origCert, origKey := certFilePath, keyFilePath
	setCertPaths("/nonexistent/tls.crt", "/nonexistent/tls.key")
	t.Cleanup(func() { setCertPaths(origCert, origKey) })

	zapLog, _ := zap.NewDevelopment()
	log := zapr.NewLogger(zapLog)
	cache := &certCache{operatorNS: "multicluster-engine", log: log}

	const goroutines = 20
	results := make([]*tls.Certificate, goroutines)
	errs := make([]error, goroutines)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = cache.getCertificate()
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "goroutine %d returned error", i)
	}
	// All goroutines must receive the exact same certificate pointer.
	for i := 1; i < goroutines; i++ {
		assert.Same(t, results[0], results[i],
			"goroutine %d got a different cert pointer — fallback was generated more than once", i)
	}
}

func Test_certCache_WhenServiceCACertAppears_ItShouldSwitchFromFallback(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "tls.crt")
	keyFile := filepath.Join(dir, "tls.key")

	// Start with no cert file → expect fallback.
	origCert, origKey := certFilePath, keyFilePath
	setCertPaths(certFile, keyFile)
	t.Cleanup(func() { setCertPaths(origCert, origKey) })

	zapLog, _ := zap.NewDevelopment()
	log := zapr.NewLogger(zapLog)
	cache := &certCache{operatorNS: "multicluster-engine", log: log}

	// First call: file absent → self-signed fallback.
	fallback, err := cache.getCertificate()
	require.NoError(t, err)
	require.NotNil(t, fallback)

	// Parse the fallback cert to verify it's self-signed.
	fallbackLeaf, err := x509.ParseCertificate(fallback.Certificate[0])
	require.NoError(t, err)
	assert.Contains(t, fallbackLeaf.Issuer.CommonName, "hcp-proxy",
		"fallback cert should be issued by the self-signed CA")

	// Now write a service-ca cert to disk (simulating kubelet projection).
	serviceCA, err := generateSelfSignedCert("multicluster-engine")
	require.NoError(t, err)
	certPEM, keyPEM, err := tlsCertToPEM(serviceCA)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(certFile, certPEM, 0600))
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0600))

	// Next call: file present → should switch to service-ca cert.
	switched, err := cache.getCertificate()
	require.NoError(t, err)
	require.NotNil(t, switched)
	assert.NotSame(t, fallback, switched,
		"cert pointer should change after service-ca cert is projected")

	// The switched cert's raw bytes should match what we wrote to disk.
	assert.Equal(t, serviceCA.Certificate[0], switched.Certificate[0],
		"switched cert should match the service-ca cert on disk")
}

// --- whoIsTheCaller ---

func Test_whoIsTheCaller_WhenHeadersPresent_ItShouldReturnUsernameAndGroups(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Remote-User", "alice")
	r.Header.Add("X-Remote-Group", "system:authenticated,developers")

	username, groups := whoIsTheCaller(r)
	assert.Equal(t, "alice", username)
	assert.Contains(t, groups, "system:authenticated")
	assert.Contains(t, groups, "developers")
}

func Test_whoIsTheCaller_WhenHeadersAbsent_ItShouldReturnEmpty(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	username, groups := whoIsTheCaller(r)
	assert.Empty(t, username)
	assert.Empty(t, groups)
}

// --- checkSpokeHealth ---

func Test_checkSpokeHealth_WhenClusterNotFound_ItShouldReturnError(t *testing.T) {
	p := newTestProxy(t)
	err := p.checkSpokeHealth(context.Background(), "missing-spoke")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func Test_checkSpokeHealth_WhenClusterNotAvailable_ItShouldReturnError(t *testing.T) {
	mc := &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "spoke-1"},
		Status: clusterv1.ManagedClusterStatus{
			Conditions: []metav1.Condition{
				{
					Type:    clusterv1.ManagedClusterConditionAvailable,
					Status:  metav1.ConditionFalse,
					Message: "cluster unreachable",
				},
			},
		},
	}
	p := newTestProxy(t, mc)
	err := p.checkSpokeHealth(context.Background(), "spoke-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not available")
}

func Test_checkSpokeHealth_WhenClusterAvailable_ItShouldReturnNil(t *testing.T) {
	mc := &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "spoke-1"},
		Status: clusterv1.ManagedClusterStatus{
			Conditions: []metav1.Condition{
				{
					Type:   clusterv1.ManagedClusterConditionAvailable,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
	p := newTestProxy(t, mc)
	err := p.checkSpokeHealth(context.Background(), "spoke-1")
	assert.NoError(t, err)
}

func Test_checkSpokeHealth_WhenNoConditions_ItShouldReturnAvailabilityUnknown(t *testing.T) {
	mc := &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "spoke-1"},
	}
	p := newTestProxy(t, mc)
	err := p.checkSpokeHealth(context.Background(), "spoke-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "availability unknown")
}

// --- resolveOperatorNamespace ---

func Test_resolveOperatorNamespace_WhenNoMCE_ItShouldReturnDefault(t *testing.T) {
	p := newTestProxy(t)
	ns := resolveOperatorNamespace(context.Background(), p.hubClient, p.log)
	assert.Equal(t, "multicluster-engine", ns)
}

func Test_resolveOperatorNamespace_WhenMCEHasTargetNamespace_ItShouldReturnIt(t *testing.T) {
	mce := &mcev1.MultiClusterEngine{
		ObjectMeta: metav1.ObjectMeta{Name: "multiclusterengine"},
		Spec:       mcev1.MultiClusterEngineSpec{TargetNamespace: "my-mce-ns"},
	}
	p := newTestProxy(t, mce)
	ns := resolveOperatorNamespace(context.Background(), p.hubClient, p.log)
	assert.Equal(t, "my-mce-ns", ns)
}

// --- service URL discovery ---

func Test_resolveClusterProxyURL_WhenEnvSet_ItShouldUseEnv(t *testing.T) {
	t.Setenv("CLUSTER_PROXY_URL", "https://localhost:9092")
	p := newTestProxy(t)
	url := resolveClusterProxyURL(context.Background(), p.hubClient, "multicluster-engine", p.log)
	assert.Equal(t, "https://localhost:9092", url)
}

func Test_resolveClusterProxyURL_WhenNoRoute_ItShouldUsePodNamespaceServiceURL(t *testing.T) {
	t.Setenv("CLUSTER_PROXY_URL", "")
	p := newTestProxy(t)
	url := resolveClusterProxyURL(context.Background(), p.hubClient, "my-mce-ns", p.log)
	assert.Equal(t, "https://cluster-proxy-addon-user.my-mce-ns.svc:9092", url)
}

func Test_clusterProxyNamespace_WhenOperatorNSEmpty_ItShouldUsePOD_NAMESPACE(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "multicluster-engine")
	assert.Equal(t, "multicluster-engine", clusterProxyNamespace(""))
}

// --- handleHealthz ---

func Test_handleHealthz_WhenCalled_ItShouldReturn200(t *testing.T) {
	p := newTestProxy(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	p.handleHealthz(w, r)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "ok", w.Body.String())
}

// --- handleDiscovery ---

func Test_handleDiscovery_WhenGroupPath_ItShouldReturnAPIGroup(t *testing.T) {
	p := newTestProxy(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/apis/"+hcpProxyAPIGroup, nil)
	p.handleDiscovery(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var doc map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	assert.Equal(t, "APIGroup", doc["kind"])
	assert.Equal(t, hcpProxyAPIGroup, doc["name"])
}

func Test_handleDiscovery_WhenVersionPath_ItShouldReturnAPIResourceList(t *testing.T) {
	p := newTestProxy(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/apis/"+hcpProxyAPIGroup+"/"+hcpProxyAPIVersion, nil)
	p.handleDiscovery(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var doc map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	assert.Equal(t, "APIResourceList", doc["kind"])
	resources := doc["resources"].([]interface{})
	// hostedclusters + hostedclusters/resources + hostedclusters/finalizers + hostedclusters/validate
	assert.Len(t, resources, 4)
	first := resources[0].(map[string]interface{})
	assert.Equal(t, hcpProxyResource, first["name"])
	verbs := first["verbs"].([]interface{})
	assert.Contains(t, verbs, "list")
	assert.Contains(t, verbs, "deletecollection")
	second := resources[1].(map[string]interface{})
	assert.Equal(t, hcpProxyResource+"/resources", second["name"])
	third := resources[2].(map[string]interface{})
	assert.Equal(t, hcpProxyResource+"/"+finalizersSubresource, third["name"])
	assert.Equal(t, []interface{}{"patch"}, third["verbs"])
	fourth := resources[3].(map[string]interface{})
	assert.Equal(t, hcpProxyResource+"/"+validateSubresource, fourth["name"])
	assert.Equal(t, []interface{}{"create"}, fourth["verbs"])
}

// --- handleRoute ---

func Test_handleRoute_WhenWatchRequested_ItShouldReturn405(t *testing.T) {
	p := newTestProxy(t)
	w := httptest.NewRecorder()
	path := "/apis/" + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion + "/namespaces/clusters/hostedclusters?watch=true"
	r := httptest.NewRequest(http.MethodGet, path, nil)
	p.handleRoute(w, r)
	assertStatusError(t, w, http.StatusMethodNotAllowed, "watch is not supported")
}

func Test_handleRoute_WhenMissingHostingCluster_OnNamedEndpoint_ItShouldReturn400(t *testing.T) {
	p := newTestProxy(t)
	w := httptest.NewRecorder()
	path := "/apis/" + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion + "/namespaces/clusters/hostedclusters/my-hc"
	r := httptest.NewRequest(http.MethodGet, path, nil) // no ?hostingCluster
	p.handleRoute(w, r)
	assertStatusError(t, w, http.StatusBadRequest, "hostingCluster query parameter is required")
}

func Test_handleRoute_WhenMissingHostingCluster_OnCollectionGET_ItShouldReturnEmptyList(t *testing.T) {
	p := newTestProxy(t)
	w := httptest.NewRecorder()
	path := "/apis/" + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion + "/namespaces/clusters/hostedclusters"
	r := httptest.NewRequest(http.MethodGet, path, nil) // no ?hostingCluster
	p.handleRoute(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var doc map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	assert.Equal(t, "HostedClusterList", doc["kind"])
	items := doc["items"].([]interface{})
	assert.Empty(t, items)
}

func Test_handleRoute_WhenMissingHostingCluster_OnCollectionDELETE_ItShouldReturnEmptyList(t *testing.T) {
	p := newTestProxy(t)
	w := httptest.NewRecorder()
	path := "/apis/" + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion + "/namespaces/clusters/hostedclusters"
	r := httptest.NewRequest(http.MethodDelete, path, nil) // no ?hostingCluster
	p.handleRoute(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var doc map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	assert.Equal(t, "HostedClusterList", doc["kind"])
	items := doc["items"].([]interface{})
	assert.Empty(t, items)
}

func Test_handleRoute_WhenMissingHostingCluster_OnCollectionPOST_ItShouldReturn400(t *testing.T) {
	p := newTestProxy(t)
	w := httptest.NewRecorder()
	path := "/apis/" + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion + "/namespaces/clusters/hostedclusters"
	r := httptest.NewRequest(http.MethodPost, path, nil) // no ?hostingCluster
	p.handleRoute(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func Test_handleRoute_WhenClusterWideGET_WithoutHostingCluster_ItShouldReturnEmptyList(t *testing.T) {
	p := newTestProxy(t)
	w := httptest.NewRecorder()
	path := "/apis/" + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion + "/hostedclusters"
	r := httptest.NewRequest(http.MethodGet, path, nil) // oc get hostedclusters -A
	p.handleRoute(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var doc map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	assert.Equal(t, "HostedClusterList", doc["kind"])
	items := doc["items"].([]interface{})
	assert.Empty(t, items)
}

func Test_handleRoute_WhenInvalidHostingCluster_OnCollection_ItShouldReturn400(t *testing.T) {
	p := newTestProxy(t)
	w := httptest.NewRecorder()
	path := "/apis/" + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion +
		"/namespaces/clusters/hostedclusters?hostingCluster=../evil"
	r := httptest.NewRequest(http.MethodDelete, path, nil)
	p.handleRoute(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func Test_handleRoute_WhenSpokeNotAvailable_ItShouldReturn503(t *testing.T) {
	// Spoke exists but is not available
	mc := &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "spoke-1"},
		Status: clusterv1.ManagedClusterStatus{
			Conditions: []metav1.Condition{
				{Type: clusterv1.ManagedClusterConditionAvailable, Status: metav1.ConditionFalse},
			},
		},
	}
	p := newTestProxy(t, mc)
	w := httptest.NewRecorder()
	path := apiPathPrefix + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion +
		"/namespaces/clusters/hostedclusters?hostingCluster=spoke-1"
	r := httptest.NewRequest(http.MethodGet, path, nil)
	p.handleRoute(w, r)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func Test_handleRoute_WhenUnauthenticated_ItShouldReturn403(t *testing.T) {
	mc := availableManagedCluster("spoke-1")
	p := newTestProxy(t, mc)
	w := httptest.NewRecorder()
	path := apiPathPrefix + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion +
		"/namespaces/clusters/hostedclusters?hostingCluster=spoke-1"
	// No X-Remote-User header → unauthenticated
	r := httptest.NewRequest(http.MethodGet, path, nil)
	p.handleRoute(w, r)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// --- checkHubPermission ---

// newTestProxyWithHubServer creates an hcpProxy whose hubConfig points at the
// provided mock hub server URL so checkHubPermission's dynamic client hits it.
func newTestProxyWithHubServer(t *testing.T, hubServerURL string, objs ...runtime.Object) *hcpProxy {
	t.Helper()
	p := newTestProxy(t, objs...)
	p.hubConfig = &rest.Config{
		Host:            hubServerURL,
		TLSClientConfig: rest.TLSClientConfig{Insecure: true},
	}
	var err error
	p.hubDynClient, err = dynamic.NewForConfig(p.hubConfig)
	require.NoError(t, err)
	return p
}

func Test_checkHubPermission_WhenAdminUserPermissionContainsCluster_ItShouldAllow(t *testing.T) {
	// Mock hub returns "managedcluster:admin" UserPermission that lists spoke-1
	adminUP := map[string]interface{}{
		"apiVersion": "clusterview.open-cluster-management.io/v1alpha1",
		"kind":       "UserPermission",
		"metadata":   map[string]interface{}{"name": "managedcluster:admin"},
		"status": map[string]interface{}{
			"bindings": []interface{}{
				map[string]interface{}{"cluster": "spoke-1"},
				map[string]interface{}{"cluster": "spoke-2"},
			},
		},
	}
	hubSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "userpermissions/managedcluster:admin") {
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(adminUP)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer hubSrv.Close()

	p := newTestProxyWithHubServer(t, hubSrv.URL)
	err := p.checkHubPermission(context.Background(), "alice", []string{"dev"}, "spoke-1")
	assert.NoError(t, err)
}

func Test_checkHubPermission_WhenClusterNotInAdminBindings_ItShouldReturn403(t *testing.T) {
	// spoke-3 is NOT in the bindings — alice only has admin on spoke-1.
	// apiVersion + kind are required so the dynamic client codec can decode the response.
	adminUP := map[string]interface{}{
		"apiVersion": "clusterview.open-cluster-management.io/v1alpha1",
		"kind":       "UserPermission",
		"metadata":   map[string]interface{}{"name": "managedcluster:admin"},
		"status": map[string]interface{}{
			"bindings": []interface{}{
				map[string]interface{}{"cluster": "spoke-1"},
			},
		},
	}
	hubSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "userpermissions/managedcluster:admin") {
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(adminUP)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer hubSrv.Close()

	p := newTestProxyWithHubServer(t, hubSrv.URL)
	err := p.checkHubPermission(context.Background(), "alice", nil, "spoke-3")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not have admin access")
}

func Test_checkHubPermission_WhenViewOnlyUser_ItShouldReturnError(t *testing.T) {
	// The operator probe (step 1) returns the admin UserPermission → API is present.
	// The impersonated GET (step 2) returns 404 → user has no admin access → hard deny.
	adminUP := map[string]interface{}{
		"apiVersion": "clusterview.open-cluster-management.io/v1alpha1",
		"kind":       "UserPermission",
		"metadata":   map[string]interface{}{"name": "managedcluster:admin"},
		"status": map[string]interface{}{
			"bindings": []interface{}{
				map[string]interface{}{"cluster": "spoke-1"},
			},
		},
	}
	hubSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Operator probe (no Impersonate header) succeeds.
		// Impersonated call (Impersonate-User header present) returns 404.
		if r.Header.Get("Impersonate-User") != "" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","reason":"NotFound",`+
				`"message":"userpermissions.clusterview.open-cluster-management.io `+
				`\"managedcluster:admin\" not found"}`)
			return
		}
		if strings.Contains(r.URL.Path, "userpermissions/managedcluster:admin") {
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(adminUP)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer hubSrv.Close()

	p := newTestProxyWithHubServer(t, hubSrv.URL)
	err := p.checkHubPermission(context.Background(), "viewer", nil, "spoke-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not have admin access")
}

func Test_checkHubPermission_WhenClusterviewAPIAbsent_WithEnvVar_ItShouldSkipAndAllow(t *testing.T) {
	// Simulates a kind/non-ACM hub with SKIP_HUB_PERMISSION_CHECK=true:
	// the API group is not installed and the env var allows skipping.
	t.Setenv("SKIP_HUB_PERMISSION_CHECK", "true")

	hubSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerContentType, contentTypeJSON)
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","reason":"NotFound",`+
			`"message":"the server could not find the requested resource"}`)
	}))
	defer hubSrv.Close()

	p := newTestProxyWithHubServer(t, hubSrv.URL)
	err := p.checkHubPermission(context.Background(), "anyuser", nil, "spoke-1")
	assert.NoError(t, err)
}

func Test_checkHubPermission_WhenClusterviewAPIAbsent_WithoutEnvVar_ItShouldFailClosed(t *testing.T) {
	// Simulates a partial MCE install failure where the API is absent but
	// SKIP_HUB_PERMISSION_CHECK is not set — should fail closed for security.
	t.Setenv("SKIP_HUB_PERMISSION_CHECK", "")

	hubSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerContentType, contentTypeJSON)
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","reason":"NotFound",`+
			`"message":"the server could not find the requested resource"}`)
	}))
	defer hubSrv.Close()

	p := newTestProxyWithHubServer(t, hubSrv.URL)
	err := p.checkHubPermission(context.Background(), "anyuser", nil, "spoke-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "UserPermission is required in production")
}

func Test_checkHubPermission_WhenProbeReturnsResourceNotFound_ItShouldProceedToStep2(t *testing.T) {
	// Simulates a real ACM hub where the clusterview API exists but the operator SA
	// has no managedcluster:admin bindings. The probe (step 1) returns a proper resource-level
	// 404 — not the API-absent message. The code should proceed to step 2 (impersonated check).
	adminUP := map[string]interface{}{
		"apiVersion": "clusterview.open-cluster-management.io/v1alpha1",
		"kind":       "UserPermission",
		"metadata":   map[string]interface{}{"name": "managedcluster:admin"},
		"status": map[string]interface{}{
			"bindings": []interface{}{
				map[string]interface{}{"cluster": "spoke-1"},
			},
		},
	}
	hubSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Operator probe (no Impersonate header): resource-level 404 with proper Status
		if r.Header.Get("Impersonate-User") == "" {
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Failure",`+
				`"message":"userpermissions.clusterview.open-cluster-management.io \"managedcluster:admin\" not found",`+
				`"reason":"NotFound","code":404}`)
			return
		}
		// Impersonated call (step 2): return admin bindings
		if strings.Contains(r.URL.Path, "userpermissions/managedcluster:admin") {
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(adminUP)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer hubSrv.Close()

	p := newTestProxyWithHubServer(t, hubSrv.URL)
	err := p.checkHubPermission(context.Background(), "alice", []string{"dev"}, "spoke-1")
	assert.NoError(t, err)
}

func Test_checkHubPermission_WhenProbeErrors_ItShouldFailClosed(t *testing.T) {
	// Transient hub failures must not bypass authorization.
	hubSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerContentType, contentTypeJSON)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Failure",`+
			`"message":"service unavailable","reason":"ServiceUnavailable","code":503}`)
	}))
	defer hubSrv.Close()

	p := newTestProxyWithHubServer(t, hubSrv.URL)
	err := p.checkHubPermission(context.Background(), "anyuser", nil, "spoke-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "clusterview permission probe failed")
}

func Test_checkHubPermission_WhenUsernameEmpty_ItShouldReturn403(t *testing.T) {
	p := newTestProxy(t)
	err := p.checkHubPermission(context.Background(), "", nil, "spoke-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unauthenticated")
}

// --- handleCreate (spoke mocked via httptest.Server) ---
// Request body mirrors `hcp create cluster --render` output.

func Test_handleCreate_WhenHostedClusterMissing_ItShouldReturn400(t *testing.T) {
	mc := availableManagedCluster("spoke-1")
	p := newTestProxy(t, mc)

	// Empty body — no hostedCluster field
	body, _ := json.Marshal(CreateRequest{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleCreate(w, r, "clusters", "spoke-1")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func Test_handleCreate_WhenSpokeAccepts_ItShouldReturn201(t *testing.T) {
	spokeSrv := httptest.NewServer(spokeCreatePreflightHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerContentType, contentTypeJSON)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer spokeSrv.Close()

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	// Mirrors hcp create cluster --render: HostedCluster references the secret by name,
	// and the Secret object is passed in the Secrets list.
	hc := &hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "clusters"},
		Spec: hypershiftv1beta1.HostedClusterSpec{
			InfraID:    "my-hc",
			PullSecret: corev1.LocalObjectReference{Name: "my-hc-pull-secret"},
		},
	}
	body, _ := json.Marshal(CreateRequest{
		HostedCluster: hc,
		Secrets: []corev1.Secret{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "my-hc-pull-secret"},
				Data:       map[string][]byte{".dockerconfigjson": []byte(`{"auths":{}}`)},
			},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleCreate(w, r, "clusters", "spoke-1")
	assert.Equal(t, http.StatusCreated, w.Code)
}

func Test_handleCreate_WhenSSHKeyProvided_ItShouldPostBothSecrets(t *testing.T) {
	var postedPaths []string
	spokeSrv := httptest.NewServer(spokeCreatePreflightHandler(func(w http.ResponseWriter, r *http.Request) {
		postedPaths = append(postedPaths, r.URL.Path)
		w.Header().Set(headerContentType, contentTypeJSON)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer spokeSrv.Close()

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	hc := &hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "clusters"},
		Spec: hypershiftv1beta1.HostedClusterSpec{
			PullSecret: corev1.LocalObjectReference{Name: "my-hc-pull-secret"},
			SSHKey:     corev1.LocalObjectReference{Name: "my-hc-ssh-key"},
		},
	}
	body, _ := json.Marshal(CreateRequest{
		HostedCluster: hc,
		Secrets: []corev1.Secret{
			{ObjectMeta: metav1.ObjectMeta{Name: "my-hc-pull-secret"},
				Data: map[string][]byte{".dockerconfigjson": []byte(`{"auths":{}}`)}},
			{ObjectMeta: metav1.ObjectMeta{Name: "my-hc-ssh-key"},
				Data: map[string][]byte{"id_rsa.pub": []byte("ssh-rsa AAAA...")}},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleCreate(w, r, "clusters", "spoke-1")

	assert.Equal(t, http.StatusCreated, w.Code)
	// namespace + pull-secret + ssh-key + hostedcluster
	assert.Len(t, postedPaths, 4)
}

// --- handleGetResources (single cluster — returns full bundle) ---

func Test_handleGetResources_WhenSpokeReturnsCluster_ItShouldReturnBundle(t *testing.T) {
	hcJSON, _ := json.Marshal(&hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "clusters"},
	})
	npListJSON, _ := json.Marshal(hypershiftv1beta1.NodePoolList{
		Items: []hypershiftv1beta1.NodePool{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "my-hc-pool"},
				Spec:       hypershiftv1beta1.NodePoolSpec{ClusterName: "my-hc"},
			},
		},
	})
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerContentType, contentTypeJSON)
		if strings.Contains(r.URL.Path, "/nodepools") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(npListJSON)
		} else {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(hcJSON)
		}
	}))
	defer spokeSrv.Close()

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Remote-User", "alice")
	p.handleGetResources(w, r, "clusters", "my-hc", "spoke-1")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get(headerContentType), contentTypeJSON)
	var bundle ResourceBundle
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &bundle))
	assert.Equal(t, "my-hc", bundle.HostedCluster.Name)
	assert.Len(t, bundle.NodePools, 1)
}

// --- handleDelete ---

func Test_handleDelete_WhenSpokeAccepts_ItShouldProxy200(t *testing.T) {
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer spokeSrv.Close()

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/", nil)
	r.Header.Set("X-Remote-User", "alice")
	p.handleDelete(w, r, "clusters", "my-hc", "spoke-1")
	assert.Equal(t, http.StatusOK, w.Code)
}

// --- handleCreate: Namespace creation ---

func Test_handleCreate_WhenNamespaceDoesNotExist_ItShouldPostNamespaceFirst(t *testing.T) {
	var postedPaths []string
	spokeSrv := httptest.NewServer(spokeCreatePreflightHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			postedPaths = append(postedPaths, r.URL.Path)
		}
		w.Header().Set(headerContentType, contentTypeJSON)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer spokeSrv.Close()

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	body, _ := json.Marshal(CreateRequest{
		HostedCluster: &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc"},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleCreate(w, r, "clusters", "spoke-1")

	require.Equal(t, http.StatusCreated, w.Code)
	require.NotEmpty(t, postedPaths)
	assert.Contains(t, postedPaths[0], "/api/v1/namespaces")
}

func Test_handleCreate_WhenNamespaceAlreadyExists_ItShouldContinue(t *testing.T) {
	spokeSrv := httptest.NewServer(spokeCreatePreflightHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/v1/namespaces") {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"reason":"AlreadyExists"}`)
			return
		}
		w.Header().Set(headerContentType, contentTypeJSON)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer spokeSrv.Close()

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	body, _ := json.Marshal(CreateRequest{
		HostedCluster: &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc"},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleCreate(w, r, "clusters", "spoke-1")

	assert.Equal(t, http.StatusCreated, w.Code)
}

func Test_handleCreate_WhenCreated_ItShouldReturnResourceBundle(t *testing.T) {
	spokeSrv := httptest.NewServer(spokeCreatePreflightHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerContentType, contentTypeJSON)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer spokeSrv.Close()

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	// Mirrors --render: HostedCluster references the secret; Secret is in the list.
	body, _ := json.Marshal(CreateRequest{
		HostedCluster: &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc"},
			Spec: hypershiftv1beta1.HostedClusterSpec{
				PullSecret: corev1.LocalObjectReference{Name: "my-hc-pull-secret"},
			},
		},
		NodePools: []*hypershiftv1beta1.NodePool{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "my-hc-pool"},
				Spec:       hypershiftv1beta1.NodePoolSpec{ClusterName: "my-hc"},
			},
		},
		Secrets: []corev1.Secret{
			{ObjectMeta: metav1.ObjectMeta{Name: "my-hc-pull-secret"},
				Data: map[string][]byte{".dockerconfigjson": []byte(`{}`)}},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleCreate(w, r, "clusters", "spoke-1")

	require.Equal(t, http.StatusCreated, w.Code)
	var bundle ResourceBundle
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &bundle))
	require.NotNil(t, bundle.Namespace)
	assert.Equal(t, "clusters", bundle.Namespace.Name)
	require.NotNil(t, bundle.HostedCluster)
	assert.Equal(t, "my-hc", bundle.HostedCluster.Name)
	require.Len(t, bundle.NodePools, 1)
	assert.Equal(t, "my-hc-pool", bundle.NodePools[0].Name)
}

// --- handleCreate label injection ---

func Test_handleCreate_WhenCreated_ItShouldStampCreatedViaLabel(t *testing.T) {
	var postedBodies [][]byte
	spokeSrv := httptest.NewServer(spokeCreatePreflightHandler(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		postedBodies = append(postedBodies, body)
		w.Header().Set(headerContentType, contentTypeJSON)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	hc := &hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "clusters"},
		Spec: hypershiftv1beta1.HostedClusterSpec{
			PullSecret: corev1.LocalObjectReference{Name: "my-hc-pull-secret"},
		},
	}
	np := &hypershiftv1beta1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "my-hc-us-east-1a"},
		Spec:       hypershiftv1beta1.NodePoolSpec{ClusterName: "my-hc"},
	}
	body, err := json.Marshal(CreateRequest{
		HostedCluster: hc,
		NodePools:     []*hypershiftv1beta1.NodePool{np},
		Secrets: []corev1.Secret{
			{ObjectMeta: metav1.ObjectMeta{Name: "my-hc-pull-secret"},
				Data: map[string][]byte{".dockerconfigjson": []byte(`{}`)}},
		},
	})
	require.NoError(t, err, "marshal CreateRequest fixture")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleCreate(w, r, "clusters", "spoke-1")
	assert.Equal(t, http.StatusCreated, w.Code)

	// postedBodies[0]=namespace, [1]=pull-secret, [2]=hostedcluster, [3]=nodepool
	require.Len(t, postedBodies, 4)

	var postedHC hypershiftv1beta1.HostedCluster
	require.NoError(t, json.Unmarshal(postedBodies[2], &postedHC))
	assert.Equal(t, labelCreatedViaValue, postedHC.Labels[labelCreatedVia])

	var postedNP hypershiftv1beta1.NodePool
	require.NoError(t, json.Unmarshal(postedBodies[3], &postedNP))
	assert.Equal(t, labelCreatedViaValue, postedNP.Labels[labelCreatedVia])
}

func Test_handleCreate_WhenExtraObjectsProvided_ItShouldPostThemBeforeHostedCluster(t *testing.T) {
	var posted []struct {
		path string
		body []byte
	}
	spokeSrv := httptest.NewServer(spokeCreatePreflightHandler(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		posted = append(posted, struct {
			path string
			body []byte
		}{r.URL.Path, body})
		w.Header().Set(headerContentType, contentTypeJSON)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	body, err := json.Marshal(CreateRequest{
		HostedCluster: &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc"},
		},
		ExtraObjects: []runtime.RawExtension{
			mustRawObject(t, "rbac.authorization.k8s.io/v1", "Role", "capi-provider-role"),
			mustRawObject(t, "v1", "ConfigMap", "user-ca-bundle"),
		},
	})
	require.NoError(t, err, "marshal CreateRequest fixture")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleCreate(w, r, "clusters", "spoke-1")

	require.Equal(t, http.StatusCreated, w.Code, "create with extra objects must succeed")
	require.Len(t, posted, 4, "namespace + Role + ConfigMap + HostedCluster")
	assert.Contains(t, posted[0].path, "/api/v1/namespaces", "Namespace must be created first")
	assert.Contains(t, posted[1].path, "/apis/rbac.authorization.k8s.io/v1/namespaces/clusters/roles",
		"Role extra object must be posted to the namespaced RBAC collection")
	assert.Contains(t, posted[2].path, "/api/v1/namespaces/clusters/configmaps",
		"ConfigMap extra object must be posted to the namespaced core collection")
	assert.Contains(t, posted[3].path, "/hostedclusters", "HostedCluster must be created after extra objects")

	var role unstructured.Unstructured
	require.NoError(t, json.Unmarshal(posted[1].body, &role), "Role body must be unstructured JSON")
	assert.Equal(t, "clusters", role.GetNamespace(), "extra objects must be pinned to the HostedCluster namespace")
	assert.Equal(t, labelCreatedViaValue, role.GetLabels()[labelCreatedVia], "Role must be stamped created-via")
	assert.Equal(t, "my-hc", role.GetLabels()[labelHostedCluster], "Role must be stamped with HostedCluster name")

	var cm unstructured.Unstructured
	require.NoError(t, json.Unmarshal(posted[2].body, &cm), "ConfigMap body must be unstructured JSON")
	assert.Equal(t, labelCreatedViaValue, cm.GetLabels()[labelCreatedVia], "ConfigMap must be stamped created-via")
	assert.Equal(t, "my-hc", cm.GetLabels()[labelHostedCluster], "ConfigMap must be stamped with HostedCluster name")

	var bundle ResourceBundle
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &bundle), "create response must be a ResourceBundle")
	require.Len(t, bundle.ExtraObjects, 2, "response must echo applied extra objects")
}

func Test_handleCreate_WhenExtraObjectFailsMidway_ItShouldRollbackCreatedObjects(t *testing.T) {
	var methods []string
	var paths []string
	spokeSrv := httptest.NewServer(spokeCreatePreflightHandler(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		paths = append(paths, r.URL.Path)
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/roles"):
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{}`)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/configmaps"):
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"message":"forbidden"}`)
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/roles/capi-provider-role"):
			w.WriteHeader(http.StatusOK)
		default:
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	body, err := json.Marshal(CreateRequest{
		HostedCluster: &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc"},
		},
		ExtraObjects: []runtime.RawExtension{
			mustRawObject(t, "rbac.authorization.k8s.io/v1", "Role", "capi-provider-role"),
			mustRawObject(t, "v1", "ConfigMap", "user-ca-bundle"),
		},
	})
	require.NoError(t, err, "marshal CreateRequest fixture")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleCreate(w, r, "clusters", "spoke-1")

	assert.Equal(t, http.StatusInternalServerError, w.Code, "midway extra-object failure must fail the create")
	assert.Contains(t, methods, http.MethodDelete, "created extra objects must be rolled back on failure")
	rolledBack := false
	for i, path := range paths {
		if methods[i] == http.MethodDelete && strings.Contains(path, "/roles/capi-provider-role") {
			rolledBack = true
			break
		}
	}
	assert.True(
		t,
		rolledBack,
		"rollback must DELETE the Role created before the failure; paths=%v methods=%v",
		paths,
		methods,
	)
}

func Test_handleCreate_WhenExtraObjectDenied_ItShouldReturnError(t *testing.T) {
	const sensitiveMsg = "user@evil.com is not allowed to create roles"
	spokeSrv := httptest.NewServer(spokeCreatePreflightHandler(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/roles") {
			w.WriteHeader(http.StatusForbidden)
			if _, writeErr := io.WriteString(w, fmt.Sprintf(`{"message":%q}`, sensitiveMsg)); writeErr != nil {
				require.NoError(t, fmt.Errorf("write forbidden role response: %w", writeErr))
			}
			return
		}
		w.Header().Set(headerContentType, contentTypeJSON)
		w.WriteHeader(http.StatusCreated)
		if _, writeErr := io.WriteString(w, `{}`); writeErr != nil {
			require.NoError(t, fmt.Errorf("write default created response: %w", writeErr))
		}
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	sink := &recordingLogSink{}
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)
	p.log = logr.New(sink)

	body, err := json.Marshal(CreateRequest{
		HostedCluster: &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc"},
		},
		ExtraObjects: []runtime.RawExtension{
			mustRawObject(t, "rbac.authorization.k8s.io/v1", "Role", "capi-provider-role"),
		},
	})
	require.NoError(t, err, "marshal CreateRequest fixture")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleCreate(w, r, "clusters", "spoke-1")

	assert.Equal(t, http.StatusInternalServerError, w.Code, "RBAC denial applying extra objects must fail the create")
	assert.Contains(t, w.Body.String(), "capi-provider-role", "error must name the extra object that failed")
	assert.Contains(t, w.Body.String(), "403", "error must include the spoke HTTP status")
	for _, loggedErr := range sink.errors {
		assert.NotContains(t, loggedErr.Error(), "user@evil.com", "logs must not contain spoke Status.Message")
		assert.NotContains(t, loggedErr.Error(), sensitiveMsg, "logs must not contain spoke Status.Message")
	}
}

func Test_handleCreate_WhenExtraObjectAlreadyExists_ItShouldContinue(t *testing.T) {
	var hostedClusterPosted bool
	spokeSrv := httptest.NewServer(spokeCreatePreflightHandler(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/roles") {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"reason":"AlreadyExists"}`)
			return
		}
		if strings.Contains(r.URL.Path, "/hostedclusters") {
			hostedClusterPosted = true
		}
		w.Header().Set(headerContentType, contentTypeJSON)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	body, err := json.Marshal(CreateRequest{
		HostedCluster: &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc"},
		},
		ExtraObjects: []runtime.RawExtension{
			mustRawObject(t, "rbac.authorization.k8s.io/v1", "Role", "capi-provider-role"),
		},
	})
	require.NoError(t, err, "marshal CreateRequest fixture")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleCreate(w, r, "clusters", "spoke-1")

	require.Equal(t, http.StatusCreated, w.Code, "409 on extra objects must be treated as already exists")
	assert.True(t, hostedClusterPosted, "HostedCluster must still be created after extra-object 409")
}

func Test_handleCreate_WhenExtraObjectMissingKind_ItShouldReturn400(t *testing.T) {
	p := newTestProxy(t, availableManagedCluster("spoke-1"))
	body, err := json.Marshal(CreateRequest{
		HostedCluster: &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc"},
		},
		ExtraObjects: []runtime.RawExtension{
			{Raw: []byte(`{"metadata":{"name":"no-kind"}}`)},
		},
	})
	require.NoError(t, err, "marshal CreateRequest fixture")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleCreate(w, r, "clusters", "spoke-1")

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"extra objects without apiVersion/kind must be rejected before spoke writes")
	assert.Contains(t, w.Body.String(), "Kind", "error must explain the ExtraObjects contract")
}

func Test_extraObjectCollectionAPIPath_WhenRoleAndConfigMap_ItShouldBuildNamespacedPaths(t *testing.T) {
	mapper := newTestRESTMapper()
	rolePath, err := extraObjectCollectionAPIPath(mapper, "clusters", schema.GroupVersionKind{
		Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "Role",
	})
	require.NoError(t, err, "Role GVK must map to a namespaced collection path")
	assert.Equal(t, "/apis/rbac.authorization.k8s.io/v1/namespaces/clusters/roles", rolePath,
		"Role must POST to the discovery-backed roles collection path")

	cmPath, err := extraObjectCollectionAPIPath(mapper, "clusters", schema.GroupVersionKind{
		Version: "v1", Kind: "ConfigMap",
	})
	require.NoError(t, err, "ConfigMap GVK must map to a namespaced collection path")
	assert.Equal(t, "/api/v1/namespaces/clusters/configmaps", cmPath,
		"ConfigMap must POST to the core configmaps collection path")
}

func Test_extraObjectCollectionAPIPath_WhenNetworkPolicy_ItShouldUseCorrectPlural(t *testing.T) {
	mapper := newTestRESTMapper()
	path, err := extraObjectCollectionAPIPath(mapper, "clusters", schema.GroupVersionKind{
		Group:   "networking.k8s.io",
		Version: "v1",
		Kind:    "NetworkPolicy",
	})
	require.NoError(t, err, "NetworkPolicy GVK must map via discovery-backed REST mapping")
	assert.Equal(t, "/apis/networking.k8s.io/v1/namespaces/clusters/networkpolicies", path,
		"NetworkPolicy plural must be networkpolicies, not an UnsafeGuessKind plural")
}

func Test_decodeExtraObjects_WhenDedicatedKind_ItShouldSkip(t *testing.T) {
	objs, err := decodeExtraObjects([]runtime.RawExtension{
		mustRawObject(t, "v1", "Secret", "pull-secret"),
		mustRawObject(t, "v1", "ConfigMap", "user-ca-bundle"),
		{Raw: []byte{}},
	})
	require.NoError(t, err, "valid extra objects must decode")
	require.Len(t, objs, 1, "Secret is a dedicated CreateRequest field and must be skipped")
	assert.Equal(t, "ConfigMap", objs[0].GetKind(), "only non-dedicated kinds remain after filtering")
	assert.Equal(t, "user-ca-bundle", objs[0].GetName(), "decoded ConfigMap name must match input")
}

func Test_decodeExtraObjects_WhenCustomSecretKind_ItShouldNotSkip(t *testing.T) {
	raw, err := json.Marshal(map[string]interface{}{
		"apiVersion": "example.com/v1",
		"kind":       "Secret",
		"metadata":   map[string]interface{}{"name": "custom-secret"},
	})
	require.NoError(t, err, "marshal custom Secret fixture")
	objs, err := decodeExtraObjects([]runtime.RawExtension{{Raw: raw}})
	require.NoError(t, err, "non-core Secret kind must not be filtered as dedicated")
	require.Len(t, objs, 1, "custom Secret CRD must pass through generic extra-object handling")
	assert.Equal(t, "example.com/v1", objs[0].GetAPIVersion(), "custom Secret must retain its API group")
	assert.Equal(t, "Secret", objs[0].GetKind(), "custom Secret kind must not be filtered as core v1 Secret")
	assert.Equal(t, "custom-secret", objs[0].GetName(), "custom Secret name must match input")
}

// --- handleGetResources ---

func Test_handleGetResources_WhenSpokeHasAllResources_ItShouldReturnBundle(t *testing.T) {
	ns := corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "clusters"}}
	hc := hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-hc",
			Namespace: "clusters",
			Labels:    map[string]string{labelCreatedVia: labelCreatedViaValue},
		},
	}
	np1 := hypershiftv1beta1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "my-hc-us-east-1a", Namespace: "clusters",
			Labels: map[string]string{labelCreatedVia: labelCreatedViaValue}},
		Spec: hypershiftv1beta1.NodePoolSpec{ClusterName: "my-hc"},
	}
	np2 := hypershiftv1beta1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "other-hc-pool"},
		Spec:       hypershiftv1beta1.NodePoolSpec{ClusterName: "other-hc"},
	}

	nsJSON, _ := json.Marshal(ns)
	hcJSON, _ := json.Marshal(hc)
	npListJSON, _ := json.Marshal(hypershiftv1beta1.NodePoolList{Items: []hypershiftv1beta1.NodePool{np1, np2}})

	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerContentType, contentTypeJSON)
		switch {
		case strings.HasSuffix(r.URL.Path, "/namespaces/clusters") && !strings.Contains(r.URL.Path, "hypershift"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(nsJSON)
		case strings.Contains(r.URL.Path, "/hostedclusters/my-hc"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(hcJSON)
		case strings.Contains(r.URL.Path, "/nodepools"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(npListJSON)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer spokeSrv.Close()

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Remote-User", "alice")
	p.handleGetResources(w, r, "clusters", "my-hc", "spoke-1")

	assert.Equal(t, http.StatusOK, w.Code)
	var bundle ResourceBundle
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &bundle))
	require.NotNil(t, bundle.Namespace)
	assert.Equal(t, "clusters", bundle.Namespace.Name)
	require.NotNil(t, bundle.HostedCluster)
	assert.Equal(t, "my-hc", bundle.HostedCluster.Name)
	assert.Equal(t, labelCreatedViaValue, bundle.HostedCluster.Labels[labelCreatedVia])
	// Only np1 belongs to my-hc; np2 should be filtered out
	require.Len(t, bundle.NodePools, 1)
	assert.Equal(t, "my-hc-us-east-1a", bundle.NodePools[0].Name)
	assert.Equal(t, labelCreatedViaValue, bundle.NodePools[0].Labels[labelCreatedVia])
}

func Test_handleGetResources_WhenHCNotFound_ItShouldReturn404(t *testing.T) {
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer spokeSrv.Close()

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Remote-User", "alice")
	p.handleGetResources(w, r, "clusters", "missing-hc", "spoke-1")

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func Test_handleRoute_WhenResourcesSubresource_ItShouldDispatch(t *testing.T) {
	hcJSON, _ := json.Marshal(hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "clusters"},
	})
	npListJSON, _ := json.Marshal(hypershiftv1beta1.NodePoolList{})

	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerContentType, contentTypeJSON)
		if strings.Contains(r.URL.Path, "/hostedclusters/my-hc") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(hcJSON)
		} else {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(npListJSON)
		}
	}))
	defer spokeSrv.Close()

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	path := "/apis/" + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion +
		"/namespaces/clusters/hostedclusters/my-hc/resources?hostingCluster=spoke-1"
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("X-Remote-User", "alice")
	p.handleRoute(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var bundle ResourceBundle
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &bundle))
	assert.Equal(t, "my-hc", bundle.HostedCluster.Name)
}

func Test_computeDestroyFinalizers_WhenAddOrRemove_ItShouldMatchDestroyCLI(t *testing.T) {
	hc := &hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Finalizers: []string{"hypershift.io/finalizer"},
		},
	}
	finalizers, changed, err := computeDestroyFinalizers(hc, finalizerOpAdd)
	require.NoError(t, err, "adding destroy finalizer to a live HC must succeed")
	require.True(t, changed, "add must report a change when destroy finalizer is absent")
	assert.Equal(t, []string{"hypershift.io/finalizer", hostedClusterDestroyFinalizer}, finalizers,
		"add must append openshift.io/destroy-cluster to existing finalizers")

	hc.Finalizers = finalizers
	finalizers, changed, err = computeDestroyFinalizers(hc, finalizerOpAdd)
	require.NoError(t, err, "re-adding an existing destroy finalizer must succeed")
	assert.False(t, changed, "add must be a no-op when destroy finalizer is already present")

	finalizers, changed, err = computeDestroyFinalizers(hc, finalizerOpRemove)
	require.NoError(t, err, "removing destroy finalizer must succeed")
	require.True(t, changed, "remove must report a change when destroy finalizer is present")
	assert.Equal(t, []string{"hypershift.io/finalizer"}, finalizers,
		"remove must strip only openshift.io/destroy-cluster")
}

func Test_computeDestroyFinalizers_WhenHCDeleting_ItShouldRejectAdd(t *testing.T) {
	now := metav1.Now()
	hc := &hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			DeletionTimestamp: &now,
		},
	}
	_, _, err := computeDestroyFinalizers(hc, finalizerOpAdd)
	require.Error(t, err, "add must fail while HostedCluster is deleting")
	assert.Contains(t, err.Error(), "deleting")
}

func Test_computeDestroyFinalizers_WhenRemoveWithoutDestroyFinalizer_ItShouldBeNoop(t *testing.T) {
	hc := &hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Finalizers: []string{"hypershift.io/finalizer"},
		},
	}
	finalizers, changed, err := computeDestroyFinalizers(hc, finalizerOpRemove)
	require.NoError(t, err, "remove on HC without destroy finalizer must succeed")
	assert.False(t, changed, "remove must be a no-op when destroy finalizer is absent")
	assert.Equal(t, []string{"hypershift.io/finalizer"}, finalizers,
		"remove must leave unrelated finalizers unchanged")
}

func Test_handleFinalizers_WhenAdd_ItShouldMergePatchDestroyFinalizer(t *testing.T) {
	var patchBodies [][]byte
	hc := hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "my-hc",
			Namespace:       "clusters",
			ResourceVersion: "42",
		},
	}
	hcJSON, err := json.Marshal(hc)
	require.NoError(t, err, "test fixture HostedCluster must marshal")

	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_, writeErr := w.Write(hcJSON)
			require.NoError(t, writeErr, "spoke fixture must write HostedCluster JSON")
		case http.MethodPatch:
			body, readErr := io.ReadAll(r.Body)
			require.NoError(t, readErr, "spoke fixture must read PATCH body")
			patchBodies = append(patchBodies, body)
			assert.Equal(t, mergePatchContentType, r.Header.Get(headerContentType),
				"finalizer patch must use application/merge-patch+json")
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected method %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	body, err := json.Marshal(FinalizersRequest{Operation: finalizerOpAdd})
	require.NoError(t, err, "FinalizersRequest must marshal")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/", bytes.NewReader(body))
	r.Header.Set("Content-Type", contentTypeJSON)
	r.Header.Set("X-Remote-User", "alice")
	p.handleFinalizers(w, r, "clusters", "my-hc", "spoke-1")

	require.Equal(t, http.StatusOK, w.Code, "finalizer add must return 200: %s", w.Body.String())
	require.Len(t, patchBodies, 1, "add must send exactly one merge patch to the spoke")
	var patch map[string]interface{}
	require.NoError(t, json.Unmarshal(patchBodies[0], &patch), "patch body must be valid JSON merge patch")
	meta := patch["metadata"].(map[string]interface{})
	finalizers := meta["finalizers"].([]interface{})
	assert.Contains(t, finalizers, hostedClusterDestroyFinalizer,
		"patch must add openshift.io/destroy-cluster so hcp delete cluster behavior matches")
	assert.Equal(t, "42", meta["resourceVersion"],
		"patch must include resourceVersion for optimistic concurrency")

	var resp FinalizersResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), "response must unmarshal as FinalizersResponse")
	require.NotNil(t, resp.HostedCluster, "response must include the updated HostedCluster")
}

func Test_handleFinalizers_WhenRemove_ItShouldMergePatchWithoutDestroyFinalizer(t *testing.T) {
	var patchBodies [][]byte
	hc := hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "my-hc",
			Namespace:       "clusters",
			ResourceVersion: "99",
			Finalizers:      []string{"hypershift.io/finalizer", hostedClusterDestroyFinalizer},
		},
	}
	hcJSON, err := json.Marshal(hc)
	require.NoError(t, err, "test fixture HostedCluster must marshal")

	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_, writeErr := w.Write(hcJSON)
			require.NoError(t, writeErr, "spoke fixture must write HostedCluster JSON")
		case http.MethodPatch:
			body, readErr := io.ReadAll(r.Body)
			require.NoError(t, readErr, "spoke fixture must read PATCH body")
			patchBodies = append(patchBodies, body)
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	body, err := json.Marshal(FinalizersRequest{Operation: finalizerOpRemove})
	require.NoError(t, err, "FinalizersRequest must marshal")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/", bytes.NewReader(body))
	r.Header.Set("Content-Type", contentTypeJSON)
	r.Header.Set("X-Remote-User", "alice")
	p.handleFinalizers(w, r, "clusters", "my-hc", "spoke-1")

	require.Equal(t, http.StatusOK, w.Code, "finalizer remove must return 200: %s", w.Body.String())
	require.Len(t, patchBodies, 1, "remove must send exactly one merge patch to the spoke")
	var patch map[string]interface{}
	require.NoError(t, json.Unmarshal(patchBodies[0], &patch), "patch body must be valid JSON merge patch")
	meta := patch["metadata"].(map[string]interface{})
	finalizers := meta["finalizers"].([]interface{})
	assert.Equal(t, []interface{}{"hypershift.io/finalizer"}, finalizers,
		"patch must remove only openshift.io/destroy-cluster")
}

func Test_handleFinalizers_WhenFinalizerAlreadyPresent_ItShouldReturnWithoutPatch(t *testing.T) {
	patchCount := 0
	hc := hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "my-hc",
			Namespace:  "clusters",
			Finalizers: []string{hostedClusterDestroyFinalizer},
		},
	}
	hcJSON, err := json.Marshal(hc)
	require.NoError(t, err, "test fixture HostedCluster must marshal")

	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_, writeErr := w.Write(hcJSON)
			require.NoError(t, writeErr, "spoke fixture must write HostedCluster JSON")
		case http.MethodPatch:
			patchCount++
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	body, err := json.Marshal(FinalizersRequest{Operation: finalizerOpAdd})
	require.NoError(t, err, "FinalizersRequest must marshal")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleFinalizers(w, r, "clusters", "my-hc", "spoke-1")

	require.Equal(t, http.StatusOK, w.Code, "idempotent add must return 200: %s", w.Body.String())
	assert.Equal(t, 0, patchCount, "add must skip spoke PATCH when destroy finalizer is already present")
}

func Test_handleFinalizers_WhenPatchConflict_ItShouldRetry(t *testing.T) {
	patchAttempts := 0
	hc := hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "clusters", ResourceVersion: "1"},
	}
	hcJSON, err := json.Marshal(hc)
	require.NoError(t, err, "test fixture HostedCluster must marshal")

	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_, writeErr := w.Write(hcJSON)
			require.NoError(t, writeErr, "spoke fixture must write HostedCluster JSON")
		case http.MethodPatch:
			patchAttempts++
			if patchAttempts == 1 {
				w.WriteHeader(http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	body, err := json.Marshal(FinalizersRequest{Operation: finalizerOpAdd})
	require.NoError(t, err, "FinalizersRequest must marshal")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleFinalizers(w, r, "clusters", "my-hc", "spoke-1")

	require.Equal(t, http.StatusOK, w.Code, "conflict retry must eventually succeed: %s", w.Body.String())
	assert.Equal(t, 2, patchAttempts, "409 conflict must trigger a spoke PATCH retry")
}

func Test_handleFinalizers_WhenInvalidOperation_ItShouldReturn400(t *testing.T) {
	p := newTestProxy(t)
	body, err := json.Marshal(FinalizersRequest{Operation: "invalid"})
	require.NoError(t, err, "FinalizersRequest must marshal")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/", bytes.NewReader(body))
	p.handleFinalizers(w, r, "clusters", "my-hc", "spoke-1")

	assertStatusError(t, w, http.StatusBadRequest, `operation must be "add" or "remove"`)
}

func Test_dispatchFinalizers_WhenGET_ItShouldReturn405(t *testing.T) {
	p := newTestProxy(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	p.dispatchFinalizers(w, r, "clusters", "my-hc", "spoke-1")

	assertStatusError(t, w, http.StatusMethodNotAllowed, "method not allowed")
}

func Test_dispatchFinalizers_WhenInvalidNamespace_ItShouldReturn400(t *testing.T) {
	p := newTestProxy(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/", nil)
	p.dispatchFinalizers(w, r, "bad ns!", "my-hc", "spoke-1")

	assertStatusError(t, w, http.StatusBadRequest, "invalid namespace")
}

func Test_handleFinalizers_WhenConflictExhausted_ItShouldReturn409(t *testing.T) {
	hc := hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "clusters"},
	}
	hcJSON, err := json.Marshal(hc)
	require.NoError(t, err, "test fixture HostedCluster must marshal")

	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_, writeErr := w.Write(hcJSON)
			require.NoError(t, writeErr, "spoke fixture must write HostedCluster JSON")
		case http.MethodPatch:
			w.WriteHeader(http.StatusConflict)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	body, err := json.Marshal(FinalizersRequest{Operation: finalizerOpAdd})
	require.NoError(t, err, "FinalizersRequest must marshal")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleFinalizers(w, r, "clusters", "my-hc", "spoke-1")

	assertStatusError(t, w, http.StatusConflict, "HostedCluster finalizers conflict after retries")
}

func Test_handleFinalizers_WhenPatchFails_ItShouldReturn502(t *testing.T) {
	hc := hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "clusters"},
	}
	hcJSON, err := json.Marshal(hc)
	require.NoError(t, err, "test fixture HostedCluster must marshal")

	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_, writeErr := w.Write(hcJSON)
			require.NoError(t, writeErr, "spoke fixture must write HostedCluster JSON")
		case http.MethodPatch:
			w.WriteHeader(http.StatusInternalServerError)
			_, writeErr := w.Write([]byte(`{"kind":"Status","message":"upstream failure"}`))
			require.NoError(t, writeErr, "spoke fixture must write error Status")
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	body, err := json.Marshal(FinalizersRequest{Operation: finalizerOpAdd})
	require.NoError(t, err, "FinalizersRequest must marshal")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleFinalizers(w, r, "clusters", "my-hc", "spoke-1")

	assertStatusError(t, w, http.StatusBadGateway, "failed to patch HostedCluster finalizers")
}

func Test_handleFinalizers_WhenHCNotFound_ItShouldReturn404(t *testing.T) {
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	body, _ := json.Marshal(FinalizersRequest{Operation: finalizerOpAdd})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleFinalizers(w, r, "clusters", "missing", "spoke-1")

	assert.Equal(t, http.StatusNotFound, w.Code, "missing HostedCluster must return 404")
}

func Test_handleRoute_WhenFinalizersSubresource_ItShouldDispatchPATCH(t *testing.T) {
	hc := hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "clusters"},
	}
	hcJSON, _ := json.Marshal(hc)

	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerContentType, contentTypeJSON)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(hcJSON)
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	path := "/apis/" + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion +
		"/namespaces/clusters/hostedclusters/my-hc/finalizers?hostingCluster=spoke-1"
	body, _ := json.Marshal(FinalizersRequest{Operation: finalizerOpAdd})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(body))
	r.Header.Set("Content-Type", contentTypeJSON)
	r.Header.Set("X-Remote-User", "alice")
	p.handleRoute(w, r)

	assert.Equal(t, http.StatusOK, w.Code, "finalizers route must return 200: %s", w.Body.String())
}

// --- handlePatchResources (kubectl-edit style: full replace via PUT) ---

func Test_handlePatchResources_WhenFullBundleSent_ItShouldPutHCAndNPsAndReturnBundle(t *testing.T) {
	var putPaths []string
	hcJSON, _ := json.Marshal(hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "clusters"},
	})
	npListJSON, _ := json.Marshal(hypershiftv1beta1.NodePoolList{
		Items: []hypershiftv1beta1.NodePool{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "my-hc-pool"},
				Spec:       hypershiftv1beta1.NodePoolSpec{ClusterName: "my-hc"},
			},
		},
	})

	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerContentType, contentTypeJSON)
		if r.Method == http.MethodPut {
			putPaths = append(putPaths, r.URL.Path)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{}`)
			return
		}
		// GET calls from handleGetResources after the update
		if strings.Contains(r.URL.Path, "/hostedclusters/my-hc") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(hcJSON)
		} else if strings.Contains(r.URL.Path, "/nodepools") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(npListJSON)
		} else {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	defer spokeSrv.Close()

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	// Send back the full bundle — same shape as GET /resources response
	reqBundle := ResourceBundle{
		HostedCluster: &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "my-hc",
				Namespace:   "clusters",
				Annotations: map[string]string{"upgrade": "true"},
			},
		},
		NodePools: []hypershiftv1beta1.NodePool{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "my-hc-pool"},
				Spec:       hypershiftv1beta1.NodePoolSpec{ClusterName: "my-hc"},
			},
		},
	}
	reqBody, _ := json.Marshal(reqBundle)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(reqBody))
	r.Header.Set("X-Remote-User", "alice")
	r.Header.Set(headerContentType, contentTypeJSON)
	p.handlePatchResources(w, r, "clusters", "my-hc", "spoke-1")

	assert.Equal(t, http.StatusOK, w.Code)
	// Must have PUT the HC and the NodePool
	assert.Len(t, putPaths, 2)
	assert.Contains(t, strings.Join(putPaths, ","), "/hostedclusters/my-hc")
	assert.Contains(t, strings.Join(putPaths, ","), "/nodepools/my-hc-pool")

	// Response must be the live ResourceBundle
	var bundle ResourceBundle
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &bundle))
	assert.Equal(t, "my-hc", bundle.HostedCluster.Name)
}

func Test_handlePatchResources_WhenHCOnly_ItShouldSkipNodePools(t *testing.T) {
	var putPaths []string
	hcJSON, _ := json.Marshal(hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-hc"},
	})
	npListJSON, _ := json.Marshal(hypershiftv1beta1.NodePoolList{})

	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerContentType, contentTypeJSON)
		if r.Method == http.MethodPut {
			putPaths = append(putPaths, r.URL.Path)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{}`)
			return
		}
		if strings.Contains(r.URL.Path, "/hostedclusters/my-hc") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(hcJSON)
		} else {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(npListJSON)
		}
	}))
	defer spokeSrv.Close()

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	// Only HC in the bundle — NodePools absent means skip them
	reqBundle := ResourceBundle{
		HostedCluster: &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Labels: map[string]string{"env": "staging"}},
		},
	}
	reqBody, _ := json.Marshal(reqBundle)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(reqBody))
	r.Header.Set("X-Remote-User", "alice")
	p.handlePatchResources(w, r, "clusters", "my-hc", "spoke-1")

	assert.Equal(t, http.StatusOK, w.Code)
	// Only HC should be PUT; no NodePool PUTs
	assert.Len(t, putPaths, 1)
	assert.Contains(t, putPaths[0], "/hostedclusters/my-hc")
}

func Test_handleRoute_WhenResourcesSubresourcePatch_ItShouldDispatch(t *testing.T) {
	hcJSON, _ := json.Marshal(hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-hc"},
	})
	npListJSON, _ := json.Marshal(hypershiftv1beta1.NodePoolList{})

	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerContentType, contentTypeJSON)
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{}`)
			return
		}
		if strings.Contains(r.URL.Path, "/hostedclusters/my-hc") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(hcJSON)
		} else {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(npListJSON)
		}
	}))
	defer spokeSrv.Close()

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	reqBundle := ResourceBundle{
		HostedCluster: &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Annotations: map[string]string{"k": "v"}},
		},
	}
	reqBody, _ := json.Marshal(reqBundle)
	path := "/apis/" + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion +
		"/namespaces/clusters/hostedclusters/my-hc/resources?hostingCluster=spoke-1"
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(reqBody))
	r.Header.Set("X-Remote-User", "alice")
	r.Header.Set(headerContentType, contentTypeJSON)
	p.handleRoute(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var bundle ResourceBundle
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &bundle))
	assert.Equal(t, "my-hc", bundle.HostedCluster.Name)
}

func Test_handleRoute_WhenPatchOnNamedResource_ItShouldDoBundleReplace(t *testing.T) {
	// PUT /{name} (no /resources suffix) must behave identically to PUT /{name}/resources —
	// full bundle replace, not a single-resource merge-patch.
	var putPaths []string
	hcJSON, _ := json.Marshal(hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-hc"},
	})
	npListJSON, _ := json.Marshal(hypershiftv1beta1.NodePoolList{})

	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerContentType, contentTypeJSON)
		if r.Method == http.MethodPut {
			putPaths = append(putPaths, r.URL.Path)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{}`)
			return
		}
		if strings.Contains(r.URL.Path, "/hostedclusters/my-hc") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(hcJSON)
		} else {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(npListJSON)
		}
	}))
	defer spokeSrv.Close()

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	reqBundle := ResourceBundle{
		HostedCluster: &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Annotations: map[string]string{"k": "v"}},
		},
	}
	reqBody, _ := json.Marshal(reqBundle)
	// Note: no /resources suffix — still routes to handlePatchResources
	path := "/apis/" + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion +
		"/namespaces/clusters/hostedclusters/my-hc?hostingCluster=spoke-1"
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(reqBody))
	r.Header.Set("X-Remote-User", "alice")
	r.Header.Set(headerContentType, contentTypeJSON)
	p.handleRoute(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	// A PUT was issued to the spoke (full replace, not merge-patch)
	assert.Len(t, putPaths, 1)
	assert.Contains(t, putPaths[0], "hostedclusters/my-hc")
}

// --- handleList (ACM Search) ---

func Test_handleRoute_WhenListPath_ItShouldReturn405(t *testing.T) {
	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, "http://unused", mc)

	path := "/apis/" + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion +
		"/namespaces/clusters/hostedclusters?hostingCluster=spoke-1"
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("X-Remote-User", "alice")
	p.handleRoute(w, r)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// --- handleDelete with NodePools ---

func Test_handleDelete_WhenMatchingNodePoolsExist_ItShouldDeleteThem(t *testing.T) {
	var deleted []string
	npListJSON, _ := json.Marshal(hypershiftv1beta1.NodePoolList{
		Items: []hypershiftv1beta1.NodePool{
			{ObjectMeta: metav1.ObjectMeta{Name: "my-hc-pool"}, Spec: hypershiftv1beta1.NodePoolSpec{ClusterName: "my-hc"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "other-pool"}, Spec: hypershiftv1beta1.NodePoolSpec{ClusterName: "other"}},
		},
	})
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerContentType, contentTypeJSON)
		if r.Method == http.MethodDelete {
			deleted = append(deleted, r.URL.Path)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{}`)
			return
		}
		if strings.Contains(r.URL.Path, "/nodepools") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(npListJSON)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer spokeSrv.Close()

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/", nil)
	r.Header.Set("X-Remote-User", "alice")
	p.handleDelete(w, r, "clusters", "my-hc", "spoke-1")

	assert.Equal(t, http.StatusOK, w.Code)
	joined := strings.Join(deleted, ",")
	assert.Contains(t, joined, "/nodepools/my-hc-pool")
	assert.NotContains(t, joined, "/nodepools/other-pool")
	assert.Contains(t, joined, "/hostedclusters/my-hc")
}

// --- createOrUpdateSecretOnSpoke ---

func Test_createOrUpdateSecretOnSpoke_WhenConflict_ItShouldPut(t *testing.T) {
	var methods []string
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `already exists`)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer spokeSrv.Close()

	p := newTestProxyWithSpokeURL(t, spokeSrv.URL)
	client, err := p.spokeHTTPClient("alice", nil)
	require.NoError(t, err)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: "clusters"},
		Data:       map[string][]byte{"key": []byte("val")},
	}
	err = p.createOrUpdateSecretOnSpoke(context.Background(), client, "spoke-1", "clusters", secret)
	require.NoError(t, err)
	assert.Equal(t, []string{http.MethodPost, http.MethodPut}, methods)
}

func Test_createOrUpdateSecretOnSpoke_WhenCreateSucceeds_ItShouldNotPut(t *testing.T) {
	var methods []string
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer spokeSrv.Close()

	p := newTestProxyWithSpokeURL(t, spokeSrv.URL)
	client, err := p.spokeHTTPClient("alice", nil)
	require.NoError(t, err)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: "clusters"},
		Data:       map[string][]byte{"key": []byte("val")},
	}
	err = p.createOrUpdateSecretOnSpoke(context.Background(), client, "spoke-1", "clusters", secret)
	require.NoError(t, err)
	assert.Equal(t, []string{http.MethodPost}, methods)
}

// --- helpers / middleware / URL defaults ---

func Test_writeJSONError_WhenCalled_ItShouldWriteKubernetesStatus(t *testing.T) {
	w := httptest.NewRecorder()
	err := writeJSONError(w, "something went wrong", http.StatusBadRequest)
	require.NoError(t, err, "encoding a metav1.Status must succeed so oc can decode the error")
	assert.Equal(t, contentTypeJSON, w.Header().Get(headerContentType), "Status responses must be application/json")
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"), "error responses must set X-Content-Type-Options")
	assertStatusError(t, w, http.StatusBadRequest, "something went wrong")
	var status metav1.Status
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status), "response body must be a metav1.Status JSON object")
	assert.Equal(t, metav1.StatusReasonBadRequest, status.Reason, "HTTP 400 must map to StatusReasonBadRequest")
}

func Test_writeJSONError_WhenWriteFails_ItShouldReturnError(t *testing.T) {
	w := failWriter{ResponseWriter: httptest.NewRecorder()}
	err := writeJSONError(w, "something went wrong", http.StatusBadRequest)
	require.Error(t, err, "a failed Write must be returned so the caller can log it")
	assert.Contains(t, err.Error(), "write Status error response", "error must wrap the write failure")
}

type failWriter struct {
	http.ResponseWriter
}

func (w failWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func Test_statusReasonForCode_WhenMapped_ItShouldReturnKubernetesReasons(t *testing.T) {
	assert.Equal(t, metav1.StatusReasonBadRequest, statusReasonForCode(http.StatusBadRequest),
		"HTTP 400 must map to StatusReasonBadRequest")
	assert.Equal(t, metav1.StatusReasonForbidden, statusReasonForCode(http.StatusForbidden),
		"HTTP 403 must map to StatusReasonForbidden")
	assert.Equal(t, metav1.StatusReasonNotFound, statusReasonForCode(http.StatusNotFound),
		"HTTP 404 must map to StatusReasonNotFound")
	assert.Equal(t, metav1.StatusReasonMethodNotAllowed, statusReasonForCode(http.StatusMethodNotAllowed),
		"HTTP 405 must map to StatusReasonMethodNotAllowed")
	assert.Equal(t, metav1.StatusReasonServiceUnavailable, statusReasonForCode(http.StatusServiceUnavailable),
		"HTTP 503 must map to StatusReasonServiceUnavailable")
	assert.Equal(t, metav1.StatusReasonConflict, statusReasonForCode(http.StatusConflict),
		"HTTP 409 must map to StatusReasonConflict")
	assert.Equal(t, metav1.StatusReasonInternalError, statusReasonForCode(http.StatusBadGateway),
		"HTTP 502 must map to StatusReasonInternalError")
	assert.Equal(t, metav1.StatusReasonInternalError, statusReasonForCode(http.StatusInternalServerError),
		"HTTP 500 must map to StatusReasonInternalError")
}

func assertStatusError(t *testing.T, w *httptest.ResponseRecorder, code int, msgContains string) {
	t.Helper()
	assert.Equal(t, code, w.Code, "HTTP status code on the Status error response")
	var status metav1.Status
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status), "error body must unmarshal as metav1.Status")
	assert.Equal(t, "Status", status.Kind, "kind must be Status so oc/kubectl can decode the error")
	assert.Equal(t, "v1", status.APIVersion, "apiVersion must be v1")
	assert.Equal(t, metav1.StatusFailure, status.Status, "status field must be Failure")
	assert.Equal(t, int32(code), status.Code, "Status.code must match the HTTP status")
	assert.Contains(t, status.Message, msgContains, "Status.message must include the error detail")
}

func Test_handleDelete_WhenSpokeResponds_ItShouldForwardContentType(t *testing.T) {
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerContentType, contentTypeJSON)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer spokeSrv.Close()

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/", nil)
	r.Header.Set("X-Remote-User", "alice")
	p.handleDelete(w, r, "clusters", "my-hc", "spoke-1")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, contentTypeJSON, w.Header().Get(headerContentType))
}

func Test_createOnSpoke_WhenSpokeReturns409_ItShouldReturnSpokeConflictError(t *testing.T) {
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `already exists`)
	}))
	defer spokeSrv.Close()

	p := newTestProxyWithSpokeURL(t, spokeSrv.URL)
	client, err := p.spokeHTTPClient("alice", nil)
	require.NoError(t, err)

	err = p.createOnSpoke(context.Background(), client, "spoke-1", "clusters", resourceHostedClusters,
		&hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "hc1"}})
	require.Error(t, err)
	assert.True(t, isAlreadyExists(err), "expected errSpokeConflict sentinel, got: %v", err)
}

func Test_loggingMiddleware_WhenCalled_ItShouldInvokeNext(t *testing.T) {
	p := newTestProxy(t)
	called := false
	handler := p.loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/healthz?hostingCluster=spoke-1", nil)
	r.Header.Set("X-Remote-User", "alice")
	handler.ServeHTTP(w, r)
	assert.True(t, called)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func Test_defaultURLs_WhenCalled_ItShouldUseExpectedNamespaces(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "")
	assert.Equal(t,
		"https://cluster-proxy-addon-user.multicluster-engine.svc:9092",
		defaultClusterProxyURL())
}

func Test_discoverClusterProxyRouteURL_WhenRouteHasHost_ItShouldReturnHTTPSURL(t *testing.T) {
	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "route.openshift.io", Version: "v1", Kind: "Route",
	})
	route.SetName(clusterProxyServiceName)
	route.SetNamespace("multicluster-engine")
	require.NoError(t, unstructured.SetNestedField(route.Object, "proxy.apps.example.com", "spec", "host"))

	p := newTestProxy(t, route)
	url, err := discoverClusterProxyRouteURL(context.Background(), p.hubClient, "multicluster-engine", p.log)
	require.NoError(t, err)
	assert.Equal(t, "https://proxy.apps.example.com", url)
}

func Test_discoverClusterProxyRouteURL_WhenRouteMissingHost_ItShouldReturnEmpty(t *testing.T) {
	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "route.openshift.io", Version: "v1", Kind: "Route",
	})
	route.SetName(clusterProxyServiceName)
	route.SetNamespace("multicluster-engine")

	p := newTestProxy(t, route)
	url, err := discoverClusterProxyRouteURL(context.Background(), p.hubClient, "multicluster-engine", p.log)
	require.NoError(t, err)
	assert.Empty(t, url)
}

func Test_resolveClusterProxyURL_WhenRoutePresent_ItShouldPreferRoute(t *testing.T) {
	t.Setenv("CLUSTER_PROXY_URL", "")
	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "route.openshift.io", Version: "v1", Kind: "Route",
	})
	route.SetName(clusterProxyServiceName)
	route.SetNamespace("my-mce-ns")
	require.NoError(t, unstructured.SetNestedField(route.Object, "cp.example.com", "spec", "host"))

	p := newTestProxy(t, route)
	url := resolveClusterProxyURL(context.Background(), p.hubClient, "my-mce-ns", p.log)
	assert.Equal(t, "https://cp.example.com", url)
}

func Test_putOnSpoke_WhenSpokeReturnsError_ItShouldReturnError(t *testing.T) {
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `boom`)
	}))
	defer spokeSrv.Close()

	p := newTestProxyWithSpokeURL(t, spokeSrv.URL)
	client, err := p.spokeHTTPClient("alice", nil)
	require.NoError(t, err)

	err = p.putOnSpoke(context.Background(), client, "spoke-1",
		"/api/v1/namespaces/ns/secrets/s", map[string]string{"k": "v"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func Test_spokeURL_WhenClusterProxyURLEmpty_ItShouldUseDefault(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "")
	p := newTestProxy(t)
	p.clusterProxyURL = ""
	got, err := p.spokeURL("spoke-1", "/apis/v1")
	require.NoError(t, err)
	assert.Equal(t, defaultClusterProxyURL()+"/spoke-1/apis/v1", got.String())
}

func Test_sanitizeProxyName_WhenInvalid_ItShouldReject(t *testing.T) {
	_, err := sanitizeProxyName("")
	require.Error(t, err)
	_, err = sanitizeProxyName("../evil")
	require.Error(t, err)
	_, err = sanitizeProxyName("http://evil.example")
	require.Error(t, err)
	got, err := sanitizeProxyName("spoke-1")
	require.NoError(t, err)
	assert.Equal(t, "spoke-1", got)
}

func Test_spokeURL_WhenInvalidSpokeName_ItShouldError(t *testing.T) {
	p := newTestProxy(t)
	p.clusterProxyURL = "https://cluster-proxy.example:9092"
	_, err := p.spokeURL("../escape", "/api/v1/namespaces/ns")
	require.Error(t, err)
	_, err = p.spokeURL("spoke-1", "/api/v1/../etc/passwd")
	require.Error(t, err)
}

func Test_StartHCPProxy_WhenContextCancelled_ItShouldShutdownCleanly(t *testing.T) {
	prevAddr := hcpProxyListenAddr
	hcpProxyListenAddr = "127.0.0.1:0"
	t.Cleanup(func() { hcpProxyListenAddr = prevAddr })

	profile, _ := tlspkg.GetTLSProfileSpec(nil)
	zapLog, _ := zap.NewDevelopment()
	log := zapr.NewLogger(zapLog)
	hubClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()
	hubConfig := &rest.Config{
		Host:            "https://127.0.0.1:1",
		TLSClientConfig: rest.TLSClientConfig{Insecure: true},
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- StartHCPProxy(ctx, profile, hubConfig, hubClient, newTestRESTMapper(), log)
	}()

	// Give the TLS server a moment to bind, then cancel for graceful shutdown.
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("StartHCPProxy did not return after context cancel")
	}
}

func Test_handlePatchResources_WhenBodyInvalid_ItShouldReturn400(t *testing.T) {
	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, "http://unused", mc)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{bad`))
	r.Header.Set("X-Remote-User", "alice")
	p.handlePatchResources(w, r, "clusters", "my-hc", "spoke-1")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func Test_handlePatchResources_WhenHostedClusterNil_ItShouldRefetchBundle(t *testing.T) {
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer spokeSrv.Close()

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)
	body, _ := json.Marshal(ResourceBundle{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handlePatchResources(w, r, "clusters", "my-hc", "spoke-1")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func Test_handleCreate_WhenNodePoolCreateFails_ItShouldOmitFromResponse(t *testing.T) {
	spokeSrv := httptest.NewServer(spokeCreatePreflightHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerContentType, contentTypeJSON)
		if strings.Contains(r.URL.Path, "/nodepools") {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `np failed`)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer spokeSrv.Close()

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)
	body, _ := json.Marshal(CreateRequest{
		HostedCluster: &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc"},
		},
		NodePools: []*hypershiftv1beta1.NodePool{
			{ObjectMeta: metav1.ObjectMeta{Name: "pool-1"}},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleCreate(w, r, "clusters", "spoke-1")

	require.Equal(t, http.StatusCreated, w.Code)
	var bundle ResourceBundle
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &bundle))
	assert.Empty(t, bundle.NodePools)
	require.Len(t, bundle.Warnings, 1)
	assert.Contains(t, bundle.Warnings[0], "pool-1")
}

// --- TLS cert validity ---

func Test_generateSelfSignedCert_WhenParsed_ItShouldBeValidForTLSServerAuth(t *testing.T) {
	cert, err := generateSelfSignedCert("multicluster-engine")
	require.NoError(t, err)

	tlsCert := tls.Certificate{Certificate: cert.Certificate, PrivateKey: cert.PrivateKey}
	x509Cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
	require.NoError(t, err)

	assert.Contains(t, x509Cert.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
}

// ----------- helpers -----------

// availableManagedCluster returns a ManagedCluster with Available=True.
func availableManagedCluster(name string) *clusterv1.ManagedCluster {
	return &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: clusterv1.ManagedClusterStatus{
			Conditions: []metav1.Condition{
				{
					Type:   clusterv1.ManagedClusterConditionAvailable,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
}

func mustRawObject(t *testing.T, apiVersion, kind, name string) runtime.RawExtension {
	t.Helper()
	raw, err := json.Marshal(map[string]interface{}{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]interface{}{"name": name},
	})
	require.NoError(t, err, "marshal extra object fixture")
	return runtime.RawExtension{Raw: raw}
}

// spokeCreatePreflightHandler answers create preflight GETs before delegating to next.
func spokeCreatePreflightHandler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if strings.HasSuffix(r.URL.Path, apiPathVersion) {
				w.Header().Set(headerContentType, contentTypeJSON)
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, `{"platform":"linux/amd64"}`)
				return
			}
			if strings.Contains(r.URL.Path, "/hostedclusters/") &&
				!strings.HasSuffix(r.URL.Path, "/hostedclusters") {
				w.Header().Set(headerContentType, contentTypeJSON)
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Failure",`+
					`"reason":"NotFound","code":404}`)
				return
			}
		}
		next(w, r)
	}
}

// newTestProxyWithSpokeURL sets clusterProxyURL to the mock server so all spoke
// requests are routed there instead of the real cluster-proxy.
// hubConfig points at a separate mock that reports clusterview as absent so
// checkHubPermission (used by handleRoute) skips non-fatally in unit tests.
func newTestProxyWithSpokeURL(t *testing.T, spokeServerURL string, objs ...runtime.Object) *hcpProxy {
	t.Helper()
	t.Setenv("SKIP_HUB_PERMISSION_CHECK", "true")
	p := newTestProxy(t, objs...)
	hubSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerContentType, contentTypeJSON)
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","reason":"NotFound",`+
			`"message":"the server could not find the requested resource"}`)
	}))
	t.Cleanup(hubSrv.Close)
	p.hubConfig = &rest.Config{
		Host:            hubSrv.URL,
		TLSClientConfig: rest.TLSClientConfig{Insecure: true},
	}
	var err error
	p.hubDynClient, err = dynamic.NewForConfig(p.hubConfig)
	require.NoError(t, err)
	p.clusterProxyURL = spokeServerURL
	return p
}
