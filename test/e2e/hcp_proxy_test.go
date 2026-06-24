package e2e_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/stolostron/hypershift-addon-operator/test/e2e/util"
)

const (
	hcpProxyNamespace      = "multicluster-engine"
	clusterProxyNamespace  = "open-cluster-management-addon"
	hcpProxyServiceName    = "hypershift-addon-hcp-proxy"
	hcpProxyAPIServiceName = "v1alpha1.hcp.ocm.io"
	hcpProxyAPIGroup       = "hcp.ocm.io"
	hcpProxyAPIVersion     = "v1alpha1"
	hcpProxyListenPort     = "9443"

	apiServiceGVR = "apiregistration.k8s.io"
)

// proxyURL builds https://host[:port]/path. If host already includes a port
// (e.g. localhost:18443 from kubectl port-forward), it is used as-is.
func proxyURL(host, path string) string {
	if strings.Contains(host, ":") {
		return "https://" + host + path
	}
	return fmt.Sprintf("https://%s:%s%s", host, hcpProxyListenPort, path)
}

var apiServicesGVR = schema.GroupVersionResource{
	Group:    "apiregistration.k8s.io",
	Version:  "v1",
	Resource: "apiservices",
}

var _ = ginkgo.Describe("HCP Proxy", func() {
	var ctx context.Context

	ginkgo.BeforeEach(func() {
		ctx = context.TODO()
	})

	// ----------------------------------------------------------------
	// Proxy health & discovery via direct pod access
	// ----------------------------------------------------------------

	ginkgo.Context("When the proxy pod is running", func() {
		// proxyHost is host or host:port used to reach the proxy server.
		// HCP_PROXY_HOST overrides the pod IP (e.g. "localhost:18443" when
		// kubectl port-forward maps a local port to container :9443).
		var proxyHost string

		ginkgo.BeforeEach(func() {
			// Allow CI to inject a pre-forwarded host via env var
			if h := os.Getenv("HCP_PROXY_HOST"); h != "" {
				proxyHost = h
				return
			}

			ginkgo.By("Finding the addon manager pod IP")
			gomega.Eventually(func() error {
				pods, err := kubeClient.CoreV1().Pods(hcpProxyNamespace).List(ctx, metav1.ListOptions{
					LabelSelector: "app=hypershift-addon-manager",
				})
				if err != nil {
					return err
				}
				for _, pod := range pods.Items {
					if pod.Status.Phase == corev1.PodRunning && pod.Status.PodIP != "" {
						proxyHost = pod.Status.PodIP
						return nil
					}
				}
				return fmt.Errorf("no running addon manager pod found")
			}, eventuallyTimeout, eventuallyInterval).ShouldNot(gomega.HaveOccurred())
		})

		ginkgo.It("should respond to /healthz with 200", func() {
			url := proxyURL(proxyHost, "/healthz")
			ginkgo.By("GET " + url)
			client := insecureHTTPClient()
			resp, err := client.Get(url)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			defer resp.Body.Close()
			gomega.Expect(resp.StatusCode).To(gomega.Equal(http.StatusOK))
			body, _ := io.ReadAll(resp.Body)
			gomega.Expect(string(body)).To(gomega.Equal("ok"))
		})

		ginkgo.It("should respond to /readyz with 200", func() {
			client := insecureHTTPClient()
			resp, err := client.Get(proxyURL(proxyHost, "/readyz"))
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			defer resp.Body.Close()
			gomega.Expect(resp.StatusCode).To(gomega.Equal(http.StatusOK))
		})

		ginkgo.It("should return an APIGroup document from /apis/hcp.ocm.io", func() {
			client := insecureHTTPClient()
			resp, err := client.Get(proxyURL(proxyHost, "/apis/"+hcpProxyAPIGroup))
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			defer resp.Body.Close()
			gomega.Expect(resp.StatusCode).To(gomega.Equal(http.StatusOK))

			var doc map[string]interface{}
			gomega.Expect(json.NewDecoder(resp.Body).Decode(&doc)).To(gomega.Succeed())
			gomega.Expect(doc["kind"]).To(gomega.Equal("APIGroup"))
			gomega.Expect(doc["name"]).To(gomega.Equal(hcpProxyAPIGroup))
		})

		ginkgo.It("should return an APIResourceList from /apis/hcp.ocm.io/v1alpha1", func() {
			client := insecureHTTPClient()
			resp, err := client.Get(proxyURL(proxyHost, "/apis/"+hcpProxyAPIGroup+"/"+hcpProxyAPIVersion))
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			defer resp.Body.Close()
			gomega.Expect(resp.StatusCode).To(gomega.Equal(http.StatusOK))

			var doc map[string]interface{}
			gomega.Expect(json.NewDecoder(resp.Body).Decode(&doc)).To(gomega.Succeed())
			gomega.Expect(doc["kind"]).To(gomega.Equal("APIResourceList"))

			resources := doc["resources"].([]interface{})
			gomega.Expect(resources).To(gomega.HaveLen(2))
			names := []string{}
			for _, r := range resources {
				names = append(names, r.(map[string]interface{})["name"].(string))
			}
			gomega.Expect(names).To(gomega.ContainElements("hostedclusters", "hostedclusters/resources"))
		})

		ginkgo.It("should return 400 when hostingCluster is missing from a spoke request", func() {
			client := insecureHTTPClient()
			url := proxyURL(proxyHost, "/apis/"+hcpProxyAPIGroup+"/"+hcpProxyAPIVersion+"/namespaces/clusters/hostedclusters")
			resp, err := client.Get(url) // no ?hostingCluster
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			defer resp.Body.Close()
			gomega.Expect(resp.StatusCode).To(gomega.Equal(http.StatusBadRequest))
		})

		ginkgo.It("should return 503 when the hosting cluster does not exist", func() {
			client := insecureHTTPClient()
			url := proxyURL(proxyHost, "/apis/"+hcpProxyAPIGroup+"/"+hcpProxyAPIVersion+
				"/namespaces/clusters/hostedclusters?hostingCluster=nonexistent-spoke")
			// Add X-Remote-User so it passes the auth check and fails on health
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			req.Header.Set("X-Remote-User", "e2e-test-user")
			resp, err := client.Do(req)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			defer resp.Body.Close()
			gomega.Expect(resp.StatusCode).To(gomega.Equal(http.StatusServiceUnavailable))
		})

		// POST create validation (400/503) plus a successful 201 create that
		// routes through OCM cluster-proxy (hack/install_cluster_proxy.sh) onto
		// local-cluster with the HostedCluster CRD applied.
		ginkgo.It("should return 400 when POST is missing hostingCluster", func() {
			client := insecureHTTPClient()
			url := proxyURL(proxyHost, "/apis/"+hcpProxyAPIGroup+"/"+hcpProxyAPIVersion+
				"/namespaces/clusters/hostedclusters")
			body := []byte(`{"hostedCluster":{"metadata":{"name":"e2e-hc"}}}`)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Remote-User", "e2e-test-user")
			resp, err := client.Do(req)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			defer resp.Body.Close()
			gomega.Expect(resp.StatusCode).To(gomega.Equal(http.StatusBadRequest))
		})

		ginkgo.It("should return 503 when POST targets a nonexistent hosting cluster", func() {
			client := insecureHTTPClient()
			url := proxyURL(proxyHost, "/apis/"+hcpProxyAPIGroup+"/"+hcpProxyAPIVersion+
				"/namespaces/clusters/hostedclusters?hostingCluster=nonexistent-spoke")
			body := []byte(`{"hostedCluster":{"metadata":{"name":"e2e-hc"}}}`)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Remote-User", "e2e-test-user")
			resp, err := client.Do(req)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			defer resp.Body.Close()
			gomega.Expect(resp.StatusCode).To(gomega.Equal(http.StatusServiceUnavailable))
		})

		ginkgo.It("should return 400 when POST body omits hostedCluster", func() {
			// On kind, clusterview is absent so permission check is skipped;
			// local-cluster is Available and handleCreate rejects the empty body.
			client := insecureHTTPClient()
			url := proxyURL(proxyHost, "/apis/"+hcpProxyAPIGroup+"/"+hcpProxyAPIVersion+
				"/namespaces/clusters/hostedclusters?hostingCluster="+defaultManagedCluster)
			body := []byte(`{}`)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Remote-User", "e2e-test-user")
			resp, err := client.Do(req)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			defer resp.Body.Close()
			gomega.Expect(resp.StatusCode).To(gomega.Equal(http.StatusBadRequest))
		})

		ginkgo.It("should create a HostedCluster via POST through cluster-proxy and return 201", func() {
			ginkgo.By("Ensuring OCM cluster-proxy user Service is present")
			_, err := kubeClient.CoreV1().Services(clusterProxyNamespace).Get(
				ctx, "cluster-proxy-addon-user", metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				ginkgo.Skip("cluster-proxy-addon-user Service missing; run make deploy-cluster-proxy")
			}
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			hcNS := fmt.Sprintf("e2e-hcp-proxy-%d", time.Now().UnixNano())
			const hcName = "e2e-hc"
			ginkgo.DeferCleanup(func() {
				_ = kubeClient.CoreV1().Namespaces().Delete(ctx, hcNS, metav1.DeleteOptions{})
			})

			// system:masters so spoke impersonation can create Namespace/Secret/HostedCluster.
			body := []byte(fmt.Sprintf(`{
			  "hostedCluster": {
			    "apiVersion": "hypershift.openshift.io/v1beta1",
			    "kind": "HostedCluster",
			    "metadata": {"name": %q, "namespace": %q},
			    "spec": {
			      "release": {"image": "quay.io/openshift-release-dev/ocp-release:4.16.0-x86_64"},
			      "pullSecret": {"name": "%s-pull-secret"},
			      "sshKey": {"name": "%s-ssh-key"},
			      "platform": {"type": "None"},
			      "networking": {"networkType": "OVNKubernetes"},
			      "services": [],
			      "etcd": {"managementType": "Managed"},
			      "infraID": %q
			    }
			  },
			  "secrets": [
			    {
			      "apiVersion": "v1",
			      "kind": "Secret",
			      "metadata": {"name": "%s-pull-secret"},
			      "type": "kubernetes.io/dockerconfigjson",
			      "data": {".dockerconfigjson": "eyJhdXRocyI6e319"}
			    },
			    {
			      "apiVersion": "v1",
			      "kind": "Secret",
			      "metadata": {"name": "%s-ssh-key"},
			      "data": {"id_rsa.pub": "c3NoLXJzYSBBQUFB"}
			    }
			  ]
			}`, hcName, hcNS, hcName, hcName, hcName, hcName, hcName))

			ginkgo.By("Waiting for ManagedClusterAddOn cluster-proxy Available")
			gomega.Eventually(func() bool {
				addon, err := addonClient.AddonV1alpha1().ManagedClusterAddOns(defaultManagedCluster).
					Get(ctx, "cluster-proxy", metav1.GetOptions{})
				if err != nil {
					return false
				}
				for _, c := range addon.Status.Conditions {
					if c.Type == "Available" && c.Status == metav1.ConditionTrue {
						return true
					}
				}
				return false
			}, eventuallyTimeout, eventuallyInterval).Should(gomega.BeTrue())

			client := insecureHTTPClient()
			url := proxyURL(proxyHost, "/apis/"+hcpProxyAPIGroup+"/"+hcpProxyAPIVersion+
				"/namespaces/"+hcNS+"/hostedclusters?hostingCluster="+defaultManagedCluster)

			ginkgo.By("POST create HostedCluster via HCP proxy → cluster-proxy → local-cluster")
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Remote-User", "e2e-test-user")
			req.Header.Set("X-Remote-Group", "system:masters")
			resp, err := client.Do(req)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			defer resp.Body.Close()
			respBody, _ := io.ReadAll(resp.Body)
			gomega.Expect(resp.StatusCode).To(gomega.Equal(http.StatusCreated),
				"POST create response: %s", string(respBody))

			var bundle map[string]interface{}
			gomega.Expect(json.Unmarshal(respBody, &bundle)).To(gomega.Succeed())
			hc, ok := bundle["hostedCluster"].(map[string]interface{})
			gomega.Expect(ok).To(gomega.BeTrue(), "response should include hostedCluster")
			meta, _ := hc["metadata"].(map[string]interface{})
			gomega.Expect(meta["name"]).To(gomega.Equal(hcName))
			labels, _ := meta["labels"].(map[string]interface{})
			gomega.Expect(labels["hcp.ocm.io/created-via"]).To(gomega.Equal("hcp-from-hub"))

			ginkgo.By("Verifying HostedCluster exists on the hub/spoke (local-cluster)")
			hcGVR := schema.GroupVersionResource{
				Group:    "hypershift.openshift.io",
				Version:  "v1beta1",
				Resource: "hostedclusters",
			}
			gomega.Eventually(func() error {
				_, err := dynamicClient.Resource(hcGVR).Namespace(hcNS).Get(ctx, hcName, metav1.GetOptions{})
				return err
			}, eventuallyTimeout, eventuallyInterval).ShouldNot(gomega.HaveOccurred())
		})
	})

	// ----------------------------------------------------------------
	// APIService routing via hub kube-apiserver
	// ----------------------------------------------------------------

	ginkgo.Context("When accessed via the hub kube-apiserver APIService route", func() {
		ginkgo.It("should serve the hcp.ocm.io API group in cluster API discovery", func() {
			ginkgo.By("Waiting for APIService to become Available")
			gomega.Eventually(func() bool {
				apiSvc, err := dynamicClient.Resource(apiServicesGVR).Get(
					ctx, hcpProxyAPIServiceName, metav1.GetOptions{})
				if err != nil {
					return false
				}
				conditions, ok := apiSvc.Object["status"].(map[string]interface{})
				if !ok {
					return false
				}
				condList, _ := conditions["conditions"].([]interface{})
				for _, c := range condList {
					cMap, _ := c.(map[string]interface{})
					if cMap["type"] == "Available" && cMap["status"] == "True" {
						return true
					}
				}
				return false
			}, eventuallyTimeout, eventuallyInterval).Should(gomega.BeTrue(),
				"APIService v1alpha1.hcp.ocm.io should become Available")
		})

		ginkgo.It("should expose hcp.ocm.io in /apis discovery via REST client", func() {
			ginkgo.By("Waiting for hcp.ocm.io to appear in server API groups")
			// ServerGroups (not ServerGroupsAndResources): aggregated APIs can
			// make the latter return a partial-error that this suite treated as fail.
			gomega.Eventually(func() bool {
				groups, err := kubeClient.Discovery().ServerGroups()
				if err != nil {
					return false
				}
				for _, g := range groups.Groups {
					if g.Name == hcpProxyAPIGroup {
						return true
					}
				}
				return false
			}, eventuallyTimeout, eventuallyInterval).Should(gomega.BeTrue(),
				"hcp.ocm.io should appear in server API groups")
		})

		ginkgo.It("should return 400 via the APIService route when hostingCluster is absent", func() {
			ginkgo.By("Making raw REST call to /apis/hcp.ocm.io/v1alpha1/namespaces/clusters/hostedclusters")
			restClient, err := util.NewKubeClient()
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			// The proxy returns 400 because hostingCluster is not set;
			// the kube-apiserver may wrap this as a 400 or 503.
			// Either way the call should not succeed with 200.
			gomega.Eventually(func() int {
				var statusCode int
				restClient.CoreV1().RESTClient().Get().
					AbsPath("/apis/hcp.ocm.io/v1alpha1/namespaces/clusters/hostedclusters").
					Do(ctx).StatusCode(&statusCode)
				return statusCode
			}, eventuallyTimeout, eventuallyInterval).ShouldNot(gomega.Equal(http.StatusOK))
		})
	})

	// ----------------------------------------------------------------
	// Proxy Service port liveness
	// ----------------------------------------------------------------

	ginkgo.Context("When the proxy Service is targetted", func() {
		ginkgo.It("should have at least one Ready endpoint backing the Service", func() {
			ginkgo.By("Checking Endpoints for " + hcpProxyServiceName)
			gomega.Eventually(func() bool {
				ep, err := kubeClient.CoreV1().Endpoints(hcpProxyNamespace).Get(
					ctx, hcpProxyServiceName, metav1.GetOptions{})
				if err != nil {
					if apierrors.IsNotFound(err) {
						return false
					}
					return false
				}
				for _, subset := range ep.Subsets {
					if len(subset.Addresses) > 0 {
						return true
					}
				}
				return false
			}, eventuallyTimeout, eventuallyInterval).Should(gomega.BeTrue(),
				"Service should have at least one ready endpoint")
		})
	})
})

// insecureHTTPClient returns an http.Client that skips TLS verification,
// suitable for testing the proxy's self-signed certificate directly.
func insecureHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
		Timeout: 10 * time.Second,
	}
}
