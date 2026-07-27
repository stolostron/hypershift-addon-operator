package manager

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	tlspkg "github.com/openshift/controller-runtime-common/pkg/tls"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	libgocrypto "github.com/openshift/library-go/pkg/crypto"
	mcev1 "github.com/stolostron/backplane-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
	hcpProxyResource    = "hostedclusters"

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

	headerContentType = "Content-Type"
	contentTypeJSON   = "application/json"

	errMsgFailedSpokeClient = "failed to build spoke client: "

	resourceNodePools      = "nodepools"
	resourceHostedClusters = "hostedclusters"
	resourceSecrets        = "secrets"
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
}

// ResourceBundle is the response body for GET/POST/PUT .../hostedclusters/{name}/resources.
// Secrets are never included — the pull-secret field in HostedCluster.Spec is a
// LocalObjectReference (name only), so no sensitive data is exposed.
type ResourceBundle struct {
	Namespace     *corev1.Namespace                `json:"namespace,omitempty"`
	HostedCluster *hypershiftv1beta1.HostedCluster `json:"hostedCluster"`
	NodePools     []hypershiftv1beta1.NodePool     `json:"nodePools,omitempty"`
	Warnings      []string                         `json:"warnings,omitempty"`
}

// hcpProxy holds shared state for the proxy HTTP server.
type hcpProxy struct {
	hubConfig         *rest.Config
	hubClient         client.Client
	hubDynClient      dynamic.Interface       // operator-identity client for permission probe; cached at startup
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
		operatorNamespace: operatorNamespace,
		clusterProxyURL:   clusterProxyURL,
		profileSpec:       profileSpec,
		log:               log,
	}

	cert, err := loadOrGenerateCert(operatorNamespace, log)
	if err != nil {
		return fmt.Errorf("failed to load/generate TLS cert: %w", err)
	}

	// Apply the cluster's APIServer TLS profile (MinVersion + CipherSuites) to the server.
	tlsConfigFn, unsupported := tlspkg.NewTLSConfigFromProfile(profileSpec)
	if len(unsupported) > 0 {
		log.Info("TLS profile contains unsupported ciphers, they will be ignored", "ciphers", unsupported)
	}

	// Identity headers (X-Remote-*) are injected by kube-apiserver over the
	// authenticated aggregated-API connection. ClientAuth/mTLS against the
	// requestheader CA is not enabled here: local e2e and documented curl
	// workflows hit the proxy directly with forged headers on a ClusterIP /
	// port-forward path that is not exposed outside the hub.
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
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

// loadOrGenerateCert loads the serving cert from the service-ca-operator Secret
// mount (OpenShift), or falls back to a self-signed cert (kind / vanilla k8s).
func loadOrGenerateCert(operatorNS string, log logr.Logger) (tls.Certificate, error) {
	if _, err := os.Stat(certFilePath); err == nil {
		cert, err := tls.LoadX509KeyPair(certFilePath, keyFilePath)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("load service-ca cert from %s: %w", hcpProxyTLSDir, err)
		}
		log.Info("loaded serving cert from service-ca Secret", "dir", hcpProxyTLSDir)
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
				"verbs":        []string{"create", "delete", "get"},
			},
			{
				// Alias subresource: same as GET|PUT /{name} but with an explicit /resources suffix.
				// Both paths return/accept the full ResourceBundle (HostedCluster + NodePools).
				"name":       hcpProxyResource + "/resources",
				"namespaced": true,
				"kind":       "ResourceBundle",
				"verbs":      []string{"get", "update"},
			},
		},
	}
	_ = json.NewEncoder(w).Encode(doc)
}

// handleRoute dispatches all /apis/hcp.ocm.io/v1alpha1/... requests.
func (p *hcpProxy) handleRoute(w http.ResponseWriter, r *http.Request) {
	prefix := apiPathPrefix + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion + "/"
	remaining := strings.TrimPrefix(r.URL.Path, prefix)
	parts := strings.Split(remaining, "/")

	hostingCluster, err := sanitizeProxyName(r.URL.Query().Get("hostingCluster"))
	if err != nil {
		writeJSONError(w,
			"hostingCluster query parameter is required and must be a valid DNS-1123 subdomain",
			http.StatusBadRequest)
		return
	}

	if err := p.checkSpokeHealth(r.Context(), hostingCluster); err != nil {
		writeJSONError(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	username, groups := whoIsTheCaller(r)
	if err := p.checkHubPermission(r.Context(), username, groups, hostingCluster); err != nil {
		writeJSONError(w, err.Error(), http.StatusForbidden)
		return
	}

	if len(parts) == 3 && parts[0] == "namespaces" && parts[2] == hcpProxyResource {
		p.dispatchCollection(w, r, parts[1], hostingCluster)
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

	writeJSONError(w, "not found", http.StatusNotFound)
}

func (p *hcpProxy) dispatchCollection(w http.ResponseWriter, r *http.Request, nsRaw, hostingCluster string) {
	ns, err := sanitizeProxyName(nsRaw)
	if err != nil {
		writeJSONError(w, "invalid namespace: "+err.Error(), http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPost:
		p.handleCreate(w, r, ns, hostingCluster)
	default:
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (p *hcpProxy) dispatchNamed(w http.ResponseWriter, r *http.Request, nsRaw, nameRaw, hostingCluster string) {
	ns, err := sanitizeProxyName(nsRaw)
	if err != nil {
		writeJSONError(w, "invalid namespace: "+err.Error(), http.StatusBadRequest)
		return
	}
	name, err := sanitizeProxyName(nameRaw)
	if err != nil {
		writeJSONError(w, "invalid name: "+err.Error(), http.StatusBadRequest)
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
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
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
	if _, probeErr := p.hubDynClient.Resource(gvr).Get(ctx, "managedcluster:admin", metav1.GetOptions{}); probeErr != nil {
		if apierrors.IsNotFound(probeErr) &&
			strings.Contains(probeErr.Error(), "the server could not find the requested resource") {
			// API group is not registered (kind / non-ACM hub) — skip non-fatally.
			p.log.Info("clusterview API not installed, skipping hub permission check")
			return nil
		}
		// Fail closed: network/auth/other probe errors must not bypass authorization.
		return fmt.Errorf("clusterview permission probe failed: %w", probeErr)
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

func coreNamespaceAPIPath(ns string) (string, error) {
	ns, err := sanitizeProxyName(ns)
	if err != nil {
		return "", err
	}
	return apiPathCoreNamespaces + "/" + ns, nil
}

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
//  0. Namespace    (auto-created, idempotent — 409 is silently ignored)
//  1. Secrets      (pull-secret, ssh-key, any cloud-provider STS secrets, ...)
//  2. HostedCluster (stamped with labelCreatedVia; spec.pullSecret already set by caller)
//  3. NodePool(s)  (each stamped with labelCreatedVia)
//
// The response is the full ResourceBundle so the caller gets every created object
// in one shot without a follow-up GET /resources round-trip.
func (p *hcpProxy) handleCreate(w http.ResponseWriter, r *http.Request, ns, spokeName string) {
	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.HostedCluster == nil {
		writeJSONError(w, "hostedCluster is required", http.StatusBadRequest)
		return
	}

	p.log.Info("creating HostedCluster on spoke",
		"name", req.HostedCluster.Name,
		"namespace", ns,
		"spoke", spokeName,
		"secrets", len(req.Secrets),
		"nodePools", len(req.NodePools),
	)

	username, groups := whoIsTheCaller(r)
	hcpClient, err := p.spokeHTTPClient(username, groups)
	if err != nil {
		p.log.Error(err, "failed to build spoke client", "spoke", spokeName)
		writeJSONError(w, errMsgFailedSpokeClient+err.Error(), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()

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
		p.log.Error(err, "failed to ensure namespace", "namespace", ns, "spoke", spokeName)
		writeJSONError(w, "failed to ensure namespace: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 1. Create or update Secrets (pull-secret, ssh-key, STS credentials, …).
	// A 409 means the secret exists from a previous run — update it in place so
	// retries are idempotent and credentials are always fresh.
	for i := range req.Secrets {
		req.Secrets[i].Namespace = ns
		req.Secrets[i].Labels = addProxyLabels(req.Secrets[i].Labels)
		if err := p.createOrUpdateSecretOnSpoke(ctx, hcpClient, spokeName, ns, &req.Secrets[i]); err != nil {
			p.log.Error(err, "failed to create/update secret", "spoke", spokeName)
			writeJSONError(w, "failed to create secret: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// 2. Create HostedCluster
	//    spec.pullSecret.name / spec.sshKey.name are already set by the caller
	//    (same as --render output) — the proxy does NOT construct those names.
	req.HostedCluster.Namespace = ns
	req.HostedCluster.APIVersion = hypershiftv1beta1.GroupVersion.String()
	req.HostedCluster.Kind = "HostedCluster"
	req.HostedCluster.Labels = addProxyLabels(req.HostedCluster.Labels)
	if err := p.createOnSpoke(ctx, hcpClient, spokeName, ns, resourceHostedClusters, req.HostedCluster); err != nil {
		p.log.Error(err, "failed to create HostedCluster", "name", hcName, "spoke", spokeName)
		writeJSONError(w, "failed to create HostedCluster: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 3. Create NodePool(s)
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
			p.log.Error(err, "failed to create NodePool", "name", np.Name)
			warnings = append(warnings, fmt.Sprintf("NodePool %q creation failed: %s", np.Name, err.Error()))
			continue
		}
		createdNodePools = append(createdNodePools, *np)
	}

	bundle := &ResourceBundle{
		Namespace:     nsObj,
		HostedCluster: req.HostedCluster,
		NodePools:     createdNodePools,
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
	_ = json.NewEncoder(w).Encode(bundle)
}

// handleDelete deletes the HostedCluster and all associated NodePools from the spoke.
func (p *hcpProxy) handleDelete(w http.ResponseWriter, r *http.Request, ns, name, spokeName string) {
	username, groups := whoIsTheCaller(r)
	hcpClient, err := p.spokeHTTPClient(username, groups)
	if err != nil {
		writeJSONError(w, errMsgFailedSpokeClient+err.Error(), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	p.deleteMatchingNodePools(ctx, hcpClient, ns, name, spokeName)

	// Delete HostedCluster
	delPath, err := hsNamedAPIPath(ns, resourceHostedClusters, name)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	delReq, err := p.newSpokeRequest(ctx, http.MethodDelete, spokeName, delPath, nil)
	if err != nil {
		writeJSONError(w, "failed to build delete request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	resp, err := doSpokeHTTP(hcpClient, delReq)
	if err != nil {
		writeJSONError(w, "spoke request failed: "+err.Error(), http.StatusBadGateway)
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
		writeJSONError(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	username, groups := whoIsTheCaller(r)
	hcpClient, err := p.spokeHTTPClient(username, groups)
	if err != nil {
		writeJSONError(w, errMsgFailedSpokeClient+err.Error(), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()

	// PUT HostedCluster (full replace — same as kubectl edit saves)
	if bundle.HostedCluster != nil {
		bundle.HostedCluster.Namespace = ns
		hcPath, pathErr := hsNamedAPIPath(ns, resourceHostedClusters, name)
		if pathErr != nil {
			writeJSONError(w, pathErr.Error(), http.StatusBadRequest)
			return
		}
		if err := p.putOnSpoke(ctx, hcpClient, spokeName, hcPath, bundle.HostedCluster); err != nil {
			writeJSONError(w, "HostedCluster update failed: "+err.Error(), http.StatusBadGateway)
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
			writeJSONError(w, fmt.Sprintf("NodePool %q: %s", np.Name, pathErr.Error()), http.StatusBadRequest)
			return
		}
		if err := p.putOnSpoke(ctx, hcpClient, spokeName, npPath, np); err != nil {
			writeJSONError(w, fmt.Sprintf("NodePool %q update failed: %s", np.Name, err.Error()), http.StatusBadGateway)
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
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("spoke returned %d: %s", resp.StatusCode, string(respBody))
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
		writeJSONError(w, errMsgFailedSpokeClient+err.Error(), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	bundle := &ResourceBundle{
		Namespace: p.fetchNamespaceBestEffort(ctx, hcpClient, ns, spokeName),
	}

	hc, status, errMsg := p.fetchHostedCluster(ctx, hcpClient, ns, name, spokeName)
	if status != http.StatusOK {
		writeJSONError(w, errMsg, status)
		return
	}
	bundle.HostedCluster = hc
	bundle.NodePools = p.fetchNodePoolsForHC(ctx, hcpClient, ns, name, spokeName)

	w.Header().Set(headerContentType, contentTypeJSON)
	_ = json.NewEncoder(w).Encode(bundle)
}

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
		body, _ := io.ReadAll(hcResp.Body)
		return nil, http.StatusBadGateway, fmt.Sprintf("spoke returned %d: %s", hcResp.StatusCode, string(body))
	}
	var hc hypershiftv1beta1.HostedCluster
	if err := json.NewDecoder(hcResp.Body).Decode(&hc); err != nil {
		return nil, http.StatusInternalServerError, "failed to decode HostedCluster: " + err.Error()
	}
	return &hc, http.StatusOK, ""
}

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

// writeJSONError writes a JSON-encoded error response {"error": "<msg>"} with the given HTTP status code.
func writeJSONError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set(headerContentType, contentTypeJSON)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
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

	body, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", resource, err)
	}

	req, err := p.newSpokeRequest(ctx, http.MethodPost, spokeName, apiPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set(headerContentType, contentTypeJSON)

	resp, err := doSpokeHTTP(httpClient, req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", resource, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusConflict {
			return fmt.Errorf("%w: spoke returned 409 for %s: %s", errSpokeConflict, resource, string(respBody))
		}
		return fmt.Errorf("spoke returned %d for %s: %s", resp.StatusCode, resource, string(respBody))
	}
	return nil
}
