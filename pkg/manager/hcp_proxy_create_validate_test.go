package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_validateCreateRequest_WhenHostedClusterExists_ItShouldReturn409(t *testing.T) {
	hcJSON, err := json.Marshal(&hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "clusters"},
	})
	require.NoError(t, err)

	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, apiPathVersion):
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"platform":"linux/amd64"}`)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/hostedclusters/my-hc"):
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(hcJSON)
		default:
			w.WriteHeader(http.StatusCreated)
		}
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)
	hcpClient, err := p.spokeHTTPClient("alice", nil)
	require.NoError(t, err)

	req := &CreateRequest{
		HostedCluster: &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc"},
		},
	}
	status, msg := p.validateCreateRequest(context.Background(), hcpClient, "spoke-1", "clusters", req)
	assert.Equal(t, http.StatusConflict, status)
	assert.Contains(t, msg, "hostedcluster clusters/my-hc already exists")
}

func Test_validateCreateRequest_WhenArchMismatch_ItShouldReturn400(t *testing.T) {
	orig := isMultiArchReleaseImageFunc
	isMultiArchReleaseImageFunc = func(context.Context, string, []byte) (bool, error) {
		return false, nil
	}
	t.Cleanup(func() { isMultiArchReleaseImageFunc = orig })

	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, apiPathVersion):
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"platform":"linux/arm64"}`)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/hostedclusters/"):
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`)
		default:
			w.WriteHeader(http.StatusCreated)
		}
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)
	hcpClient, err := p.spokeHTTPClient("alice", nil)
	require.NoError(t, err)

	req := &CreateRequest{
		HostedCluster: &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc"},
			Spec: hypershiftv1beta1.HostedClusterSpec{
				Release:    hypershiftv1beta1.Release{Image: "quay.io/openshift-release-dev/ocp-release:4.16.0-x86_64"},
				PullSecret: corev1.LocalObjectReference{Name: "pull"},
			},
		},
		NodePools: []*hypershiftv1beta1.NodePool{{
			ObjectMeta: metav1.ObjectMeta{Name: "pool-1"},
			Spec: hypershiftv1beta1.NodePoolSpec{
				ClusterName: "my-hc",
				Arch:        hypershiftv1beta1.ArchitectureAMD64,
				Release:     hypershiftv1beta1.Release{Image: "quay.io/openshift-release-dev/ocp-release:4.16.0-x86_64"},
			},
		}},
		Secrets: []corev1.Secret{{
			ObjectMeta: metav1.ObjectMeta{Name: "pull"},
			Data:       map[string][]byte{".dockerconfigjson": []byte(`{}`)},
		}},
	}

	status, msg := p.validateCreateRequest(context.Background(), hcpClient, "spoke-1", "clusters", req)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, msg, "management cluster cpu arch: arm64")
	assert.Contains(t, msg, "nodepool cpu arch: amd64")
}

func Test_validateCreateRequest_WhenMultiArchRelease_ItShouldSkipArchCheck(t *testing.T) {
	orig := isMultiArchReleaseImageFunc
	isMultiArchReleaseImageFunc = func(context.Context, string, []byte) (bool, error) {
		return true, nil
	}
	t.Cleanup(func() { isMultiArchReleaseImageFunc = orig })

	spokeSrv := httptest.NewServer(spokeCreatePreflightHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, apiPathVersion) {
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"platform":"linux/arm64"}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)
	hcpClient, err := p.spokeHTTPClient("alice", nil)
	require.NoError(t, err)

	req := &CreateRequest{
		HostedCluster: &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc"},
			Spec: hypershiftv1beta1.HostedClusterSpec{
				Release:    hypershiftv1beta1.Release{Image: "quay.io/openshift-release-dev/ocp-release:4.16.0-multi"},
				PullSecret: corev1.LocalObjectReference{Name: "pull"},
			},
		},
		NodePools: []*hypershiftv1beta1.NodePool{{
			Spec: hypershiftv1beta1.NodePoolSpec{
				ClusterName: "my-hc",
				Arch:        hypershiftv1beta1.ArchitectureAMD64,
			},
		}},
		Secrets: []corev1.Secret{{
			ObjectMeta: metav1.ObjectMeta{Name: "pull"},
			Data:       map[string][]byte{".dockerconfigjson": []byte(`{}`)},
		}},
	}

	status, msg := p.validateCreateRequest(context.Background(), hcpClient, "spoke-1", "clusters", req)
	assert.Equal(t, 0, status, msg)
}

// --- dedicated POST .../hostedclusters/{name}/validate endpoint ---

func Test_handleValidateCreate_WhenHostedClusterDoesNotExist_ItShouldReturn200(t *testing.T) {
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, apiPathVersion):
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"platform":"linux/amd64"}`)
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

	body, err := json.Marshal(CreateRequest{
		HostedCluster: &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc"},
		},
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleValidateCreate(w, r, "clusters", "my-hc", "spoke-1")

	assert.Equal(t, http.StatusOK, w.Code)
	var status metav1.Status
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status))
	assert.Equal(t, metav1.StatusSuccess, status.Status)
}

func Test_handleValidateCreate_WhenHostedClusterExists_ItShouldReturn409(t *testing.T) {
	hcJSON, err := json.Marshal(&hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "clusters"},
	})
	require.NoError(t, err)

	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, apiPathVersion):
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"platform":"linux/amd64"}`)
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

	body, err := json.Marshal(CreateRequest{
		HostedCluster: &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc"},
		},
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleValidateCreate(w, r, "clusters", "my-hc", "spoke-1")

	assert.Equal(t, http.StatusConflict, w.Code)
}

func Test_handleValidateCreate_WhenNameMismatchesPath_ItShouldReturn400(t *testing.T) {
	p := newTestProxy(t, availableManagedCluster("spoke-1"))
	body, err := json.Marshal(CreateRequest{
		HostedCluster: &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "other-name"},
		},
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleValidateCreate(w, r, "clusters", "my-hc", "spoke-1")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "does not match path name")
}

func Test_handleValidateCreate_WhenHostedClusterMissing_ItShouldReturn400(t *testing.T) {
	p := newTestProxy(t, availableManagedCluster("spoke-1"))
	body, _ := json.Marshal(CreateRequest{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleValidateCreate(w, r, "clusters", "my-hc", "spoke-1")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// --- routing: POST .../hostedclusters/{name}/validate ---

func Test_handleRoute_WhenValidatePosted_ItShouldDispatchToHandleValidateCreate(t *testing.T) {
	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, apiPathVersion):
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"platform":"linux/amd64"}`)
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

	body, err := json.Marshal(CreateRequest{
		HostedCluster: &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc"},
		},
	})
	require.NoError(t, err)

	url := "/apis/" + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion +
		"/namespaces/clusters/hostedclusters/my-hc/validate?hostingCluster=spoke-1"
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleRoute(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
}

func Test_handleRoute_WhenValidateGetRequested_ItShouldReturn405(t *testing.T) {
	p := newTestProxyWithSpokeURL(t, "http://unused", availableManagedCluster("spoke-1"))
	url := "/apis/" + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion +
		"/namespaces/clusters/hostedclusters/my-hc/validate?hostingCluster=spoke-1"
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, url, nil)
	r.Header.Set("X-Remote-User", "alice")
	p.handleRoute(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func Test_handleCreate_WhenHostedClusterExists_ItShouldFailBeforePostingResources(t *testing.T) {
	var postedPaths []string
	hcJSON, err := json.Marshal(&hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "clusters"},
	})
	require.NoError(t, err)

	spokeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			postedPaths = append(postedPaths, r.URL.Path)
		}
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, apiPathVersion):
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"platform":"linux/amd64"}`)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/hostedclusters/my-hc"):
			w.Header().Set(headerContentType, contentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(hcJSON)
		default:
			w.WriteHeader(http.StatusCreated)
		}
	}))
	t.Cleanup(spokeSrv.Close)

	mc := availableManagedCluster("spoke-1")
	p := newTestProxyWithSpokeURL(t, spokeSrv.URL, mc)
	body, err := json.Marshal(CreateRequest{
		HostedCluster: &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "my-hc"},
		},
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("X-Remote-User", "alice")
	p.handleCreate(w, r, "clusters", "spoke-1")

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Empty(t, postedPaths, "validation must fail before any spoke POST")
}
