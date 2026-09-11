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

const (
	apiPathVersion = "/version"

	// maxVersionResponseBytes caps /version reads so a hostile hosting cluster cannot
	// exhaust proxy memory. The real payload is a few hundred bytes of JSON.
	maxVersionResponseBytes = 64 * 1024
)

// k8sVersionInfo is the subset of the Kubernetes /version JSON document used to
// derive the hosting cluster CPU architecture.
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
// Multi-arch release detection here uses only the "-multi" naming convention on
// releaseImage/releaseStream. Registry manifest lookup (IsMultiArchManifestList)
// is performed client-side by hcp from-hub create using local --pull-secret.
//
//	GET .../hostedclusters/{name}/validate?hostingCluster={cluster}
//	    &arch=amd64&releaseImage={image}&releaseStream={stream}
func (p *hcpProxy) handleValidateHostedCluster(w http.ResponseWriter, r *http.Request, ns, name, spokeName string) {
	username, groups := whoIsTheCaller(r)
	hcpClient, err := p.spokeHTTPClient(username, groups)
	if err != nil {
		p.log.Error(err, "failed to build spoke client")
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
	if err := json.NewEncoder(w).Encode(metav1.Status{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"},
		Status:   metav1.StatusSuccess,
		Message:  "validation passed",
	}); err != nil {
		p.log.Error(err, "failed to encode validation response")
	}
}

// validateArchAgainstHostingCluster checks the requested NodePool arch against the
// hosting cluster's CPU architecture, skipping the check when releaseImage or
// releaseStream names a multi-arch payload.
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

// validateHostedClusterNotExists returns a non-zero HTTP status when the named
// HostedCluster already exists on the hosting cluster.
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

// fetchHostingClusterCPUArch reads the hosting cluster /version document and
// returns the CPU architecture from its platform field (e.g. "amd64" from
// "linux/amd64").
func (p *hcpProxy) fetchHostingClusterCPUArch(
	ctx context.Context,
	hcpClient *http.Client,
	spokeName string,
) (string, error) {
	versionReq, err := p.newSpokeRequest(ctx, http.MethodGet, spokeName, apiPathVersion, nil)
	if err != nil {
		return "", fmt.Errorf("build /version request: %w", err)
	}
	versionResp, err := doSpokeHTTP(hcpClient, versionReq)
	if err != nil {
		return "", fmt.Errorf("GET /version: %w", err)
	}
	defer func() {
		if cerr := versionResp.Body.Close(); cerr != nil {
			p.log.Error(cerr, "failed to close /version response body")
		}
	}()

	limitedBody := io.LimitReader(versionResp.Body, maxVersionResponseBytes+1)

	if versionResp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(limitedBody)
		if readErr != nil {
			return "", fmt.Errorf("read /version error response: %w", readErr)
		}
		if len(body) > maxVersionResponseBytes {
			return "", fmt.Errorf("/version error response exceeds %d bytes", maxVersionResponseBytes)
		}
		msg := spokeHTTPStatusMessage(body)
		if msg != "" {
			return "", fmt.Errorf("spoke returned %d for /version: %s", versionResp.StatusCode, msg)
		}
		return "", fmt.Errorf("spoke returned %d for /version", versionResp.StatusCode)
	}

	body, readErr := io.ReadAll(limitedBody)
	if readErr != nil {
		return "", fmt.Errorf("read /version response: %w", readErr)
	}
	if len(body) > maxVersionResponseBytes {
		return "", fmt.Errorf("/version response exceeds %d bytes", maxVersionResponseBytes)
	}
	var info k8sVersionInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return "", fmt.Errorf("failed to decode /version response: %w", err)
	}
	platformParts := strings.Split(info.Platform, "/")
	if len(platformParts) != 2 {
		return "", fmt.Errorf("failed to extract the cpu arch from the platform field")
	}
	return platformParts[1], nil
}

// isMultiArchByNaming reports whether releaseImage or releaseStream names a
// multi-arch payload using the "-multi" segment convention (e.g. 4.16.0-multi,
// 4-stable-multi). Substrings like "notmulti" or registry paths that merely
// contain "multi" are not treated as multi-arch.
func isMultiArchByNaming(releaseImage, releaseStream string) bool {
	return namesMultiArchReleaseImage(releaseImage) || namesMultiArchReleaseStream(releaseStream)
}

// releaseImageTag returns the OCI image tag from releaseImage, or the full
// reference when no tag delimiter is present. The final ':' is a tag separator
// only when it appears after the final '/' so registry ports (e.g. :5000) are
// not mistaken for tags.
func releaseImageTag(releaseImage string) string {
	lastSlash := strings.LastIndex(releaseImage, "/")
	lastColon := strings.LastIndex(releaseImage, ":")
	if lastColon > lastSlash {
		return releaseImage[lastColon+1:]
	}
	return releaseImage
}

// namesMultiArchReleaseImage checks the image tag for a "-multi" suffix.
// Untagged references (no ':' after the final '/') are not classified as multi-arch.
func namesMultiArchReleaseImage(releaseImage string) bool {
	if releaseImage == "" {
		return false
	}
	lastSlash := strings.LastIndex(releaseImage, "/")
	lastColon := strings.LastIndex(releaseImage, ":")
	if lastColon <= lastSlash {
		return false
	}
	tag := releaseImage[lastColon+1:]
	multi := hypershiftv1beta1.ArchitectureMulti
	return tag == multi || strings.HasSuffix(tag, "-"+multi)
}

// namesMultiArchReleaseStream checks the stream name for a "-multi" suffix,
// matching hypershift's getArchFromStream convention.
func namesMultiArchReleaseStream(releaseStream string) bool {
	if releaseStream == "" {
		return false
	}
	return strings.HasSuffix(releaseStream, "-"+hypershiftv1beta1.ArchitectureMulti)
}
