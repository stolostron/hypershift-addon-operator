package manager

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// --- dedicated GET .../hostedclusters/{name}/validate endpoint ---
// No request body: {name}/{ns} come from the path, NodePool arch from the
// "arch" query param, hcp create cluster only ever renders one NodePool at a time.

func Test_handleValidateHostedCluster_WhenHostedClusterDoesNotExistAndNoArch_ItShouldReturn200(t *testing.T) {
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/hostedclusters/my-hc"):
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`)
		default:
			t.Fatalf("unexpected spoke request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Remote-User", "alice")
	p.handleValidateHostedCluster(w, r, "clusters", "my-hc", "spoke-1")

	assert.Equal(t, http.StatusOK, w.Code)
	var status metav1.Status
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status))
	assert.Equal(t, metav1.StatusSuccess, status.Status)
}

func Test_handleValidateHostedCluster_WhenHostedClusterExists_ItShouldReturn409(t *testing.T) {
	hcJSON, err := json.Marshal(&hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "clusters"},
	})
	require.NoError(t, err)

	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/hostedclusters/my-hc"):
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(hcJSON)
		default:
			t.Fatalf("unexpected spoke request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Remote-User", "alice")
	p.handleValidateHostedCluster(w, r, "clusters", "my-hc", "spoke-1")

	assert.Equal(t, http.StatusConflict, w.Code)
}

func Test_handleValidateHostedCluster_WhenArchMismatch_ItShouldReturn400(t *testing.T) {
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, apiPathVersion):
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"platform":"linux/arm64"}`)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/hostedclusters/my-hc"):
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`)
		default:
			t.Fatalf("unexpected spoke request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/?arch=amd64", nil)
	r.Header.Set("X-Remote-User", "alice")
	p.handleValidateHostedCluster(w, r, "clusters", "my-hc", "spoke-1")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "management cluster cpu arch: arm64")
	assert.Contains(t, w.Body.String(), "nodepool cpu arch: amd64")
}

func Test_handleValidateHostedCluster_WhenMultiArchReleaseImage_ItShouldSkipArchCheck(t *testing.T) {
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/hostedclusters/my-hc"):
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`)
		default:
			t.Fatalf("unexpected spoke request: %s %s (arch check should be skipped, /version must not be called)",
				r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet,
		"/?arch=amd64&releaseImage=quay.io/openshift-release-dev/ocp-release:4.16.0-multi", nil)
	r.Header.Set("X-Remote-User", "alice")
	p.handleValidateHostedCluster(w, r, "clusters", "my-hc", "spoke-1")

	assert.Equal(t, http.StatusOK, w.Code)
}

// --- routing: GET .../hostedclusters/{name}/validate ---

func Test_handleRoute_WhenValidateGet_ItShouldDispatchToHandleValidateHostedCluster(t *testing.T) {
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/hostedclusters/"):
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`)
		default:
			t.Fatalf("unexpected spoke request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)

	url := "/apis/" + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion +
		"/namespaces/clusters/hostedclusters/my-hc/validate?hostingCluster=spoke-1"
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, url, nil)
	r.Header.Set("X-Remote-User", "alice")
	p.handleRoute(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
}

func Test_handleRoute_WhenValidatePosted_ItShouldReturn405(t *testing.T) {
	p := newTestProxyWithSpokeURL(t, "http://unused", availableManagedCluster("spoke-1"))
	url := "/apis/" + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion +
		"/namespaces/clusters/hostedclusters/my-hc/validate?hostingCluster=spoke-1"
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, url, nil)
	r.Header.Set("X-Remote-User", "alice")
	p.handleRoute(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// Note: handleCreate no longer pre-checks name collisions or NodePool arch itself —
// that's now the dedicated GET .../hostedclusters/{name}/validate endpoint's job
// (see the Test_handleValidateHostedCluster_* tests above). Callers that skip
// calling /validate first get whatever response the spoke itself returns.
