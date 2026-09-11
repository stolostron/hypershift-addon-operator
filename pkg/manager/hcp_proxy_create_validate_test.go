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

// Test_handleValidateHostedCluster_WhenHostedClusterDoesNotExistAndNoArch_ItShouldReturn200
// verifies validation succeeds when the HostedCluster name is free and no arch is requested.
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

	assert.Equal(t, http.StatusOK, w.Code, "validation should return 200 when the HostedCluster does not exist")
	var status metav1.Status
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status), "decode validation success Status body")
	assert.Equal(t, metav1.StatusSuccess, status.Status, "validation success Status should report Success")
}

// Test_handleValidateHostedCluster_WhenHostedClusterExists_ItShouldReturn409
// verifies validation returns 409 when the HostedCluster already exists on the spoke.
func Test_handleValidateHostedCluster_WhenHostedClusterExists_ItShouldReturn409(t *testing.T) {
	hcJSON, err := json.Marshal(&hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-hc", Namespace: "clusters"},
	})
	require.NoError(t, err, "marshal existing HostedCluster fixture")

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

	assert.Equal(t, http.StatusConflict, w.Code, "validation should return 409 when the HostedCluster already exists")
}

// Test_handleValidateHostedCluster_WhenArchMismatch_ItShouldReturn400
// verifies validation returns 400 when the requested NodePool arch differs from the hosting cluster.
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

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"validation should return 400 when NodePool arch mismatches hosting cluster")
	assert.Contains(t, w.Body.String(), "management cluster cpu arch: arm64",
		"arch mismatch error should name the hosting cluster architecture")
	assert.Contains(t, w.Body.String(), "nodepool cpu arch: amd64",
		"arch mismatch error should name the requested NodePool architecture")
}

// Test_handleValidateHostedCluster_WhenMultiArchReleaseImage_ItShouldSkipArchCheck
// verifies a multi-arch release image tag bypasses the /version architecture check.
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

	assert.Equal(t, http.StatusOK, w.Code, "multi-arch release image should skip the arch check")
}

// Test_handleValidateHostedCluster_WhenMultiArchReleaseStream_ItShouldSkipArchCheck
// verifies a multi-arch release stream bypasses the /version architecture check.
func Test_handleValidateHostedCluster_WhenMultiArchReleaseStream_ItShouldSkipArchCheck(t *testing.T) {
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
	r := httptest.NewRequest(http.MethodGet, "/?arch=amd64&releaseStream=4-stable-multi", nil)
	r.Header.Set("X-Remote-User", "alice")
	p.handleValidateHostedCluster(w, r, "clusters", "my-hc", "spoke-1")

	assert.Equal(t, http.StatusOK, w.Code, "multi-arch release stream should skip the arch check")
}

// --- routing: GET .../hostedclusters/{name}/validate ---

// Test_handleRoute_WhenValidateGet_ItShouldDispatchToHandleValidateHostedCluster
// verifies GET .../validate is routed to the validation handler and returns 200 on success.
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

	assert.Equal(t, http.StatusOK, w.Code, "GET /validate route should return 200 when validation passes")
}

// Test_handleRoute_WhenValidatePosted_ItShouldReturn405
// verifies POST .../validate is rejected with 405 Method Not Allowed.
func Test_handleRoute_WhenValidatePosted_ItShouldReturn405(t *testing.T) {
	p := newTestProxyWithSpokeURL(t, "http://unused", availableManagedCluster("spoke-1"))
	url := "/apis/" + hcpProxyAPIGroup + "/" + hcpProxyAPIVersion +
		"/namespaces/clusters/hostedclusters/my-hc/validate?hostingCluster=spoke-1"
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, url, nil)
	r.Header.Set("X-Remote-User", "alice")
	p.handleRoute(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code, "POST /validate should not be supported")
}

// Test_isMultiArchByNaming_WhenReleaseImageContainsMulti_ItShouldReturnTrue
// verifies release image tags ending in -multi are treated as multi-arch.
func Test_isMultiArchByNaming_WhenReleaseImageContainsMulti_ItShouldReturnTrue(t *testing.T) {
	assert.True(t, isMultiArchByNaming("quay.io/openshift-release-dev/ocp-release:4.16.0-multi", ""),
		"release image tag ending in -multi should be treated as multi-arch")
}

// Test_isMultiArchByNaming_WhenReleaseStreamContainsMulti_ItShouldReturnTrue
// verifies release streams ending in -multi are treated as multi-arch.
func Test_isMultiArchByNaming_WhenReleaseStreamContainsMulti_ItShouldReturnTrue(t *testing.T) {
	assert.True(t, isMultiArchByNaming("", "4-stable-multi"),
		"release stream ending in -multi should be treated as multi-arch")
}

// Test_isMultiArchByNaming_WhenReleaseStreamIsMultiButImageIsNot_ItShouldReturnTrue
// verifies a multi-arch stream bypasses arch checks even when the release image is single-arch.
func Test_isMultiArchByNaming_WhenReleaseStreamIsMultiButImageIsNot_ItShouldReturnTrue(t *testing.T) {
	assert.True(t, isMultiArchByNaming("quay.io/openshift-release-dev/ocp-release:4.16.0-x86_64", "4-stable-multi"),
		"multi-arch release stream should bypass arch check even when release image is single-arch")
}

// Test_isMultiArchByNaming_WhenNeitherNamesMulti_ItShouldReturnFalse
// verifies single-arch release image and stream names are not treated as multi-arch.
func Test_isMultiArchByNaming_WhenNeitherNamesMulti_ItShouldReturnFalse(t *testing.T) {
	assert.False(t, isMultiArchByNaming("quay.io/openshift-release-dev/ocp-release:4.16.0-x86_64", "4-stable"),
		"single-arch release image and stream should not be treated as multi-arch")
}

// Test_isMultiArchByNaming_WhenNearMatchNames_ItShouldReturnFalse
// verifies substring matches like notmulti or registry paths containing multi are rejected.
func Test_isMultiArchByNaming_WhenNearMatchNames_ItShouldReturnFalse(t *testing.T) {
	assert.False(t, isMultiArchByNaming("quay.io/multi-registry/ocp-release:4.16.0-x86_64", "4-stable-notmulti"),
		"registry paths or stream names containing 'multi' without a -multi suffix should not match")
}

// Test_releaseImageTag_WhenRegistryPortPresent_ItShouldNotTreatPortAsTag
// verifies registry-port colons are not mistaken for OCI tag delimiters.
func Test_releaseImageTag_WhenRegistryPortPresent_ItShouldNotTreatPortAsTag(t *testing.T) {
	assert.Equal(t, "4.16.0-multi", releaseImageTag("localhost:5000/my/ocp-release:4.16.0-multi"),
		"registry-port references should extract the -multi OCI tag after the final slash")
	assert.Equal(t, "4.16.0-x86_64", releaseImageTag("localhost:5000/my/ocp-release:4.16.0-x86_64"),
		"registry-port references should extract the single-arch OCI tag after the final slash")
}

// Test_isMultiArchByNaming_WhenReleaseImageHasRegistryPort_ItShouldParseTagCorrectly
// verifies multi-arch detection works for images referenced through a registry port.
func Test_isMultiArchByNaming_WhenReleaseImageHasRegistryPort_ItShouldParseTagCorrectly(t *testing.T) {
	assert.True(t, isMultiArchByNaming("localhost:5000/my/ocp-release:4.16.0-multi", ""),
		"registry-port references with a -multi tag should be treated as multi-arch")
	assert.False(t, isMultiArchByNaming("localhost:5000/my/ocp-release:4.16.0-x86_64", ""),
		"registry-port references with a single-arch tag should not be treated as multi-arch")
}

// Test_isMultiArchByNaming_WhenReleaseImageIsUntagged_ItShouldReturnFalse
// verifies untagged repository paths ending in -multi are not treated as multi-arch.
func Test_isMultiArchByNaming_WhenReleaseImageIsUntagged_ItShouldReturnFalse(t *testing.T) {
	assert.False(t, isMultiArchByNaming("quay.io/openshift-release-dev/ocp-release-multi", ""),
		"untagged repository paths ending in -multi should not be treated as multi-arch")
}

// Note: handleCreate no longer pre-checks name collisions or NodePool arch itself —
// that's now the dedicated GET .../hostedclusters/{name}/validate endpoint's job
// (see the Test_handleValidateHostedCluster_* tests above). Callers that skip
// calling /validate first get whatever response the spoke itself returns.
