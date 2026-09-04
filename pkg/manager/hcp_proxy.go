package manager

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	tlspkg "github.com/openshift/controller-runtime-common/pkg/tls"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	libgocrypto "github.com/openshift/library-go/pkg/crypto"
	mcev1 "github.com/stolostron/backplane-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	hcpProxyServiceName = "hypershift-addon-hcp-proxy"
	hcpProxyAPIGroup    = "hcp.ocm.io"
	hcpProxyAPIVersion  = "v1alpha1"
	// Same plural as hypershift.openshift.io HostedCluster. Unqualified
	// `oc get hostedclusters` is steered to the native CRD by setting the
	// APIService groupPriorityMinimum to 1 (CRD default is 1000). Callers
	// that want this proxy must use hostedclusters.hcp.ocm.io.
	hcpProxyResource = "hostedclusters"

	// In-cluster Service names/ports.
	// cluster-proxy: operator pod namespace (POD_NAMESPACE / backplane-operator).
	clusterProxyServiceName = "cluster-proxy-addon-user"
	clusterProxyServicePort = 9092

	// Mount path for the Secret created by service-ca-operator (OpenShift only).
	hcpProxyTLSDir = "/etc/hcp-proxy/tls"

	// labelCreatedVia is stamped on every resource created through this proxy.
	labelCreatedVia      = "hcp.ocm.io/created-via"
	labelCreatedViaValue = "hcp-from-hub"

	// labelHostedCluster records the owning HostedCluster name on every related resource.
	labelHostedCluster = "hcp.ocm.io/hostedcluster"

	// Spoke kube-apiserver path prefixes (constants — never built from request input).
	apiPathPrefix         = "/apis/"
	apiPathCoreNamespaces = "/api/v1/namespaces"
	apiPathHSNamespaces   = "/apis/hypershift.openshift.io/v1beta1/namespaces"

	headerContentType     = "Content-Type"
	contentTypeJSON       = "application/json"
	mergePatchContentType = "application/merge-patch+json"

	errMsgFailedSpokeClient  = "failed to build spoke client: "
	errMsgInvalidNamespace   = "invalid namespace: "
	errMsgInvalidRequestBody = "invalid request body: "
	errMsgMethodNotAllowed   = "method not allowed"

	resourceNodePools      = "nodepools"
	resourceHostedClusters = "hostedclusters"
	resourceSecrets        = "secrets"

	// Bounds for CreateRequest decoding — prevents unbounded memory use on large payloads.
	maxCreateRequestBytes = 4 * 1024 * 1024 // 4 MiB total body
	maxExtraObjects       = 64
	maxExtraObjectBytes   = 256 * 1024 // 256 KiB per object

	finalizersSubresource = "finalizers"

	// validateSubresource is the dedicated pre-create validation route:
	// POST .../hostedclusters/{name}/validate?hostingCluster={cluster}
	validateSubresource = "validate"

	// hostedClusterDestroyFinalizer matches cmd/cluster/core/destroy.go destroyFinalizer.
	hostedClusterDestroyFinalizer = "openshift.io/destroy-cluster"
	finalizerOpAdd                = "add"
	finalizerOpRemove             = "remove"
	maxFinalizersRequestBytes     = 16 * 1024
)

// Overridable in tests.
var (
	certFilePath = hcpProxyTLSDir + "/tls.crt"
	keyFilePath  = hcpProxyTLSDir + "/tls.key"
	// Port 9443 avoids conflict with library-go controllercmd (:8443) in the same process.
	hcpProxyListenAddr = ":9443"
)

// CreateRequest mirrors the output of `hcp create cluster --render`.
type CreateRequest struct {
	// HostedCluster is required. spec.pullSecret.name must reference a Secret
	// in the Secrets list (same as --render output).
	HostedCluster *hypershiftv1beta1.HostedCluster `json:"hostedCluster"`

	// NodePools is the list of NodePools to create (--render may produce more than one).
	NodePools []*hypershiftv1beta1.NodePool `json:"nodePools,omitempty"`

	// Secrets holds every Secret that --render outputs: pull-secret, ssh-key,
	// and (for cloud platforms) any STS/credential secrets.
	// Each Secret is created on the spoke before the HostedCluster.
	Secrets []corev1.Secret `json:"secrets,omitempty"`

	// ExtraObjects holds non-secret resources from `hcp create cluster --render`
	// that are not HostedCluster/NodePool/Secret (e.g. Agent capi-provider-role
	// Role, --additional-trust-bundle ConfigMap). Applied on the spoke after
	// Secrets and before the HostedCluster so the hypershift-operator can
	// reference them immediately. Created as the impersonated caller; spoke
	// RBAC is the user's, not the manager ServiceAccount.
	ExtraObjects []runtime.RawExtension `json:"extraObjects,omitempty"`
}

// FinalizersRequest is the PATCH body for .../hostedclusters/{name}/finalizers.
type FinalizersRequest struct {
	// Operation is "add" or "remove" for the CLI destroy finalizer
	// (openshift.io/destroy-cluster), matching hcp delete cluster behavior.
	Operation string `json:"operation"`
}

// FinalizersResponse returns the HostedCluster after a finalizers mutation.
type FinalizersResponse struct {
	HostedCluster *hypershiftv1beta1.HostedCluster `json:"hostedCluster"`
}

// ResourceBundle is the response body for GET/POST/PUT .../hostedclusters/{name}/resources.
// Secrets are never included — the pull-secret field in HostedCluster.Spec is a
// LocalObjectReference (name only), so no sensitive data is exposed.
type ResourceBundle struct {
	Namespace     *corev1.Namespace                `json:"namespace,omitempty"`
	HostedCluster *hypershiftv1beta1.HostedCluster `json:"hostedCluster"`
	NodePools     []hypershiftv1beta1.NodePool     `json:"nodePools,omitempty"`
	ExtraObjects  []runtime.RawExtension           `json:"extraObjects,omitempty"`
	Warnings      []string                         `json:"warnings,omitempty"`
}

// hcpProxy holds shared state for the proxy HTTP server.
type hcpProxy struct {
	hubConfig         *rest.Config
	hubClient         client.Client
	hubDynClient      dynamic.Interface // operator-identity client for permission probe; cached at startup
	restMapper        meta.RESTMapper   // discovery-backed mapping for extra object API paths
	operatorNamespace string
	clusterProxyURL   string                  // resolved at startup; overridable in tests
	profileSpec       configv1.TLSProfileSpec // cluster TLS profile applied to server + outbound clients
	log               logr.Logger
}

// StartHCPProxy starts the HCP proxy HTTPS server on :9443.
func StartHCPProxy(
	ctx context.Context,
	profileSpec configv1.TLSProfileSpec,
	hubConfig *rest.Config,
	hubClient client.Client,
	restMapper meta.RESTMapper,
	log logr.Logger,
) error {
	operatorNamespace := resolveOperatorNamespace(ctx, hubClient, log)

	clusterProxyURL := resolveClusterProxyURL(ctx, hubClient, operatorNamespace, log)

	hubDynClient, err := dynamic.NewForConfig(hubConfig)
	if err != nil {
		return fmt.Errorf("failed to create hub dynamic client: %w", err)
	}

	p := &hcpProxy{
		hubConfig:         hubConfig,
		hubClient:         hubClient,
		hubDynClient:      hubDynClient,
		restMapper:        restMapper,
		operatorNamespace: operatorNamespace,
		clusterProxyURL:   clusterProxyURL,
		profileSpec:       profileSpec,
		log:               log,
	}

	// Apply the cluster's APIServer TLS profile (MinVersion + CipherSuites) to the server.
	tlsConfigFn, unsupported := tlspkg.NewTLSConfigFromProfile(profileSpec)
	if len(unsupported) > 0 {
		log.Info("TLS profile contains unsupported ciphers, they will be ignored", "ciphers", unsupported)
	}

	cache := &certCache{operatorNS: operatorNamespace, log: log}

	tlsCfg := &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return cache.getCertificate()
		},
	}
	tlsConfigFn(tlsCfg)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", p.handleHealthz)
	mux.HandleFunc("/readyz", p.handleHealthz)
	mux.HandleFunc(apiPathPrefix+hcpProxyAPIGroup, p.handleDiscovery)
	mux.HandleFunc(apiPathPrefix+hcpProxyAPIGroup+"/"+hcpProxyAPIVersion, p.handleDiscovery)
	mux.HandleFunc(apiPathPrefix+hcpProxyAPIGroup+"/"+hcpProxyAPIVersion+"/", p.handleRoute)

	server := &http.Server{
		Addr:              hcpProxyListenAddr,
		Handler:           p.loggingMiddleware(mux),
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: 30 * time.Second,
	}

	log.Info("starting HCP proxy server", "addr", hcpProxyListenAddr)

	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

// resolveOperatorNamespace returns the MCE target namespace (defaults to multicluster-engine).
func resolveOperatorNamespace(ctx context.Context, hubClient client.Client, log logr.Logger) string {
	ns := "multicluster-engine"
	mceList := &mcev1.MultiClusterEngineList{}
	if err := hubClient.List(ctx, mceList); err == nil && len(mceList.Items) > 0 {
		if mceList.Items[0].Spec.TargetNamespace != "" {
			ns = mceList.Items[0].Spec.TargetNamespace
		}
	} else if err != nil {
		log.Error(err, "failed to list MultiClusterEngine, defaulting namespace to multicluster-engine")
	}
	return ns
}

// resolveClusterProxyURL picks the cluster-proxy base URL:
//  1. CLUSTER_PROXY_URL env (explicit override, e.g. port-forward / local dev)
//  2. OpenShift Route in the operator pod namespace
//  3. In-cluster Service DNS in the operator pod namespace
//
// The namespace comes from the manager pod (operatorNamespace / POD_NAMESPACE);
// backplane-operator deploys cluster-proxy into the same namespace.
func resolveClusterProxyURL(
	ctx context.Context,
	hubClient client.Client,
	operatorNamespace string,
	log logr.Logger,
) string {
	if override := os.Getenv("CLUSTER_PROXY_URL"); override != "" {
		log.Info("cluster-proxy URL overridden by CLUSTER_PROXY_URL env var")
		return override
	}
	ns := clusterProxyNamespace(operatorNamespace)
	if routeURL, err := discoverClusterProxyRouteURL(ctx, hubClient, ns, log); err == nil && routeURL != "" {
		log.Info("using cluster-proxy Route URL", "namespace", ns)
		return routeURL
	}
	url := inClusterServiceURL(clusterProxyServiceName, ns, clusterProxyServicePort, "")
	log.Info("using cluster-proxy Service URL", "namespace", ns)
	return url
}

// defaultClusterProxyURL returns the in-cluster cluster-proxy user server URL for MCE.
func defaultClusterProxyURL() string {
	return inClusterServiceURL(clusterProxyServiceName, clusterProxyNamespace(""), clusterProxyServicePort, "")
}

// clusterProxyNamespace returns the namespace where cluster-proxy is deployed —
// the operator pod namespace (backplane-operator injects POD_NAMESPACE).
func clusterProxyNamespace(operatorNamespace string) string {
	if operatorNamespace != "" {
		return operatorNamespace
	}
	if podNS := os.Getenv("POD_NAMESPACE"); podNS != "" {
		return podNS
	}
	return "multicluster-engine"
}

// inClusterServiceURL builds https://<svc>.<ns>.svc:<port><path>.
func inClusterServiceURL(serviceName, namespace string, port int, path string) string {
	return fmt.Sprintf("https://%s.%s.svc:%d%s", serviceName, namespace, port, path)
}

// discoverClusterProxyRouteURL looks up the cluster-proxy-addon-user OpenShift
// Route in the operator namespace and returns its HTTPS URL.
// Returns ("", nil) if no Route is found (non-OpenShift cluster or route absent).
func discoverClusterProxyRouteURL(
	ctx context.Context,
	hubClient client.Client,
	namespace string,
	log logr.Logger,
) (string, error) {
	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "route.openshift.io",
		Version: "v1",
		Kind:    "Route",
	})

	routeKey := types.NamespacedName{Namespace: namespace, Name: clusterProxyServiceName}
	if err := hubClient.Get(ctx, routeKey, route); err != nil {
		log.Info("cluster-proxy Route not found, falling back to in-cluster service DNS",
			"namespace", namespace)
		return "", nil
	}
	host, found, err := unstructured.NestedString(route.Object, "spec", "host")
	if err != nil || !found || host == "" {
		log.Info("cluster-proxy Route has no host, falling back to in-cluster service DNS", "namespace", namespace)
		return "", nil
	}
	return "https://" + host, nil
}

// certCache provides a synchronized certificate source for GetCertificate.
// On every TLS handshake it checks whether the service-ca Secret has been
// projected (cheap os.Stat on a tmpfs volume). When found, it loads and caches
// the service-ca cert. When absent, it generates and caches a self-signed
// fallback exactly once (expensive crypto), shared across concurrent handshakes.
type certCache struct {
	mu         sync.Mutex
	operatorNS string
	log        logr.Logger

	// serviceCACert is the cached cert loaded from the mounted Secret.
	serviceCACert *tls.Certificate
	// fallbackCert is a lazily-generated self-signed cert (generated once).
	fallbackCert *tls.Certificate
}

func (c *certCache) getCertificate() (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Always check for the projected service-ca cert so the proxy switches
	// to the real cert as soon as the kubelet projects the Secret volume.
	if _, err := os.Stat(certFilePath); err == nil {
		cert, err := tls.LoadX509KeyPair(certFilePath, keyFilePath)
		if err != nil {
			return nil, fmt.Errorf("load serving certificate during TLS handshake: %w", err)
		}
		c.serviceCACert = &cert
		return c.serviceCACert, nil
	}

	// Service-ca cert not yet available — use cached fallback.
	if c.fallbackCert != nil {
		return c.fallbackCert, nil
	}

	// Generate fallback once; concurrent handshakes are serialized by the mutex.
	c.log.Info("service-ca cert not found, generating self-signed fallback cert", "dir", hcpProxyTLSDir)
	cert, err := generateSelfSignedCert(c.operatorNS)
	if err != nil {
		return nil, fmt.Errorf("generate fallback serving certificate: %w", err)
	}
	c.fallbackCert = &cert
	return c.fallbackCert, nil
}

// loadOrGenerateCert loads the serving cert from the service-ca-operator Secret
// mount (OpenShift), or falls back to a self-signed cert (kind / vanilla k8s).
func loadOrGenerateCert(operatorNS string, log logr.Logger) (tls.Certificate, error) {
	if _, err := os.Stat(certFilePath); err == nil {
		cert, err := tls.LoadX509KeyPair(certFilePath, keyFilePath)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("load service-ca cert from %s: %w", hcpProxyTLSDir, err)
		}
		return cert, nil
	}
	log.Info("service-ca cert not found, generating self-signed fallback cert", "dir", hcpProxyTLSDir)
	return generateSelfSignedCert(operatorNS)
}

// generateSelfSignedCert creates an ephemeral serving cert via library-go crypto.
// Used only when the service-ca-operator Secret is not available (non-OpenShift).
func generateSelfSignedCert(operatorNS string) (tls.Certificate, error) {
	const certLifetime = 2 * 365 * 24 * time.Hour // within library-go's 7200-day limit

	caConfig, err := libgocrypto.MakeSelfSignedCAConfigForDuration(hcpProxyServiceName+"-ca", certLifetime)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create CA: %w", err)
	}
	ca := &libgocrypto.CA{
		Config:          caConfig,
		SerialGenerator: &libgocrypto.RandomSerialGenerator{},
	}

	hostnames := sets.New[string](
		"localhost",
		"127.0.0.1",
		hcpProxyServiceName,
		hcpProxyServiceName+"."+operatorNS,
		hcpProxyServiceName+"."+operatorNS+".svc",
		hcpProxyServiceName+"."+operatorNS+".svc.cluster.local",
	)
	serverConfig, err := ca.MakeServerCert(hostnames, certLifetime)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create server cert: %w", err)
	}

	certPEM, keyPEM, err := serverConfig.GetPEMBytes()
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("encode server cert PEM: %w", err)
	}

	return tls.X509KeyPair(certPEM, keyPEM)
}

// loggingMiddleware logs method and path only (no query string or identity headers).
func (p *hcpProxy) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.log.Info("hcp-proxy request", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

// handleHealthz responds to health/readiness probes.
func (p *hcpProxy) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleDiscovery returns API group / version discovery documents.
func (p *hcpProxy) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(headerContentType, contentTypeJSON)

	if strings.HasSuffix(r.URL.Path, hcpProxyAPIGroup) {
		doc := map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "APIGroup",
			"name":       hcpProxyAPIGroup,
			"versions": []map[string]string{
				{"groupVersion": hcpProxyAPIGroup + "/" + hcpProxyAPIVersion, "version": hcpProxyAPIVersion},
			},
			"preferredVersion": map[string]string{
				"groupVersion": hcpProxyAPIGroup + "/" + hcpProxyAPIVersion,
				"version":      hcpProxyAPIVersion,
			},
		}
		_ = json.NewEncoder(w).Encode(doc)
		return
	}

	// /apis/hcp.ocm.io/v1alpha1
	doc := map[string]interface{}{
		"apiVersion":   "v1",
		"kind":         "APIResourceList",
		"groupVersion": hcpProxyAPIGroup + "/" + hcpProxyAPIVersion,
		"resources": []map[string]interface{}{
			{
				"name":         hcpProxyResource,
				"singularName": "hostedcluster",
				"namespaced":   true,
				"kind":         "HostedCluster",
				"verbs":        []string{"create", "delete", "deletecollection", "get", "list"},
			},
			{
				// Alias subresource: same as GET|PUT /{name} but with an explicit /resources suffix.
				// Both paths return/accept the full ResourceBundle (HostedCluster + NodePools).
				"name":       hcpProxyResource + "/resources",
				"namespaced": true,
				"kind":       "ResourceBundle",
				"verbs":      []string{"get", "update"},
			},
			{
				"name":       hcpProxyResource + "/" + finalizersSubresource,
				"namespaced": true,
				"kind":       "HostedCluster",
				"verbs":      []string{"patch"},
			},
			{
				// Pre-create validation: duplicate HostedCluster name + NodePool
				// CPU architecture, run against the hosting cluster before apply.
				"name":       hcpProxyResource + "/" + validateSubresource,
				"namespaced": true,
				"kind":       "HostedCluster",
				"verbs":      []string{"create"},
			},
		},
	}
	_ = json.NewEncoder(w).Encode(doc)
}

// handleRoute dispatches all /apis/hcp.ocm.io/v1alpha1/... requests.
func (p *hcpProxy) handleRoute(w http.ResponseWriter, r *http.Request) {
	// Watch is not supported — the proxy is a stateless pass-through and
	// cannot maintain long-lived event streams across spoke clusters.
	if r.URL.Query().Get("watch") == "true" {
		p.writeJSONError(w, "watch is not supported by the HCP proxy", http.StatusMethodNotAllowed)
		return
	}

	prefix := apiPathPrefix + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion + "/"
	remaining := strings.TrimPrefix(r.URL.Path, prefix)
	parts := strings.Split(remaining, "/")

	hostingClusterParam := r.URL.Query().Get("hostingCluster")

	// When hostingCluster is completely absent, the Kubernetes namespace
	// controller may be sending collection-level GET (list) or DELETE
	// (delete-collection) requests during namespace cleanup. The same
	// applies to cluster-wide list calls (e.g. "oc get hostedclusters -A").
	// Return an empty HostedClusterList so these callers are not blocked.
	// POST (create) still requires a spoke target → fall through to 400.
	if hostingClusterParam == "" {
		isNamespacedCollection := len(parts) == 3 && parts[0] == "namespaces" && parts[2] == hcpProxyResource
		isClusterWideList := len(parts) == 1 && parts[0] == hcpProxyResource
		if (isNamespacedCollection || isClusterWideList) && (r.Method == http.MethodGet || r.Method == http.MethodDelete) {
			p.handleEmptyCollection(w, r)
			return
		}
	}

	hostingCluster, err := sanitizeProxyName(hostingClusterParam)
	if err != nil {
		p.writeJSONError(w,
			"hostingCluster query parameter is required and must be a valid DNS-1123 subdomain",
			http.StatusBadRequest)
		return
	}

	if err := p.checkSpokeHealth(r.Context(), hostingCluster); err != nil {
		p.writeJSONError(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	username, groups := whoIsTheCaller(r)
	if err := p.checkHubPermission(r.Context(), username, groups, hostingCluster); err != nil {
		p.writeJSONError(w, err.Error(), http.StatusForbidden)
		return
	}

	if len(parts) == 3 && parts[0] == "namespaces" && parts[2] == hcpProxyResource {
		p.dispatchCollection(w, r, parts[1], hostingCluster)
		return
	}

	// PATCH .../namespaces/{ns}/hostedclusters/{name}/finalizers
	isFinalizers := len(parts) == 5 && parts[0] == "namespaces" && parts[2] == hcpProxyResource &&
		parts[4] == finalizersSubresource
	if isFinalizers {
		p.dispatchFinalizers(w, r, parts[1], parts[3], hostingCluster)
		return
	}

	// POST .../namespaces/{ns}/hostedclusters/{name}/validate
	isValidate := len(parts) == 5 && parts[0] == "namespaces" && parts[2] == hcpProxyResource &&
		parts[4] == validateSubresource
	if isValidate {
		p.dispatchValidate(w, r, parts[1], parts[3], hostingCluster)
		return
	}

	// GET|PUT|DELETE .../namespaces/{ns}/hostedclusters/{name}
	// GET/PUT also accept the /resources suffix — both operate on the full bundle.
	isNamed := (len(parts) == 4 || (len(parts) == 5 && parts[4] == "resources")) &&
		parts[0] == "namespaces" && parts[2] == hcpProxyResource
	if isNamed {
		p.dispatchNamed(w, r, parts[1], parts[3], hostingCluster)
		return
	}

	p.writeJSONError(w, "not found", http.StatusNotFound)
}

// dispatchCollection routes collection-scoped /namespaces/{ns}/hostedclusters requests.
func (p *hcpProxy) dispatchCollection(w http.ResponseWriter, r *http.Request, nsRaw, hostingCluster string) {
	ns, err := sanitizeProxyName(nsRaw)
	if err != nil {
		p.writeJSONError(w, errMsgInvalidNamespace+err.Error(), http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPost:
		p.handleCreate(w, r, ns, hostingCluster)
	default:
		p.writeJSONError(w, errMsgMethodNotAllowed, http.StatusMethodNotAllowed)
	}
}

// dispatchFinalizers routes PATCH requests on the hostedclusters/finalizers subresource.
func (p *hcpProxy) dispatchFinalizers(w http.ResponseWriter, r *http.Request, nsRaw, nameRaw, hostingCluster string) {
	ns, err := sanitizeProxyName(nsRaw)
	if err != nil {
		p.writeJSONError(w, errMsgInvalidNamespace+err.Error(), http.StatusBadRequest)
		return
	}
	name, err := sanitizeProxyName(nameRaw)
	if err != nil {
		p.writeJSONError(w, "invalid name: "+err.Error(), http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPatch:
		p.handleFinalizers(w, r, ns, name, hostingCluster)
	default:
		p.writeJSONError(w, errMsgMethodNotAllowed, http.StatusMethodNotAllowed)
	}
}

// dispatchValidate routes POST requests on the hostedclusters/validate subresource.
func (p *hcpProxy) dispatchValidate(w http.ResponseWriter, r *http.Request, nsRaw, nameRaw, hostingCluster string) {
	ns, err := sanitizeProxyName(nsRaw)
	if err != nil {
		p.writeJSONError(w, errMsgInvalidNamespace+err.Error(), http.StatusBadRequest)
		return
	}
	name, err := sanitizeProxyName(nameRaw)
	if err != nil {
		p.writeJSONError(w, "invalid name: "+err.Error(), http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPost:
		p.handleValidateCreate(w, r, ns, name, hostingCluster)
	default:
		p.writeJSONError(w, errMsgMethodNotAllowed, http.StatusMethodNotAllowed)
	}
}

// handleFinalizers adds or removes the CLI destroy finalizer on a hosting
// HostedCluster via merge patch, matching cmd/cluster/core/destroy.go behavior.
func (p *hcpProxy) handleFinalizers(w http.ResponseWriter, r *http.Request, ns, name, spokeName string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFinalizersRequestBytes)
	var req FinalizersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		p.writeJSONError(w, errMsgInvalidRequestBody+err.Error(), http.StatusBadRequest)
		return
	}
	op := strings.TrimSpace(strings.ToLower(req.Operation))
	if op != finalizerOpAdd && op != finalizerOpRemove {
		p.writeJSONError(w, `operation must be "add" or "remove"`, http.StatusBadRequest)
		return
	}

	username, groups := whoIsTheCaller(r)
	hcpClient, err := p.spokeHTTPClient(username, groups)
	if err != nil {
		p.writeJSONError(w, errMsgFailedSpokeClient+err.Error(), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	const maxConflictRetries = 5
	for attempt := 0; attempt < maxConflictRetries; attempt++ {
		hc, status, errMsg := p.fetchHostedCluster(ctx, hcpClient, ns, name, spokeName)
		if status != http.StatusOK {
			p.writeJSONError(w, errMsg, status)
			return
		}

		newFinalizers, changed, computeErr := computeDestroyFinalizers(hc, op)
		if computeErr != nil {
			p.writeJSONError(w, computeErr.Error(), http.StatusBadRequest)
			return
		}
		if !changed {
			p.writeFinalizersResponse(w, hc)
			return
		}

		hcPath, pathErr := hsNamedAPIPath(ns, resourceHostedClusters, name)
		if pathErr != nil {
			p.writeJSONError(w, pathErr.Error(), http.StatusBadRequest)
			return
		}
		patchStatus, patchErr := p.patchHostedClusterFinalizersOnSpoke(
			ctx, hcpClient, spokeName, hcPath, hc.ResourceVersion, newFinalizers,
		)
		if patchStatus == http.StatusConflict {
			continue
		}
		if patchErr != nil {
			p.writeJSONError(w, "failed to patch HostedCluster finalizers: "+patchErr.Error(), http.StatusBadGateway)
			return
		}
		switch {
		case patchStatus == http.StatusOK:
			updated, fetchStatus, fetchErrMsg := p.fetchHostedCluster(ctx, hcpClient, ns, name, spokeName)
			if fetchStatus != http.StatusOK {
				p.writeJSONError(w, fetchErrMsg, fetchStatus)
				return
			}
			p.writeFinalizersResponse(w, updated)
			return
		default:
			p.writeJSONError(
				w,
				fmt.Sprintf("spoke returned %d patching HostedCluster finalizers", patchStatus),
				http.StatusBadGateway,
			)
			return
		}
	}
	p.writeJSONError(w, "HostedCluster finalizers conflict after retries", http.StatusConflict)
}

// computeDestroyFinalizers returns the updated finalizer list for add/remove of the CLI destroy finalizer.
func computeDestroyFinalizers(hc *hypershiftv1beta1.HostedCluster, op string) ([]string, bool, error) {
	finalizers := append([]string(nil), hc.Finalizers...)
	switch op {
	case finalizerOpAdd:
		if hc.DeletionTimestamp != nil {
			return nil, false, fmt.Errorf("cannot add finalizer while HostedCluster is deleting")
		}
		if sets.New(finalizers...).Has(hostedClusterDestroyFinalizer) {
			return finalizers, false, nil
		}
		return append(finalizers, hostedClusterDestroyFinalizer), true, nil
	case finalizerOpRemove:
		if !sets.New(finalizers...).Has(hostedClusterDestroyFinalizer) {
			return finalizers, false, nil
		}
		out := make([]string, 0, len(finalizers))
		for _, f := range finalizers {
			if f != hostedClusterDestroyFinalizer {
				out = append(out, f)
			}
		}
		return out, true, nil
	default:
		return nil, false, fmt.Errorf("unsupported operation %q", op)
	}
}

// patchHostedClusterFinalizersOnSpoke merge-patches finalizers on the spoke HostedCluster.
func (p *hcpProxy) patchHostedClusterFinalizersOnSpoke(
	ctx context.Context,
	httpClient *http.Client,
	spokeName, hcPath, resourceVersion string,
	finalizers []string,
) (int, error) {
	metadata := map[string]interface{}{
		"finalizers": finalizers,
	}
	if resourceVersion != "" {
		metadata["resourceVersion"] = resourceVersion
	}
	patchBody, err := json.Marshal(map[string]interface{}{
		"metadata": metadata,
	})
	if err != nil {
		return 0, fmt.Errorf("marshal merge patch: %w", err)
	}
	req, err := p.newSpokeRequest(ctx, http.MethodPatch, spokeName, hcPath, bytes.NewReader(patchBody))
	if err != nil {
		return 0, fmt.Errorf("create PATCH request for %s: %w", hcPath, err)
	}
	req.Header.Set(headerContentType, mergePatchContentType)
	resp, err := doSpokeHTTP(httpClient, req)
	if err != nil {
		return 0, fmt.Errorf("PATCH %s: %w", hcPath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return resp.StatusCode, fmt.Errorf("read spoke response for PATCH %s: %w", hcPath, readErr)
		}
		if msg := spokeHTTPStatusMessage(respBody); msg != "" {
			return resp.StatusCode, fmt.Errorf("spoke returned %d: %s", resp.StatusCode, msg)
		}
		return resp.StatusCode, fmt.Errorf("spoke returned %d for PATCH %s", resp.StatusCode, hcPath)
	}
	return resp.StatusCode, nil
}

// writeFinalizersResponse returns the HostedCluster after a finalizers mutation.
func (p *hcpProxy) writeFinalizersResponse(w http.ResponseWriter, hc *hypershiftv1beta1.HostedCluster) {
	w.Header().Set(headerContentType, contentTypeJSON)
	if err := json.NewEncoder(w).Encode(FinalizersResponse{HostedCluster: hc}); err != nil {
		p.log.Error(err, "failed to write finalizers response")
	}
}

// dispatchNamed routes named /namespaces/{ns}/hostedclusters/{name} requests.
func (p *hcpProxy) dispatchNamed(w http.ResponseWriter, r *http.Request, nsRaw, nameRaw, hostingCluster string) {
	ns, err := sanitizeProxyName(nsRaw)
	if err != nil {
		p.writeJSONError(w, errMsgInvalidNamespace+err.Error(), http.StatusBadRequest)
		return
	}
	name, err := sanitizeProxyName(nameRaw)
	if err != nil {
		p.writeJSONError(w, "invalid name: "+err.Error(), http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		p.handleGetResources(w, r, ns, name, hostingCluster)
	case http.MethodPut:
		p.handlePatchResources(w, r, ns, name, hostingCluster)
	case http.MethodDelete:
		p.handleDelete(w, r, ns, name, hostingCluster)
	default:
		p.writeJSONError(w, errMsgMethodNotAllowed, http.StatusMethodNotAllowed)
	}
}

// handleEmptyCollection returns an empty success response for collection-level
// operations (LIST / DELETE-collection) that arrive without a hostingCluster
// query parameter. The Kubernetes namespace controller sends these during
// namespace cleanup to enumerate and remove all resources of every registered
// API type. Since the proxy does not store resources locally (it proxies to
// spoke clusters identified by hostingCluster), an empty list is correct.
func (p *hcpProxy) handleEmptyCollection(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(headerContentType, contentTypeJSON)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"apiVersion": hcpProxyAPIGroup + "/" + hcpProxyAPIVersion,
		"kind":       "HostedClusterList",
		"metadata":   map[string]interface{}{"resourceVersion": ""},
		"items":      []interface{}{},
	})
}

// checkSpokeHealth verifies that the named ManagedCluster is Available.
func (p *hcpProxy) checkSpokeHealth(ctx context.Context, spokeName string) error {
	mc := &clusterv1.ManagedCluster{}
	if err := p.hubClient.Get(ctx, types.NamespacedName{Name: spokeName}, mc); err != nil {
		return fmt.Errorf("managed cluster %q not found: %w", spokeName, err)
	}
	for _, cond := range mc.Status.Conditions {
		if cond.Type == clusterv1.ManagedClusterConditionAvailable {
			if cond.Status == metav1.ConditionTrue {
				return nil
			}
			return fmt.Errorf("managed cluster %q is not available: %s", spokeName, cond.Message)
		}
	}
	return fmt.Errorf("managed cluster %q availability unknown", spokeName)
}

// whoIsTheCaller extracts the authenticated user identity injected by the kube-apiserver.
func whoIsTheCaller(r *http.Request) (username string, groups []string) {
	username = r.Header.Get("X-Remote-User")
	for _, g := range r.Header["X-Remote-Group"] {
		groups = append(groups, strings.Split(g, ",")...)
	}
	return username, groups
}

// checkHubPermission verifies the caller has admin-level access to the hosting cluster
// via the clusterview UserPermission named "managedcluster:admin".
//
// Two-step logic:
//  1. Probe with the operator's own identity (no impersonation) to confirm the
//     clusterview API is installed on this hub. If the API is absent the hub is a
//     dev/kind cluster — skip the check non-fatally so local development still works.
//  2. Re-fetch under the caller's impersonated identity. A 404 at this step means
//     the user does not hold managedcluster:admin on any cluster → hard deny.
//     (View-only callers have a "managedcluster:view" object, not "managedcluster:admin".)
func (p *hcpProxy) checkHubPermission(
	ctx context.Context,
	username string,
	groups []string,
	hostingCluster string,
) error {
	if username == "" {
		return fmt.Errorf("unauthenticated request")
	}

	gvr := schema.GroupVersionResource{
		Group:    "clusterview.open-cluster-management.io",
		Version:  "v1alpha1",
		Resource: "userpermissions",
	}

	// Step 1 — probe API availability using the operator's own credentials (cached client).
	// The userpermissions API is virtual: it returns results scoped to the caller's identity.
	// The operator SA has no managedcluster:admin bindings, so a regular 404 ("not found")
	// is expected and means the API exists. Only a 404 with "the server could not find the
	// requested resource" means the API group itself is absent (kind / non-ACM hub).
	if _, probeErr := p.hubDynClient.Resource(gvr).Get(ctx, "managedcluster:admin", metav1.GetOptions{}); probeErr != nil {
		if apierrors.IsNotFound(probeErr) {
			if strings.Contains(probeErr.Error(), "the server could not find the requested resource") {
				// API group is not registered — only skip in E2E/kind environments.
				// On production clusters this could indicate a partial MCE install failure;
				// skipping would be a security risk.
				if os.Getenv("SKIP_HUB_PERMISSION_CHECK") == "true" {
					p.log.Info("clusterview API not installed, skipping hub permission check (SKIP_HUB_PERMISSION_CHECK=true)")
					return nil
				}
				return fmt.Errorf(
					"UserPermission is required in production. " +
						"Please ensure the cluster has UserPermission configured",
				)
			}
			// API exists but the SA has no admin bindings — expected. Proceed to step 2.
		} else {
			// Fail closed: network/auth/other probe errors must not bypass authorization.
			return fmt.Errorf("clusterview permission probe failed: %w", probeErr)
		}
	}

	// Step 2 — check caller's permissions under impersonation.
	// clusterview API is present; a 404 here means the user is not an admin.
	impConfig := rest.CopyConfig(p.hubConfig)
	impConfig.Impersonate = rest.ImpersonationConfig{
		UserName: username,
		Groups:   groups,
	}
	dynClient, err := dynamic.NewForConfig(impConfig)
	if err != nil {
		return fmt.Errorf("failed to create impersonated client: %w", err)
	}

	item, err := dynClient.Resource(gvr).Get(ctx, "managedcluster:admin", metav1.GetOptions{})
	if err != nil {
		// API exists but the user cannot see this object → not an admin on any cluster.
		return fmt.Errorf("user %q does not have admin access to hosting cluster %q", username, hostingCluster)
	}

	status, ok := item.Object["status"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("user %q does not have admin access to hosting cluster %q", username, hostingCluster)
	}
	bindingList, ok := status["bindings"].([]interface{})
	if !ok {
		return fmt.Errorf("user %q does not have admin access to hosting cluster %q", username, hostingCluster)
	}
	for _, b := range bindingList {
		bMap, ok := b.(map[string]interface{})
		if !ok {
			continue
		}
		if cluster, _ := bMap["cluster"].(string); cluster == hostingCluster {
			return nil
		}
	}
	return fmt.Errorf("user %q does not have admin access to hosting cluster %q", username, hostingCluster)
}

// sanitizeProxyName rejects empty or non-DNS-1123 names so user-controlled path
// segments cannot alter the cluster-proxy host or inject path traversal (SSRF).
func sanitizeProxyName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("name must not be empty")
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return "", fmt.Errorf("invalid name %q: %s", name, strings.Join(errs, ", "))
	}
	return name, nil
}

// validateAPIPath ensures a spoke API path is absolute and cannot escape the
// cluster-proxy base URL (no ".." / scheme injection).
func validateAPIPath(apiPath string) error {
	if apiPath == "" || !strings.HasPrefix(apiPath, "/") {
		return fmt.Errorf("API path must be absolute")
	}
	if strings.Contains(apiPath, "..") || strings.Contains(apiPath, "://") || strings.ContainsAny(apiPath, " \t\r\n\\") {
		return fmt.Errorf("invalid API path")
	}
	return nil
}

// spokeURL builds the cluster-proxy URL for a resource on the spoke.
// Scheme/host come only from the preconfigured base; spokeName and apiPath are
// validated so request input cannot redirect the HTTP client (gosec G704).
func (p *hcpProxy) spokeURL(spokeName, apiPath string) (*url.URL, error) {
	spokeName, err := sanitizeProxyName(spokeName)
	if err != nil {
		return nil, err
	}
	if err := validateAPIPath(apiPath); err != nil {
		return nil, err
	}
	baseStr := p.clusterProxyURL
	if baseStr == "" {
		baseStr = defaultClusterProxyURL()
	}
	base, err := url.Parse(baseStr)
	if err != nil {
		return nil, fmt.Errorf("invalid cluster-proxy base URL: %w", err)
	}
	if (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("invalid cluster-proxy base URL: http(s) scheme and host required")
	}
	// Rebuild from scheme/host + cleaned path so path tricks cannot change host.
	prefix := path.Join("/", strings.Trim(base.Path, "/"), spokeName)
	return &url.URL{Scheme: base.Scheme, Host: base.Host, Path: prefix + apiPath}, nil
}

// newSpokeRequest builds an *http.Request against a validated spoke URL without
// passing a raw URL string into http.NewRequest (SSRF taint sink).
func (p *hcpProxy) newSpokeRequest(
	ctx context.Context,
	method, spokeName, apiPath string,
	body io.Reader,
) (*http.Request, error) {
	u, err := p.spokeURL(spokeName, apiPath)
	if err != nil {
		return nil, err
	}
	req := &http.Request{
		Method: method,
		URL:    u,
		Header: make(http.Header),
		Host:   u.Host,
	}
	if body != nil {
		if rc, ok := body.(io.ReadCloser); ok {
			req.Body = rc
		} else {
			req.Body = io.NopCloser(body)
		}
	}
	return req.WithContext(ctx), nil
}

// cancelOnClose cancels a context when the response body is closed so
// doSpokeHTTP can honor http.Client.Timeout without racing body reads.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

// Close cancels the in-flight spoke HTTP request when the response body is closed.
func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// doSpokeHTTP executes a pre-validated spoke request via RoundTripper.
// gosec G704 flags http.Client.Do / Get / Post as SSRF sinks; RoundTrip is not
// a sink, and the request URL host is always the fixed cluster-proxy base.
func doSpokeHTTP(client *http.Client, req *http.Request) (*http.Response, error) {
	rt := http.DefaultTransport
	if client != nil && client.Transport != nil {
		rt = client.Transport
	}
	if client != nil && client.Timeout > 0 {
		ctx, cancel := context.WithTimeout(req.Context(), client.Timeout)
		req = req.WithContext(ctx)
		resp, err := rt.RoundTrip(req)
		if err != nil {
			cancel()
			return nil, err
		}
		resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
		return resp, nil
	}
	return rt.RoundTrip(req)
}

// coreNamespaceAPIPath returns the cluster-scoped Namespace API path for ns.
func coreNamespaceAPIPath(ns string) (string, error) {
	ns, err := sanitizeProxyName(ns)
	if err != nil {
		return "", err
	}
	return apiPathCoreNamespaces + "/" + ns, nil
}

// hsCollectionAPIPath returns the HyperShift collection API path for ns and resource.
func hsCollectionAPIPath(ns, resource string) (string, error) {
	ns, err := sanitizeProxyName(ns)
	if err != nil {
		return "", err
	}
	switch resource {
	case resourceHostedClusters, resourceNodePools, resourceSecrets:
	default:
		return "", fmt.Errorf("unknown resource type: %s", resource)
	}
	if resource == resourceSecrets {
		return apiPathCoreNamespaces + "/" + ns + "/" + resourceSecrets, nil
	}
	return apiPathHSNamespaces + "/" + ns + "/" + resource, nil
}

// extraObjectCollectionAPIPath builds a namespaced collection path from GVK using
// discovery-backed REST mapping so irregular plurals (e.g. networkpolicies) resolve
// correctly. Cluster-scoped resources are rejected.
func extraObjectCollectionAPIPath(restMapper meta.RESTMapper, ns string, gvk schema.GroupVersionKind) (string, error) {
	if restMapper == nil {
		return "", fmt.Errorf("REST mapper not configured")
	}
	ns, err := sanitizeProxyName(ns)
	if err != nil {
		return "", err
	}
	mapping, err := restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return "", fmt.Errorf("map GVK %s: %w", gvk.String(), err)
	}
	if mapping.Scope.Name() != meta.RESTScopeNameNamespace {
		return "", fmt.Errorf("extra object %s is cluster-scoped", gvk.Kind)
	}
	gvr := mapping.Resource
	version, err := sanitizeProxyName(gvr.Version)
	if err != nil {
		return "", fmt.Errorf("extra object apiVersion: %w", err)
	}
	resource, err := sanitizeProxyName(gvr.Resource)
	if err != nil {
		return "", fmt.Errorf("extra object resource: %w", err)
	}
	if gvr.Group == "" {
		return "/api/" + version + "/namespaces/" + ns + "/" + resource, nil
	}
	group, err := sanitizeProxyName(gvr.Group)
	if err != nil {
		return "", fmt.Errorf("extra object apiGroup: %w", err)
	}
	return apiPathPrefix + group + "/" + version + "/namespaces/" + ns + "/" + resource, nil
}

// extraObjectNamedAPIPath builds a namespaced named-resource path from GVK and name.
func extraObjectNamedAPIPath(restMapper meta.RESTMapper, ns string, obj *unstructured.Unstructured) (string, error) {
	base, err := extraObjectCollectionAPIPath(restMapper, ns, obj.GroupVersionKind())
	if err != nil {
		return "", err
	}
	name, err := sanitizeProxyName(obj.GetName())
	if err != nil {
		return "", err
	}
	return base + "/" + name, nil
}

// hsNamedAPIPath returns the HyperShift named-resource API path for ns, resource, and name.
func hsNamedAPIPath(ns, resource, name string) (string, error) {
	base, err := hsCollectionAPIPath(ns, resource)
	if err != nil {
		return "", err
	}
	name, err = sanitizeProxyName(name)
	if err != nil {
		return "", err
	}
	return base + "/" + name, nil
}

// buildHTTPClient builds an *http.Client using the hub rest.Config for mTLS/auth
// and the cluster TLS profile for MinVersion + CipherSuites. This is the canonical
// way to build outbound HTTP clients so no TLS version is hardcoded.
func (p *hcpProxy) buildHTTPClient(timeout time.Duration) (*http.Client, error) {
	// Build TLS config from rest.Config (CA cert, client cert, server name).
	tlsCfg, err := rest.TLSConfigFor(p.hubConfig)
	if err != nil {
		return nil, fmt.Errorf("TLS config from rest.Config: %w", err)
	}
	// Apply the cluster's OpenShift TLS profile (MinVersion + CipherSuites).
	// No version is hardcoded here — settings come from apiservers.config.openshift.io/cluster.
	tlsConfigFn, _ := tlspkg.NewTLSConfigFromProfile(p.profileSpec)
	tlsConfigFn(tlsCfg)

	// Local dev override: when cluster-proxy is reached via kubectl port-forward the
	// server cert SAN won't match "localhost", so allow skipping TLS verification.
	// Set CLUSTER_PROXY_INSECURE=true only in development — never in production.
	if os.Getenv("CLUSTER_PROXY_INSECURE") == "true" {
		tlsCfg.InsecureSkipVerify = true //nolint:gosec
	}

	base := &http.Transport{
		TLSClientConfig: tlsCfg,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:    100,
		IdleConnTimeout: 90 * time.Second,
	}

	// Wrap base transport with Bearer token / impersonation auth from hub config.
	wrapped, err := rest.HTTPWrappersForConfig(p.hubConfig, base)
	if err != nil {
		return nil, fmt.Errorf("HTTP auth wrappers: %w", err)
	}
	return &http.Client{Transport: wrapped, Timeout: timeout}, nil
}

// spokeHTTPClient builds an http.Client that routes through cluster-proxy
// with Impersonate-User/Group headers for the caller.
func (p *hcpProxy) spokeHTTPClient(username string, groups []string) (*http.Client, error) {
	c, err := p.buildHTTPClient(30 * time.Second)
	if err != nil {
		return nil, fmt.Errorf("%s%w", errMsgFailedSpokeClient, err)
	}
	c.Transport = &impersonatingTransport{
		wrapped:  c.Transport,
		username: username,
		groups:   groups,
	}
	return c, nil
}

// impersonatingTransport injects Impersonate-User/Group headers on every request.
type impersonatingTransport struct {
	wrapped  http.RoundTripper
	username string
	groups   []string
}

// RoundTrip adds Impersonate-User/Group headers before delegating to the base transport.
func (t *impersonatingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if t.username != "" {
		req.Header.Set("Impersonate-User", t.username)
	}
	for _, g := range t.groups {
		req.Header.Add("Impersonate-Group", g)
	}
	return t.wrapped.RoundTrip(req)
}

// handleCreate applies the full set of resources that `hcp create cluster --render`
// produces to the spoke, in the correct dependency order:
//
//  0. Namespace     (auto-created, idempotent — 409 is silently ignored)
//  1. Secrets       (pull-secret, ssh-key, any cloud-provider STS secrets, ...)
//  2. ExtraObjects  (Roles, ConfigMaps, …; 409 ignored; other failures abort)
//  3. HostedCluster (stamped with labelCreatedVia; spec.pullSecret already set by caller)
//  4. NodePool(s)   (each stamped with labelCreatedVia)
//
// The response is the full ResourceBundle so the caller gets every created object
// in one shot without a follow-up GET /resources round-trip.
func (p *hcpProxy) handleCreate(w http.ResponseWriter, r *http.Request, ns, spokeName string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCreateRequestBytes)
	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		p.writeJSONError(w, errMsgInvalidRequestBody+err.Error(), http.StatusBadRequest)
		return
	}
	if req.HostedCluster == nil {
		p.writeJSONError(w, "hostedCluster is required", http.StatusBadRequest)
		return
	}

	extraObjs, extraErr := decodeExtraObjects(req.ExtraObjects)
	if extraErr != nil {
		p.writeJSONError(w, extraErr.Error(), http.StatusBadRequest)
		return
	}

	p.log.Info("creating HostedCluster on spoke",
		"name", req.HostedCluster.Name,
		"namespace", ns,
		"spoke", spokeName,
		"secrets", len(req.Secrets),
		"extraObjects", len(extraObjs),
		"nodePools", len(req.NodePools),
	)

	username, groups := whoIsTheCaller(r)
	hcpClient, err := p.spokeHTTPClient(username, groups)
	if err != nil {
		p.log.Error(err, "failed to build spoke client", "spoke", spokeName)
		p.writeJSONError(w, errMsgFailedSpokeClient+err.Error(), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()

	if status, msg := p.validateCreateRequest(ctx, hcpClient, spokeName, ns, &req); status != 0 {
		p.writeJSONError(w, msg, status)
		return
	}

	hcName := req.HostedCluster.Name

	// addProxyLabels merges the proxy-managed labels into an existing label map.
	addProxyLabels := func(labels map[string]string) map[string]string {
		if labels == nil {
			labels = make(map[string]string)
		}
		labels[labelCreatedVia] = labelCreatedViaValue
		labels[labelHostedCluster] = hcName
		return labels
	}

	// 0. Ensure Namespace (idempotent — 409 means it already exists)
	nsObj := buildNamespace(ns, hcName)
	if err := p.createOnSpoke(ctx, hcpClient, spokeName, ns, "namespaces", nsObj); err != nil && !isAlreadyExists(err) {
		p.logSpokeHTTPFailure("failed to ensure namespace", "namespace", ns, "spoke", spokeName)
		p.writeJSONError(w, "failed to ensure namespace: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 1. Create or update Secrets (pull-secret, ssh-key, STS credentials, …).
	// A 409 means the secret exists from a previous run — update it in place so
	// retries are idempotent and credentials are always fresh.
	for i := range req.Secrets {
		req.Secrets[i].Namespace = ns
		req.Secrets[i].Labels = addProxyLabels(req.Secrets[i].Labels)
		if err := p.createOrUpdateSecretOnSpoke(ctx, hcpClient, spokeName, ns, &req.Secrets[i]); err != nil {
			p.logSpokeHTTPFailure("failed to create/update secret", "secret", req.Secrets[i].Name, "spoke", spokeName)
			p.writeJSONError(w, "failed to create secret: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// 2. Extra objects (Role, ConfigMap, …) before HostedCluster so HO can
	// reference them (e.g. capi-provider-role) as soon as the HC exists.
	var createdExtraObjs []*unstructured.Unstructured
	var appliedExtraObjs []*unstructured.Unstructured
	for _, obj := range extraObjs {
		obj.SetNamespace(ns)
		obj.SetLabels(addProxyLabels(obj.GetLabels()))
		ident := obj.GetKind() + "/" + obj.GetName()
		extraErr := p.createUnstructuredOnSpoke(ctx, hcpClient, spokeName, ns, obj)
		if extraErr != nil {
			if isAlreadyExists(extraErr) {
				appliedExtraObjs = append(appliedExtraObjs, obj.DeepCopy())
				continue
			}
			p.logSpokeHTTPFailure("failed to create extra object", "object", ident, "spoke", spokeName)
			p.rollbackExtraObjectsOnSpoke(ctx, hcpClient, spokeName, ns, createdExtraObjs)
			p.writeJSONError(w, "failed to create extra object "+ident+": "+extraErr.Error(), http.StatusInternalServerError)
			return
		}
		createdExtraObjs = append(createdExtraObjs, obj.DeepCopy())
		appliedExtraObjs = append(appliedExtraObjs, obj.DeepCopy())
	}
	extraObjectsRaw, marshalErr := extraObjectsToRaw(appliedExtraObjs)
	if marshalErr != nil {
		p.logSpokeHTTPFailure("failed to marshal extra objects for response", "spoke", spokeName)
		p.rollbackExtraObjectsOnSpoke(ctx, hcpClient, spokeName, ns, createdExtraObjs)
		p.writeJSONError(w, "failed to marshal extra objects: "+marshalErr.Error(), http.StatusInternalServerError)
		return
	}

	// 3. Create HostedCluster
	//    spec.pullSecret.name / spec.sshKey.name are already set by the caller
	//    (same as --render output) — the proxy does NOT construct those names.
	req.HostedCluster.Namespace = ns
	req.HostedCluster.APIVersion = hypershiftv1beta1.GroupVersion.String()
	req.HostedCluster.Kind = "HostedCluster"
	req.HostedCluster.Labels = addProxyLabels(req.HostedCluster.Labels)
	if err := p.createOnSpoke(ctx, hcpClient, spokeName, ns, resourceHostedClusters, req.HostedCluster); err != nil {
		p.logSpokeHTTPFailure("failed to create HostedCluster", "name", hcName, "spoke", spokeName)
		p.rollbackExtraObjectsOnSpoke(ctx, hcpClient, spokeName, ns, createdExtraObjs)
		p.writeJSONError(w, "failed to create HostedCluster: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 4. Create NodePool(s)
	var createdNodePools []hypershiftv1beta1.NodePool
	var warnings []string
	for i := range req.NodePools {
		np := req.NodePools[i]
		if np == nil {
			continue
		}
		np.Namespace = ns
		np.APIVersion = hypershiftv1beta1.GroupVersion.String()
		np.Kind = "NodePool"
		if np.Spec.ClusterName == "" {
			np.Spec.ClusterName = hcName
		}
		np.Labels = addProxyLabels(np.Labels)
		if err := p.createOnSpoke(ctx, hcpClient, spokeName, ns, resourceNodePools, np); err != nil {
			p.logSpokeHTTPFailure("failed to create NodePool", "name", np.Name, "spoke", spokeName)
			warnings = append(warnings, fmt.Sprintf("NodePool %q creation failed: %s", np.Name, err.Error()))
			continue
		}
		createdNodePools = append(createdNodePools, *np)
	}

	bundle := &ResourceBundle{
		Namespace:     nsObj,
		HostedCluster: req.HostedCluster,
		NodePools:     createdNodePools,
		ExtraObjects:  extraObjectsRaw,
		Warnings:      warnings,
	}

	p.log.Info("HostedCluster created successfully",
		"name", req.HostedCluster.Name,
		"namespace", ns,
		"spoke", spokeName,
		"nodePools", len(createdNodePools),
	)

	w.Header().Set(headerContentType, contentTypeJSON)
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(bundle); err != nil {
		p.log.Error(fmt.Errorf("encode create response: %w", err), "failed to write create response")
	}
}

// handleDelete deletes the HostedCluster and all associated NodePools from the spoke.
func (p *hcpProxy) handleDelete(w http.ResponseWriter, r *http.Request, ns, name, spokeName string) {
	username, groups := whoIsTheCaller(r)
	hcpClient, err := p.spokeHTTPClient(username, groups)
	if err != nil {
		p.writeJSONError(w, errMsgFailedSpokeClient+err.Error(), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	p.deleteMatchingNodePools(ctx, hcpClient, ns, name, spokeName)

	// Delete HostedCluster
	delPath, err := hsNamedAPIPath(ns, resourceHostedClusters, name)
	if err != nil {
		p.writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	delReq, err := p.newSpokeRequest(ctx, http.MethodDelete, spokeName, delPath, nil)
	if err != nil {
		p.writeJSONError(w, "failed to build delete request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	resp, err := doSpokeHTTP(hcpClient, delReq)
	if err != nil {
		p.writeJSONError(w, "spoke request failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get(headerContentType); ct != "" {
		w.Header().Set(headerContentType, ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// deleteMatchingNodePools best-effort deletes NodePools whose spec.clusterName matches hcName.
func (p *hcpProxy) deleteMatchingNodePools(
	ctx context.Context,
	hcpClient *http.Client,
	ns, hcName, spokeName string,
) {
	for _, np := range p.fetchNodePoolsForHC(ctx, hcpClient, ns, hcName, spokeName) {
		p.deleteNodePool(ctx, hcpClient, ns, spokeName, np.Name)
	}
}

func (p *hcpProxy) deleteNodePool(
	ctx context.Context,
	hcpClient *http.Client,
	ns, spokeName, npName string,
) {
	delNPPath, err := hsNamedAPIPath(ns, resourceNodePools, npName)
	if err != nil {
		p.log.Error(err, "skipping NodePool with invalid name", "name", npName)
		return
	}
	delNPReq, err := p.newSpokeRequest(ctx, http.MethodDelete, spokeName, delNPPath, nil)
	if err != nil {
		p.log.Error(err, "failed to build NodePool delete request", "name", npName)
		return
	}
	delNPResp, err := doSpokeHTTP(hcpClient, delNPReq)
	if err != nil {
		p.log.Error(err, "failed to delete NodePool", "name", npName)
		return
	}
	_, _ = io.Copy(io.Discard, delNPResp.Body)
	_ = delNPResp.Body.Close()
}

// handlePatchResources works like kubectl edit: accept a full ResourceBundle,
// PUT each resource back to the spoke (full replace), and return the live bundle.
//
// Workflow mirrors kubectl edit:
//  1. GET .../hostedclusters/{name}/resources  → receive ResourceBundle
//  2. Edit the fields you want to change
//  3. PUT .../hostedclusters/{name}/resources with the modified ResourceBundle
//
// The proxy sends a PUT for the HostedCluster and a PUT for each NodePool present
// in the bundle (identified by metadata.name). Resources absent from the bundle are
// left untouched. Content-Type must be application/json.
func (p *hcpProxy) handlePatchResources(w http.ResponseWriter, r *http.Request, ns, name, spokeName string) {
	var bundle ResourceBundle
	if err := json.NewDecoder(r.Body).Decode(&bundle); err != nil {
		p.writeJSONError(w, errMsgInvalidRequestBody+err.Error(), http.StatusBadRequest)
		return
	}

	username, groups := whoIsTheCaller(r)
	hcpClient, err := p.spokeHTTPClient(username, groups)
	if err != nil {
		p.writeJSONError(w, errMsgFailedSpokeClient+err.Error(), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()

	// PUT HostedCluster (full replace — same as kubectl edit saves)
	if bundle.HostedCluster != nil {
		bundle.HostedCluster.Namespace = ns
		hcPath, pathErr := hsNamedAPIPath(ns, resourceHostedClusters, name)
		if pathErr != nil {
			p.writeJSONError(w, pathErr.Error(), http.StatusBadRequest)
			return
		}
		if err := p.putOnSpoke(ctx, hcpClient, spokeName, hcPath, bundle.HostedCluster); err != nil {
			p.writeJSONError(w, "HostedCluster update failed: "+err.Error(), http.StatusBadGateway)
			return
		}
	}

	// PUT each NodePool present in the bundle (identified by metadata.name)
	for i := range bundle.NodePools {
		np := &bundle.NodePools[i]
		if np.Name == "" {
			continue
		}
		np.Namespace = ns
		npPath, pathErr := hsNamedAPIPath(ns, resourceNodePools, np.Name)
		if pathErr != nil {
			p.writeJSONError(w, fmt.Sprintf("NodePool %q: %s", np.Name, pathErr.Error()), http.StatusBadRequest)
			return
		}
		if err := p.putOnSpoke(ctx, hcpClient, spokeName, npPath, np); err != nil {
			p.writeJSONError(w, fmt.Sprintf("NodePool %q update failed: %s", np.Name, err.Error()), http.StatusBadGateway)
			return
		}
	}

	// Re-fetch the full bundle so the response reflects the live server state.
	p.handleGetResources(w, r, ns, name, spokeName)
}

// putOnSpoke sends a PUT request (full replace) to the spoke kube-apiserver.
func (p *hcpProxy) putOnSpoke(
	ctx context.Context,
	httpClient *http.Client,
	spokeName, apiPath string,
	obj interface{},
) error {
	body, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := p.newSpokeRequest(ctx, http.MethodPut, spokeName, apiPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set(headerContentType, contentTypeJSON)
	resp, err := doSpokeHTTP(httpClient, req)
	if err != nil {
		return fmt.Errorf("PUT %s: %w", apiPath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("read spoke response for PUT %s: %w", apiPath, readErr)
		}
		return spokeHTTPError(resp.StatusCode, apiPath, respBody)
	}
	return nil
}

// handleGetResources returns all K8s resources that make up a HostedCluster:
//   - Namespace (best-effort — omitted if unreachable)
//   - HostedCluster (pull-secret is a reference only; no Secret data is exposed)
//   - NodePools whose spec.clusterName matches the requested HostedCluster
//
// Resources created via this proxy carry the label hcp.ocm.io/created-via=hcp-from-hub.
func (p *hcpProxy) handleGetResources(w http.ResponseWriter, r *http.Request, ns, name, spokeName string) {
	username, groups := whoIsTheCaller(r)
	hcpClient, err := p.spokeHTTPClient(username, groups)
	if err != nil {
		p.writeJSONError(w, errMsgFailedSpokeClient+err.Error(), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	bundle := &ResourceBundle{
		Namespace: p.fetchNamespaceBestEffort(ctx, hcpClient, ns, spokeName),
	}

	hc, status, errMsg := p.fetchHostedCluster(ctx, hcpClient, ns, name, spokeName)
	if status != http.StatusOK {
		p.writeJSONError(w, errMsg, status)
		return
	}
	bundle.HostedCluster = hc
	bundle.NodePools = p.fetchNodePoolsForHC(ctx, hcpClient, ns, name, spokeName)

	w.Header().Set(headerContentType, contentTypeJSON)
	if err := json.NewEncoder(w).Encode(bundle); err != nil {
		p.log.Error(fmt.Errorf("encode get resources response: %w", err), "failed to write get resources response")
	}
}

// fetchNamespaceBestEffort GETs the Namespace from the spoke; returns nil if missing.
func (p *hcpProxy) fetchNamespaceBestEffort(
	ctx context.Context,
	hcpClient *http.Client,
	ns, spokeName string,
) *corev1.Namespace {
	nsPath, err := coreNamespaceAPIPath(ns)
	if err != nil {
		return nil
	}
	nsReq, err := p.newSpokeRequest(ctx, http.MethodGet, spokeName, nsPath, nil)
	if err != nil {
		return nil
	}
	nsResp, err := doSpokeHTTP(hcpClient, nsReq)
	if err != nil {
		return nil
	}
	defer nsResp.Body.Close()
	if nsResp.StatusCode != http.StatusOK {
		return nil
	}
	var namespace corev1.Namespace
	if json.NewDecoder(nsResp.Body).Decode(&namespace) != nil {
		return nil
	}
	return &namespace
}

// fetchHostedCluster GETs a HostedCluster from the spoke by namespace and name.
func (p *hcpProxy) fetchHostedCluster(
	ctx context.Context,
	hcpClient *http.Client,
	ns, name, spokeName string,
) (*hypershiftv1beta1.HostedCluster, int, string) {
	hcPath, err := hsNamedAPIPath(ns, resourceHostedClusters, name)
	if err != nil {
		return nil, http.StatusBadRequest, err.Error()
	}
	hcReq, err := p.newSpokeRequest(ctx, http.MethodGet, spokeName, hcPath, nil)
	if err != nil {
		return nil, http.StatusInternalServerError, "failed to build spoke request: " + err.Error()
	}
	hcResp, err := doSpokeHTTP(hcpClient, hcReq)
	if err != nil {
		return nil, http.StatusBadGateway, "spoke request failed: " + err.Error()
	}
	defer hcResp.Body.Close()
	if hcResp.StatusCode == http.StatusNotFound {
		return nil, http.StatusNotFound, "HostedCluster not found"
	}
	if hcResp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(hcResp.Body)
		if readErr != nil {
			return nil, http.StatusInternalServerError, "failed to read HostedCluster response: " + readErr.Error()
		}
		msg := spokeHTTPStatusMessage(body)
		if msg != "" {
			return nil, http.StatusBadGateway, fmt.Sprintf("spoke returned %d: %s", hcResp.StatusCode, msg)
		}
		return nil, http.StatusBadGateway, fmt.Sprintf("spoke returned %d for HostedCluster", hcResp.StatusCode)
	}
	var hc hypershiftv1beta1.HostedCluster
	if err := json.NewDecoder(hcResp.Body).Decode(&hc); err != nil {
		return nil, http.StatusInternalServerError, "failed to decode HostedCluster: " + err.Error()
	}
	return &hc, http.StatusOK, ""
}

// fetchNodePoolsForHC lists NodePools in ns whose spec.clusterName matches hcName.
func (p *hcpProxy) fetchNodePoolsForHC(
	ctx context.Context,
	hcpClient *http.Client,
	ns, hcName, spokeName string,
) []hypershiftv1beta1.NodePool {
	npPath, err := hsCollectionAPIPath(ns, resourceNodePools)
	if err != nil {
		return nil
	}
	npReq, err := p.newSpokeRequest(ctx, http.MethodGet, spokeName, npPath, nil)
	if err != nil {
		return nil
	}
	npResp, err := doSpokeHTTP(hcpClient, npReq)
	if err != nil {
		return nil
	}
	defer npResp.Body.Close()
	if npResp.StatusCode != http.StatusOK {
		return nil
	}
	var npList hypershiftv1beta1.NodePoolList
	if json.NewDecoder(npResp.Body).Decode(&npList) != nil {
		return nil
	}
	var out []hypershiftv1beta1.NodePool
	for _, np := range npList.Items {
		if np.Spec.ClusterName == hcName {
			out = append(out, np)
		}
	}
	return out
}

// statusCodeInt32 converts an HTTP status code to int32 without integer overflow.
func statusCodeInt32(code int) int32 {
	if code < math.MinInt32 || code > math.MaxInt32 {
		return int32(http.StatusInternalServerError)
	}
	return int32(code)
}

// writeJSONError marshals a Kubernetes Status body before committing the HTTP
// status so a marshal failure cannot leave a truncated response. Callers that
// have an hcpProxy should use (*hcpProxy).writeJSONError so encode/write
// errors are logged.
func writeJSONError(w http.ResponseWriter, msg string, code int) error {
	status := metav1.Status{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Status",
		},
		Status:  metav1.StatusFailure,
		Message: msg,
		Reason:  statusReasonForCode(code),
		Code:    statusCodeInt32(code),
	}
	body, err := json.Marshal(status)
	w.Header().Set(headerContentType, contentTypeJSON)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return fmt.Errorf("marshal Status error response: %w", err)
	}
	w.WriteHeader(code)
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("write Status error response: %w", err)
	}
	return nil
}

// writeJSONError writes a Kubernetes Status error and logs marshal/write
// failures so HTTP handlers can stay one-liners.
func (p *hcpProxy) writeJSONError(w http.ResponseWriter, msg string, code int) {
	if err := writeJSONError(w, msg, code); err != nil {
		p.log.Error(fmt.Errorf("write Status error response: %w", err), "failed to write Status error response")
	}
}

// statusReasonForCode maps an HTTP status code to the Kubernetes StatusReason
// oc/kubectl expect when decoding a Status error body.
func statusReasonForCode(code int) metav1.StatusReason {
	switch code {
	case http.StatusBadRequest:
		return metav1.StatusReasonBadRequest
	case http.StatusForbidden:
		return metav1.StatusReasonForbidden
	case http.StatusNotFound:
		return metav1.StatusReasonNotFound
	case http.StatusMethodNotAllowed:
		return metav1.StatusReasonMethodNotAllowed
	case http.StatusServiceUnavailable:
		return metav1.StatusReasonServiceUnavailable
	case http.StatusConflict:
		return metav1.StatusReasonConflict
	default:
		return metav1.StatusReasonInternalError
	}
}

// buildNamespace constructs a Namespace stamped with the created-via label.
func buildNamespace(name, hcName string) *corev1.Namespace {
	return &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				labelCreatedVia:    labelCreatedViaValue,
				labelHostedCluster: hcName,
			},
		},
	}
}

// errSpokeConflict is returned by createOnSpoke when the spoke responds with 409.
var errSpokeConflict = errors.New("spoke conflict")

// errSpokeLogged is the stable error value for spoke HTTP failures in logs. Callers
// must not pass spoke Status.Message-bearing errors to log.Error (customer data).
var errSpokeLogged = errors.New("spoke HTTP request failed")

// logSpokeHTTPFailure records a spoke operation failure without untrusted error text.
func (p *hcpProxy) logSpokeHTTPFailure(msg string, keysAndValues ...interface{}) {
	p.log.Error(errSpokeLogged, msg, keysAndValues...)
}

// isAlreadyExists reports whether a createOnSpoke error means the resource
// already exists on the spoke (HTTP 409 Conflict).
func isAlreadyExists(err error) bool {
	return errors.Is(err, errSpokeConflict)
}

// createOrUpdateSecretOnSpoke POSTs a Secret; if the spoke returns 409 (already
// exists) it falls back to a PUT so retries are idempotent and credentials stay fresh.
func (p *hcpProxy) createOrUpdateSecretOnSpoke(
	ctx context.Context,
	httpClient *http.Client,
	spokeName, ns string,
	secret *corev1.Secret,
) error {
	err := p.createOnSpoke(ctx, httpClient, spokeName, ns, resourceSecrets, secret)
	if err == nil {
		return nil
	}
	if !isAlreadyExists(err) {
		return err
	}
	// Secret already exists — PUT to update it (keeps data fresh on retries).
	apiPath, pathErr := hsNamedAPIPath(ns, resourceSecrets, secret.Name)
	if pathErr != nil {
		return pathErr
	}
	return p.putOnSpoke(ctx, httpClient, spokeName, apiPath, secret)
}

// createOnSpoke POSTs an object to the spoke kube-apiserver via cluster-proxy.
func (p *hcpProxy) createOnSpoke(
	ctx context.Context,
	httpClient *http.Client,
	spokeName, ns, resource string,
	obj interface{},
) error {
	var apiPath string
	var err error
	switch resource {
	case "namespaces":
		apiPath = apiPathCoreNamespaces // cluster-scoped — no ns prefix
	case resourceSecrets, resourceHostedClusters, resourceNodePools:
		apiPath, err = hsCollectionAPIPath(ns, resource)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown resource type: %s", resource)
	}
	return p.postOnSpoke(ctx, httpClient, spokeName, resource, apiPath, obj)
}

// createUnstructuredOnSpoke POSTs a generic namespaced object (Role, ConfigMap, …)
// to the spoke kube-apiserver via cluster-proxy.
func (p *hcpProxy) createUnstructuredOnSpoke(
	ctx context.Context,
	httpClient *http.Client,
	spokeName, ns string,
	obj *unstructured.Unstructured,
) error {
	apiPath, err := extraObjectCollectionAPIPath(p.restMapper, ns, obj.GroupVersionKind())
	if err != nil {
		return err
	}
	what := obj.GetKind() + "/" + obj.GetName()
	return p.postOnSpoke(ctx, httpClient, spokeName, what, apiPath, obj)
}

// deleteUnstructuredOnSpoke DELETEs a generic namespaced object from the spoke.
// NotFound is treated as success (idempotent rollback).
func (p *hcpProxy) deleteUnstructuredOnSpoke(
	ctx context.Context,
	httpClient *http.Client,
	spokeName, ns string,
	obj *unstructured.Unstructured,
) error {
	apiPath, err := extraObjectNamedAPIPath(p.restMapper, ns, obj)
	if err != nil {
		return err
	}
	what := obj.GetKind() + "/" + obj.GetName()
	req, err := p.newSpokeRequest(ctx, http.MethodDelete, spokeName, apiPath, nil)
	if err != nil {
		return err
	}
	resp, err := doSpokeHTTP(httpClient, req)
	if err != nil {
		return fmt.Errorf("DELETE %s: %w", what, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= 300 {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("read spoke response for DELETE %s: %w", what, readErr)
		}
		return spokeHTTPError(resp.StatusCode, what, respBody)
	}
	return nil
}

// rollbackExtraObjectsOnSpoke best-effort deletes extra objects created during a
// failed create, in reverse order. Errors are logged but not returned.
func (p *hcpProxy) rollbackExtraObjectsOnSpoke(
	ctx context.Context,
	httpClient *http.Client,
	spokeName, ns string,
	created []*unstructured.Unstructured,
) {
	for i := len(created) - 1; i >= 0; i-- {
		obj := created[i]
		ident := obj.GetKind() + "/" + obj.GetName()
		if err := p.deleteUnstructuredOnSpoke(ctx, httpClient, spokeName, ns, obj); err != nil {
			p.logSpokeHTTPFailure("failed to rollback extra object", "object", ident, "spoke", spokeName)
		}
	}
}

// postOnSpoke POSTs a JSON-encoded object to apiPath on the spoke.
func (p *hcpProxy) postOnSpoke(
	ctx context.Context,
	httpClient *http.Client,
	spokeName, what, apiPath string,
	obj interface{},
) error {
	body, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", what, err)
	}

	req, err := p.newSpokeRequest(ctx, http.MethodPost, spokeName, apiPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set(headerContentType, contentTypeJSON)

	resp, err := doSpokeHTTP(httpClient, req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", what, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("read spoke response for %s: %w", what, readErr)
		}
		return spokeHTTPErrorForWhat(resp.StatusCode, what, respBody)
	}
	return nil
}

// spokeHTTPStatusMessage extracts a Kubernetes Status message from a spoke error
// body without logging or returning the raw payload (may contain customer data).
func spokeHTTPStatusMessage(respBody []byte) string {
	if len(respBody) == 0 {
		return ""
	}
	var status metav1.Status
	if err := json.Unmarshal(respBody, &status); err == nil && status.Message != "" {
		return status.Message
	}
	return ""
}

// spokeHTTPError formats a spoke HTTP failure using status code and an optional
// Kubernetes Status message — never the raw response body.
func spokeHTTPError(statusCode int, resourcePath string, respBody []byte) error {
	if msg := spokeHTTPStatusMessage(respBody); msg != "" {
		return fmt.Errorf("spoke returned %d for %s: %s", statusCode, resourcePath, msg)
	}
	return fmt.Errorf("spoke returned %d for %s", statusCode, resourcePath)
}

// spokeHTTPErrorForWhat formats a spoke POST failure, mapping 409 to errSpokeConflict.
func spokeHTTPErrorForWhat(statusCode int, what string, respBody []byte) error {
	if statusCode == http.StatusConflict {
		return fmt.Errorf("%w: spoke returned 409 for %s", errSpokeConflict, what)
	}
	return spokeHTTPError(statusCode, what, respBody)
}

// decodeExtraObjects unmarshals ExtraObjects entries, skipping empty payloads
// and kinds that already have dedicated CreateRequest fields.
func decodeExtraObjects(raws []runtime.RawExtension) ([]*unstructured.Unstructured, error) {
	if len(raws) > maxExtraObjects {
		return nil, fmt.Errorf("extraObjects exceeds maximum of %d", maxExtraObjects)
	}
	out := make([]*unstructured.Unstructured, 0, len(raws))
	for i, raw := range raws {
		if len(raw.Raw) > maxExtraObjectBytes {
			return nil, fmt.Errorf("extraObjects[%d]: object exceeds maximum size of %d bytes", i, maxExtraObjectBytes)
		}
		if len(raw.Raw) == 0 {
			continue
		}
		obj := &unstructured.Unstructured{}
		if err := obj.UnmarshalJSON(raw.Raw); err != nil {
			return nil, fmt.Errorf("extraObjects[%d]: invalid object: %w", i, err)
		}
		gvk := obj.GroupVersionKind()
		if gvk.Kind == "" || gvk.Version == "" {
			return nil, fmt.Errorf("extraObjects[%d]: missing apiVersion or kind", i)
		}
		if isDedicatedCreateGVK(gvk) {
			continue
		}
		if _, err := sanitizeProxyName(obj.GetName()); err != nil {
			return nil, fmt.Errorf("extraObjects[%d]: %w", i, err)
		}
		out = append(out, obj)
	}
	return out, nil
}

// extraObjectsToRaw marshals unstructured extra objects for ResourceBundle responses.
func extraObjectsToRaw(objs []*unstructured.Unstructured) ([]runtime.RawExtension, error) {
	if len(objs) == 0 {
		return nil, nil
	}
	out := make([]runtime.RawExtension, 0, len(objs))
	for i, obj := range objs {
		raw, err := obj.MarshalJSON()
		if err != nil {
			return nil, fmt.Errorf("extraObjects[%d]: %w", i, err)
		}
		out = append(out, runtime.RawExtension{Raw: raw})
	}
	return out, nil
}

// isDedicatedCreateGVK reports whether gvk is represented by a dedicated CreateRequest
// field rather than extraObjects (core Secret/Namespace, HyperShift HC/NodePool).
func isDedicatedCreateGVK(gvk schema.GroupVersionKind) bool {
	switch gvk {
	case schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"},
		schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Namespace"},
		hypershiftv1beta1.GroupVersion.WithKind("HostedCluster"),
		hypershiftv1beta1.GroupVersion.WithKind("NodePool"):
		return true
	default:
		return false
	}
}
