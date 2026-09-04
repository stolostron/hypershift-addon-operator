package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperutil "github.com/openshift/hypershift/support/util"
	"github.com/openshift/hypershift/support/releaseinfo/registryclient"
)

const (
	defaultNodePoolArch = hypershiftv1beta1.ArchitectureAMD64
	apiPathVersion      = "/version"
)

// isMultiArchReleaseImageFunc checks whether a release image is a multi-arch manifest list.
// Overridable in unit tests to avoid registry calls.
var isMultiArchReleaseImageFunc = func(
	ctx context.Context,
	image string,
	pullSecret []byte,
) (bool, error) {
	return registryclient.IsMultiArchManifestList(
		ctx,
		image,
		pullSecret,
		&hyperutil.RegistryClientImageMetadataProvider{},
	)
}

type k8sVersionInfo struct {
	Platform string `json:"platform"`
}

// validateCreateRequest runs hosting-cluster pre-create checks before any resources
// are applied on the spoke. Mirrors core's validateClusterExistence and
// validateMgmtClusterAndNodePoolCPUArchitectures for hcp from-hub create.
func (p *hcpProxy) validateCreateRequest(
	ctx context.Context,
	hcpClient *http.Client,
	spokeName, ns string,
	req *CreateRequest,
) (int, string) {
	if status, msg := p.validateHostedClusterNotExists(ctx, hcpClient, spokeName, ns, req.HostedCluster.Name); status != 0 {
		return status, msg
	}
	if status, msg := p.validateNodePoolArchitectures(ctx, hcpClient, spokeName, req); status != 0 {
		return status, msg
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

func (p *hcpProxy) validateNodePoolArchitectures(
	ctx context.Context,
	hcpClient *http.Client,
	spokeName string,
	req *CreateRequest,
) (int, string) {
	if req.HostedCluster == nil || len(req.NodePools) == 0 {
		return 0, ""
	}

	hostingArch, err := p.fetchHostingClusterCPUArch(ctx, hcpClient, spokeName)
	if err != nil {
		return http.StatusBadGateway, "failed to check hosting cluster CPU arch: " + err.Error()
	}

	pullSecret := pullSecretBytesFromCreateRequest(req)
	releaseImage := strings.TrimSpace(req.HostedCluster.Spec.Release.Image)
	multiArch, multiArchErr := isMultiArchRelease(ctx, releaseImage, pullSecret)
	if multiArchErr != nil {
		if isManifestAccessError(multiArchErr) {
			p.log.Info(
				"WARNING: Unable to access the release payload, skipping the Architectures check.",
				"error", multiArchErr.Error(),
			)
			return 0, ""
		}
		return http.StatusBadRequest, multiArchErr.Error()
	}
	if multiArch {
		return 0, ""
	}

	for _, np := range req.NodePools {
		if np == nil {
			continue
		}
		npArch := strings.TrimSpace(np.Spec.Arch)
		if npArch == "" {
			npArch = defaultNodePoolArch
		}
		npReleaseImage := strings.TrimSpace(np.Spec.Release.Image)
		if npReleaseImage != "" && npReleaseImage != releaseImage {
			npMultiArch, npErr := isMultiArchRelease(ctx, npReleaseImage, pullSecret)
			if npErr != nil {
				if isManifestAccessError(npErr) {
					p.log.Info(
						"WARNING: Unable to access the release payload, skipping the Architectures check.",
						"error", npErr.Error(),
					)
					continue
				}
				return http.StatusBadRequest, npErr.Error()
			}
			if npMultiArch {
				continue
			}
		}
		if !strings.EqualFold(hostingArch, npArch) {
			return http.StatusBadRequest, fmt.Sprintf(
				"multi-arch hosted cluster is not enabled and "+
					"management cluster and nodepool cpu architectures do not match; "+
					"please use a multi-arch release image or a multi-arch release stream - "+
					"management cluster cpu arch: %s, nodepool cpu arch: %s",
				hostingArch, npArch,
			)
		}
	}
	return 0, ""
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

func pullSecretBytesFromCreateRequest(req *CreateRequest) []byte {
	if req.HostedCluster == nil || req.HostedCluster.Spec.PullSecret.Name == "" {
		return nil
	}
	secretName := req.HostedCluster.Spec.PullSecret.Name
	for i := range req.Secrets {
		if req.Secrets[i].Name != secretName {
			continue
		}
		if data, ok := req.Secrets[i].Data[".dockerconfigjson"]; ok {
			return data
		}
	}
	return nil
}

func isMultiArchRelease(ctx context.Context, releaseImage string, pullSecret []byte) (bool, error) {
	if releaseImage == "" {
		return false, nil
	}
	if strings.Contains(releaseImage, hypershiftv1beta1.ArchitectureMulti) {
		return true, nil
	}
	if len(pullSecret) == 0 {
		return false, nil
	}
	return isMultiArchReleaseImageFunc(ctx, releaseImage, pullSecret)
}

func isManifestAccessError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "failed to retrieve manifest")
}
