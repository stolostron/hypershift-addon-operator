package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const apiPathVersion = "/version"

type k8sVersionInfo struct {
	Platform string `json:"platform"`
}

// handleValidateHostedCluster serves GET .../namespaces/{ns}/hostedclusters/{name}/validate.
// This is the dedicated, side-effect-free pre-create check: callers (hcp from-hub
// create) can call it before rendering/POSTing infrastructure to find out whether
// {name} already exists on the hosting cluster and whether the NodePool arch they
// intend to create matches the hosting cluster's CPU architecture.
//
// No request body — {name}/{ns} come from the path (like every other endpoint) and
// the NodePool arch comes from the "arch" query parameter, since that is data about
// a NodePool that doesn't exist yet and so cannot be looked up. hcp create cluster
// renders exactly one NodePool per invocation (one --arch flag), so a single value
// is enough.
//
// Multi-arch release detection here uses only the "multi" naming convention on
// releaseImage/releaseStream. Registry manifest lookup (IsMultiArchManifestList)
// is performed client-side by hcp from-hub create using local --pull-secret.
//
//	GET .../hostedclusters/{name}/validate?hostingCluster={cluster}
//	    &arch=amd64&releaseImage={image}&releaseStream={stream}
func (p *hcpProxy) handleValidateHostedCluster(w http.ResponseWriter, r *http.Request, ns, name, spokeName string) {
	username, groups := whoIsTheCaller(r)
	hcpClient, err := p.spokeHTTPClient(username, groups)
	if err != nil {
		p.log.Error(err, "failed to build spoke client", "spoke", spokeName)
		p.writeJSONError(w, errMsgFailedSpokeClient+err.Error(), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	if status, msg := p.validateHostedClusterNotExists(ctx, hcpClient, spokeName, ns, name); status != 0 {
		p.writeJSONError(w, msg, status)
		return
	}

	arch := strings.TrimSpace(r.URL.Query().Get("arch"))
	releaseImage := strings.TrimSpace(r.URL.Query().Get("releaseImage"))
	releaseStream := strings.TrimSpace(r.URL.Query().Get("releaseStream"))
	if status, msg := p.validateArchAgainstHostingCluster(
		ctx, hcpClient, spokeName, arch, releaseImage, releaseStream,
	); status != 0 {
		p.writeJSONError(w, msg, status)
		return
	}

	w.Header().Set(headerContentType, contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(metav1.Status{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"},
		Status:   metav1.StatusSuccess,
		Message:  "validation passed",
	})
}

// validateArchAgainstHostingCluster checks the requested NodePool arch against the
// hosting cluster's CPU architecture, skipping the check when releaseImage or
// releaseStream names a multi-arch payload (contains "multi").
func (p *hcpProxy) validateArchAgainstHostingCluster(
	ctx context.Context,
	hcpClient *http.Client,
	spokeName string,
	arch, releaseImage, releaseStream string,
) (int, string) {
	if arch == "" {
		return 0, ""
	}

	if isMultiArchByNaming(releaseImage, releaseStream) {
		return 0, ""
	}

	hostingArch, err := p.fetchHostingClusterCPUArch(ctx, hcpClient, spokeName)
	if err != nil {
		return http.StatusBadGateway, "failed to check hosting cluster CPU arch: " + err.Error()
	}

	if !strings.EqualFold(hostingArch, arch) {
		return http.StatusBadRequest, fmt.Sprintf(
			"multi-arch hosted cluster is not enabled and "+
				"management cluster and nodepool cpu architectures do not match; "+
				"please use a multi-arch release image or a multi-arch release stream - "+
				"management cluster cpu arch: %s, nodepool cpu arch: %s",
			hostingArch, arch,
		)
	}
	return 0, ""
}

func (p *hcpProxy) validateHostedClusterNotExists(
	ctx context.Context,
	hcpClient *http.Client,
	spokeName, ns, name string,
) (int, string) {
	_, status, msg := p.fetchHostedCluster(ctx, hcpClient, ns, name, spokeName)
	switch status {
	case http.StatusNotFound:
		return 0, ""
	case http.StatusOK:
		return http.StatusConflict, fmt.Sprintf("hostedcluster %s/%s already exists", ns, name)
	default:
		return status, msg
	}
}

func (p *hcpProxy) fetchHostingClusterCPUArch(
	ctx context.Context,
	hcpClient *http.Client,
	spokeName string,
) (string, error) {
	versionReq, err := p.newSpokeRequest(ctx, http.MethodGet, spokeName, apiPathVersion, nil)
	if err != nil {
		return "", err
	}
	versionResp, err := doSpokeHTTP(hcpClient, versionReq)
	if err != nil {
		return "", err
	}
	defer versionResp.Body.Close()
	if versionResp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(versionResp.Body)
		if readErr != nil {
			return "", fmt.Errorf("spoke returned %d for /version", versionResp.StatusCode)
		}
		msg := spokeHTTPStatusMessage(body)
		if msg != "" {
			return "", fmt.Errorf("spoke returned %d: %s", versionResp.StatusCode, msg)
		}
		return "", fmt.Errorf("spoke returned %d for /version", versionResp.StatusCode)
	}
	var info k8sVersionInfo
	if err := json.NewDecoder(versionResp.Body).Decode(&info); err != nil {
		return "", fmt.Errorf("failed to decode /version response: %w", err)
	}
	platformParts := strings.Split(info.Platform, "/")
	if len(platformParts) != 2 {
		return "", fmt.Errorf("failed to extract the cpu arch from the platform field")
	}
	return platformParts[1], nil
}

// isMultiArchByNaming reports whether releaseImage or releaseStream names a
// multi-arch payload using the same "multi" substring convention as core create
// validation when no pull secret is available for a registry manifest lookup.
func isMultiArchByNaming(releaseImage, releaseStream string) bool {
	if releaseImage != "" {
		return strings.Contains(releaseImage, hypershiftv1beta1.ArchitectureMulti)
	}
	return releaseStream != "" && strings.Contains(releaseStream, hypershiftv1beta1.ArchitectureMulti)
}
