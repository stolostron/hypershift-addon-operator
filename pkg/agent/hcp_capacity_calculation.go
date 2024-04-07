package agent

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/common/model"

	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/stolostron/hypershift-addon-operator/pkg/metrics"
	"go.withmatt.com/size"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeClient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaulCpuRequestPerHCP     float64 = 5  // vCPUs required per HCP
	defaultMemoryRequestPerHCP float64 = 18 // Memory required per HCP in GB
	defaultPodsPerHCP          float64 = 75 // Pods per HighlyAvailable HCP

	defaultIncrementalCPUUsagePer1KQPS float64 = 9.0 // Incremental CPU usage per 1K QPS
	defaultIncrementalMemUsagePer1KQPS float64 = 2.5 // Incremental Memory usage per 1K QPS

	defaultIdleCPUUsage    float64 = 2.9  // Idle CPU usage (unit vCPU)
	defaultIdleMemoryUsage float64 = 11.1 // Idle memory usage (unit GiB)

	defaultMinimumQPSPerHCP float64 = 50.0   // Default low Kube API QPS per HCP
	defaultMediumQPSPerHCP  float64 = 1000.0 // Default medium Kube API QPS per HCP
	defaultHighQPSPerHCP    float64 = 2000.0 // Default high Kube API QPS per HCP
)

type HCPSizingBaseline struct {
	cpuRequestPerHCP            float64 // vCPUs required per HCP
	memoryRequestPerHCP         float64 // Memory required per HCP in BG
	podsPerHCP                  float64 // Pods per HighlyAvailable HCP
	incrementalCPUUsagePer1KQPS float64 // Incremental CPU usage per 1K QPS
	incrementalMemUsagePer1KQPS float64 // Incremental Memory usage per 1K QPS
	idleCPUUsage                float64 // Idle CPU usage (unit vCPU)
	idleMemoryUsage             float64 // Idle memory usage (unit GiB)
	minimumQPSPerHCP            float64 // Default low Kube API QPS per HCP
	mediumQPSPerHCP             float64 // Default medium Kube API QPS per HCP
	highQPSPerHCP               float64 // Default high Kube API QPS per HCP
}

func (c *agentController) calculateMaxHCPs(workerCPUs, workerMemory, maxPods, apiRate float64, useLoadBased bool) float64 {
	maxHCPsByCPU := workerCPUs / c.hcpSizingBaseline.cpuRequestPerHCP
	maxHCPsByMemory := workerMemory / c.hcpSizingBaseline.memoryRequestPerHCP
	maxHCPsByPods := maxPods / c.hcpSizingBaseline.podsPerHCP

	var maxHCPsByCPUUsage, maxHCPsByMemoryUsage float64
	if useLoadBased {
		maxHCPsByCPUUsage = workerCPUs / (c.hcpSizingBaseline.idleCPUUsage + (apiRate/1000)*c.hcpSizingBaseline.incrementalCPUUsagePer1KQPS)
		maxHCPsByMemoryUsage = workerMemory / (c.hcpSizingBaseline.idleMemoryUsage + (apiRate/1000)*c.hcpSizingBaseline.incrementalMemUsagePer1KQPS)
	} else {
		maxHCPsByCPUUsage = maxHCPsByCPU
		maxHCPsByMemoryUsage = maxHCPsByMemory
	}

	// Return the minimum of all the calculated values (this is the maximum number of HCPs that can be hosted)
	// This considers the most constrained resource as the limiting factor (e.g., CPU, Memory, Pods, etc.)
	minHCPs := math.Min(maxHCPsByCPU, math.Min(maxHCPsByCPUUsage, math.Min(maxHCPsByMemory, math.Min(maxHCPsByMemoryUsage, maxHCPsByPods))))

	return minHCPs
}

func (c *agentController) calculateCapacitiesToHostHCPs() error {
	listopts := &runtimeClient.ListOptions{}
	hcpList := &hyperv1beta1.HostedControlPlaneList{}
	err := c.spokeUncachedClient.List(context.TODO(), hcpList, listopts)
	if err != nil {
		if strings.HasPrefix(err.Error(), "no matches for kind") {
			c.log.Info("No HostedControlPlane kind exists yet.")
		} else {
			c.log.Error(err, "failed to list hosted control planes")
			return err
		}
	}

	metrics.QPSValues.WithLabelValues("average").Set(1)
	metrics.QPSValues.WithLabelValues("low").Set(c.hcpSizingBaseline.minimumQPSPerHCP)
	metrics.QPSValues.WithLabelValues("medium").Set(c.hcpSizingBaseline.mediumQPSPerHCP)
	metrics.QPSValues.WithLabelValues("high").Set(c.hcpSizingBaseline.highQPSPerHCP)

	totalHCPQPS := c.hcpSizingBaseline.minimumQPSPerHCP
	averageHCPQPS := c.hcpSizingBaseline.minimumQPSPerHCP
	numberOfHCPs := 0.0
	if c.prometheusClient == nil {
		c.log.Info("Prometheus client is not available. Defaulting the average QPS to the minimum QPS range which " + fmt.Sprintf("%f", c.hcpSizingBaseline.minimumQPSPerHCP))
	} else {
		for _, hcp := range hcpList.Items {
			if hcp.Status.Ready { // For calcucalting the average QPS, consider HCPs with ready state only
				queryStr := "sum(rate(apiserver_request_total{namespace=~\"" + hcp.Namespace + "\"}[2m])) by (namespace)"
				result, warnings, err := c.prometheusClient.Query(context.TODO(), queryStr, time.Now())
				if err != nil {
					c.log.Error(err, "failed to query Prometheus")
					continue
				}
				if len(warnings) > 0 {
					c.log.Info("Warnings in querying Prometheus: %v\n", warnings)
				}

				for _, sample := range result.(model.Vector) {
					hcpQPS, err := strconv.ParseFloat(sample.Value.String(), 64)
					if err == nil {
						totalHCPQPS += hcpQPS
					} else {
						totalHCPQPS += c.hcpSizingBaseline.minimumQPSPerHCP
					}
				}
				numberOfHCPs++
			}
		}

		if numberOfHCPs > 0 {
			averageHCPQPS = totalHCPQPS / numberOfHCPs
		}

		metrics.QPSValues.WithLabelValues("average").Set(averageHCPQPS)

		c.log.Info(fmt.Sprintf("There are currently %d hosted control planes with ready status", int(numberOfHCPs)))
		c.log.Info("The average QPS of all existing HCPs is " + fmt.Sprintf("%f", averageHCPQPS))
	}

	c.log.Info(fmt.Sprintf("There are currently %d hosted control planes", int(numberOfHCPs)))
	c.log.Info("The average QPS of all existing HCPs is " + fmt.Sprintf("%f", averageHCPQPS))

	nodes := &corev1.NodeList{}
	workerNodesSelector, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
		MatchLabels: map[string]string{
			"node-role.kubernetes.io/worker": "",
		},
	})
	if err != nil {
		c.log.Error(err, "failed to define worker nodes label selector")
		return err
	}

	listopts = &runtimeClient.ListOptions{LabelSelector: workerNodesSelector}
	err = c.spokeUncachedClient.List(context.TODO(), nodes, listopts)
	if err != nil {
		c.log.Error(err, "failed to list worker nodes")
		return err
	}

	totalWorkerCPU := 0.0
	totalWorkerMemory := 0.0
	totalWorkerPods := 0.0

	for _, node := range nodes.Items {
		totalWorkerCPU += node.Status.Capacity.Cpu().AsApproximateFloat64()
		totalWorkerMemory += node.Status.Capacity.Memory().AsApproximateFloat64()
		totalWorkerPods += node.Status.Capacity.Pods().AsApproximateFloat64()

		nodeMemInGB := node.Status.Capacity.Memory().AsApproximateFloat64() / float64(size.Gigabyte)

		metrics.WorkerNodeResourceCapacities.WithLabelValues(node.Name,
			fmt.Sprintf("%.2f", node.Status.Capacity.Cpu().AsApproximateFloat64()),
			fmt.Sprintf("%.2f", nodeMemInGB),
			node.Status.Capacity.Pods().String()).Set(1)
	}

	totalWorkerMemory = totalWorkerMemory / float64(size.Gigabyte)

	c.log.Info("The worker nodes have " + fmt.Sprintf("%f", totalWorkerCPU) + " vCPUs")
	c.log.Info("The worker nodes have " + fmt.Sprintf("%f", totalWorkerMemory) + " GB memory")
	c.log.Info("The maximum number of pods the worker nodes can have is " + fmt.Sprintf("%f", totalWorkerPods))

	// 1. Request based max num of HCPs
	maxHCPs := int(math.Floor(c.calculateMaxHCPs(totalWorkerCPU, totalWorkerMemory, totalWorkerPods, 0.0, false)))

	// 2. ~50 low QPS load based max num of HCPs
	maxLowQPSHCPs := int(math.Floor(c.calculateMaxHCPs(totalWorkerCPU, totalWorkerMemory, totalWorkerPods, c.hcpSizingBaseline.minimumQPSPerHCP, true)))

	// 3. ~1000 medium QPS load based max num of HCPs
	maxMediumQPSHCPs := int(math.Floor(c.calculateMaxHCPs(totalWorkerCPU, totalWorkerMemory, totalWorkerPods, c.hcpSizingBaseline.mediumQPSPerHCP, true)))

	// 4. ~2000 high QPS load based max num of HCPs
	maxHighQPSHCPs := int(math.Floor(c.calculateMaxHCPs(totalWorkerCPU, totalWorkerMemory, totalWorkerPods, c.hcpSizingBaseline.highQPSPerHCP, true)))

	// 5. Current everage QPS of all HCPs max num of HCPs
	maxAvgQPSHCPs := int(math.Floor(c.calculateMaxHCPs(totalWorkerCPU, totalWorkerMemory, totalWorkerPods, averageHCPQPS, true)))

	// 6. Current number of HCPs

	c.log.Info("The maximum number of HCPs based on resource requests per HCP is " + fmt.Sprintf("%d", maxHCPs))
	c.log.Info("The maximum number of HCPs based on low QPS load per HCP is " + fmt.Sprintf("%d", maxLowQPSHCPs))
	c.log.Info("The maximum number of HCPs based on medium QPS load per HCP is " + fmt.Sprintf("%d", maxMediumQPSHCPs))
	c.log.Info("The maximum number of HCPs based on high QPS load per HCP is " + fmt.Sprintf("%d", maxHighQPSHCPs))
	c.log.Info("The maximum number of HCPs based on average QPS of all existing HCPs is " + fmt.Sprintf("%d", maxAvgQPSHCPs))

	metrics.CapacityOfQPSBasedHCPs.Reset()

	metrics.CapacityOfRequestBasedHCPs.Set(float64(maxHCPs))
	metrics.CapacityOfLowQPSHCPs.Set(float64(maxLowQPSHCPs))
	metrics.CapacityOfMediumQPSHCPs.Set(float64(maxMediumQPSHCPs))
	metrics.CapacityOfHighQPSHCPs.Set(float64(maxHighQPSHCPs))
	metrics.CapacityOfAverageQPSHCPs.Set(float64(maxAvgQPSHCPs))
	metrics.CapacityOfQPSBasedHCPs.WithLabelValues("high").Set(float64(maxHighQPSHCPs))
	metrics.CapacityOfQPSBasedHCPs.WithLabelValues("medium").Set(float64(maxMediumQPSHCPs))
	metrics.CapacityOfQPSBasedHCPs.WithLabelValues("low").Set(float64(maxLowQPSHCPs))
	metrics.CapacityOfQPSBasedHCPs.WithLabelValues("average").Set(float64(maxAvgQPSHCPs))
	return nil
}
