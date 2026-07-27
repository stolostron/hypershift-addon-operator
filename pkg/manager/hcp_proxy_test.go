package manager

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/zapr"
	tlspkg "github.com/openshift/controller-runtime-common/pkg/tls"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	mcev1 "github.com/stolostron/backplane-operator/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

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
	// hostedclusters + hostedclusters/resources subresource
	assert.Len(t, resources, 2)
	first := resources[0].(map[string]interface{})
	assert.Equal(t, hcpProxyResource, first["name"])
	second := resources[1].(map[string]interface{})
	assert.Equal(t, hcpProxyResource+"/resources", second["name"])
}

// --- handleRoute ---

func Test_handleRoute_WhenMissingHostingCluster_ItShouldReturn400(t *testing.T) {
	p := newTestProxy(t)
	w := httptest.NewRecorder()
	path := "/apis/" + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion + "/namespaces/clusters/hostedclusters"
	r := httptest.NewRequest(http.MethodGet, path, nil) // no ?hostingCluster
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

func Test_checkHubPermission_WhenClusterviewAPIAbsent_ItShouldSkipAndAllow(t *testing.T) {
	// Simulates a kind/non-ACM hub: every request returns 404 with the
	// "server could not find the requested resource" message, meaning the API group
	// is not installed at all — the check is skipped non-fatally.
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
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		postedBodies = append(postedBodies, body)
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
		},
	}
	np := &hypershiftv1beta1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "my-hc-us-east-1a"},
		Spec:       hypershiftv1beta1.NodePoolSpec{ClusterName: "my-hc"},
	}
	body, _ := json.Marshal(CreateRequest{
		HostedCluster: hc,
		NodePools:     []*hypershiftv1beta1.NodePool{np},
		Secrets: []corev1.Secret{
			{ObjectMeta: metav1.ObjectMeta{Name: "my-hc-pull-secret"},
				Data: map[string][]byte{".dockerconfigjson": []byte(`{}`)}},
		},
	})
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

	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: "clusters"}, Data: map[string][]byte{"key": []byte("val")}}
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

	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: "clusters"}, Data: map[string][]byte{"key": []byte("val")}}
	err = p.createOrUpdateSecretOnSpoke(context.Background(), client, "spoke-1", "clusters", secret)
	require.NoError(t, err)
	assert.Equal(t, []string{http.MethodPost}, methods)
}

// --- helpers / middleware / URL defaults ---

func Test_writeJSONError_WhenCalled_ItShouldSetNoSniffHeader(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSONError(w, "something went wrong", http.StatusBadRequest)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, contentTypeJSON, w.Header().Get(headerContentType))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "something went wrong", body["error"])
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
		errCh <- StartHCPProxy(ctx, profile, hubConfig, hubClient, log)
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
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// newTestProxyWithSpokeURL sets clusterProxyURL to the mock server so all spoke
// requests are routed there instead of the real cluster-proxy.
// hubConfig points at a separate mock that reports clusterview as absent so
// checkHubPermission (used by handleRoute) skips non-fatally in unit tests.
func newTestProxyWithSpokeURL(t *testing.T, spokeServerURL string, objs ...runtime.Object) *hcpProxy {
	t.Helper()
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
