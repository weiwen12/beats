// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package pod

import (
	"fmt"
	"github.com/goccy/go-json"

	"github.com/elastic/beats/v7/libbeat/common"
	"github.com/elastic/beats/v7/metricbeat/mb"
	"github.com/elastic/beats/v7/metricbeat/module/kubernetes"
	"github.com/elastic/beats/v7/metricbeat/module/kubernetes/util"
)

func eventMapping(content []byte, metricsRepo *util.MetricsRepo) ([]common.MapStr, error) {
	events := []common.MapStr{}

	var summary kubernetes.Summary
	err := json.Unmarshal(content, &summary)
	if err != nil {
		return nil, fmt.Errorf("cannot unmarshal json response: %s", err)
	}

	node := summary.Node

	nodeCores := 0.0
	nodeMem := 0.0

	nodeStore := metricsRepo.GetNodeStore(node.NodeName)
	nodeMetrics := nodeStore.GetNodeMetrics()
	if nodeMetrics.CoresAllocatable != nil {
		nodeCores = nodeMetrics.CoresAllocatable.Value
	}
	if nodeMetrics.MemoryAllocatable != nil {
		nodeMem = nodeMetrics.MemoryAllocatable.Value
	}
	for _, pod := range summary.Pods {
		var usageNanoCores, usageMem, availMem, rss, workingSet, pageFaults, majorPageFaults uint64
		var podCoreLimit, podMemLimit float64

		podId := util.NewPodId(pod.PodRef.Namespace, pod.PodRef.Name)
		podStore := nodeStore.GetPodStore(podId)

		for _, container := range pod.Containers {
			usageNanoCores += container.CPU.UsageNanoCores
			usageMem += container.Memory.UsageBytes
			availMem += container.Memory.AvailableBytes
			rss += container.Memory.RssBytes
			workingSet += container.Memory.WorkingSetBytes
			pageFaults += container.Memory.PageFaults
			majorPageFaults += container.Memory.MajorPageFaults

			containerStore := podStore.GetContainerStore(container.Name)
			containerMetrics := containerStore.GetContainerMetrics()

			containerCoresLimit := nodeCores
			if containerMetrics.CoresLimit != nil {
				containerCoresLimit = containerMetrics.CoresLimit.Value
			}

			containerMemLimit := nodeMem
			if containerMetrics.MemoryLimit != nil {
				containerMemLimit = containerMetrics.MemoryLimit.Value
			}
			podCoreLimit += containerCoresLimit
			podMemLimit += containerMemLimit
		}

		podEvent := common.MapStr{
			mb.ModuleDataKey: common.MapStr{
				"namespace": pod.PodRef.Namespace,
				"node": common.MapStr{
					"name": node.NodeName,
				},
			},
			"name": pod.PodRef.Name,
			"uid":  pod.PodRef.UID,

			"cpu": common.MapStr{
				"usage": common.MapStr{
					"nanocores": usageNanoCores,
				},
			},

			"memory": common.MapStr{
				"usage": common.MapStr{
					"bytes": usageMem,
				},
				"available": common.MapStr{
					"bytes": availMem,
				},
				"working_set": common.MapStr{
					"bytes": workingSet,
				},
				"rss": common.MapStr{
					"bytes": rss,
				},
				"page_faults":       pageFaults,
				"major_page_faults": majorPageFaults,
			},

			"network": common.MapStr{
				"rx": common.MapStr{
					"bytes":  pod.Network.RxBytes,
					"errors": pod.Network.RxErrors,
				},
				"tx": common.MapStr{
					"bytes":  pod.Network.TxBytes,
					"errors": pod.Network.TxErrors,
				},
			},
		}

		if pod.StartTime != "" {
			podEvent.Put("start_time", pod.StartTime)
		}

		// NOTE:
		// - `podCoreLimit > `nodeCores` is possible if a pod has more than one container
		// and at least one of them doesn't have a limit set. The container without limits
		// inherit a limit = `nodeCores` and the sum of all limits for all the
		// containers will be > `nodeCores`. In this case we want to cap the
		// value of `podCoreLimit` to `nodeCores`.
		// - `nodeCores` can be 0 if `state_node` and/or `node` metricsets are disabled.
		// - if `nodeCores` == 0 and podCoreLimit > 0` we need to avoid that `podCoreLimit` is
		// incorrectly overridden to 0. That's why we check for `nodeCores > 0`.
		if nodeCores > 0 && podCoreLimit > nodeCores {
			podCoreLimit = nodeCores
		}

		// NOTE:
		// - `podMemLimit > `nodeMem` is possible if a pod has more than one container
		// and at least one of them doesn't have a limit set. The container without limits
		// inherit a limit = `nodeMem` and the sum of all limits for all the
		// containers will be > `nodeMem`. In this case we want to cap the
		// value of `podMemLimit` to `nodeMem`.
		// - `nodeMem` can be 0 if `state_node` and/or `node` metricsets are disabled.
		// - if `nodeMem` == 0 and podMemLimit > 0` we need to avoid that `podMemLimit` is
		// incorrectly overridden to 0. That's why we check for `nodeMem > 0`.
		if nodeMem > 0 && podMemLimit > nodeMem {
			podMemLimit = nodeMem
		}

		if nodeCores > 0 {
			podEvent.Put("cpu.usage.node.pct", float64(usageNanoCores)/1e9/nodeCores)
		}

		if podCoreLimit > 0 {
			podEvent.Put("cpu.usage.limit.pct", float64(usageNanoCores)/1e9/podCoreLimit)
		}

		if usageMem > 0 {
			if nodeMem > 0 {
				podEvent.Put("memory.usage.node.pct", float64(usageMem)/nodeMem)
			}
			if podMemLimit > 0 {
				podEvent.Put("memory.usage.limit.pct", float64(usageMem)/podMemLimit)
			}
		}

		if workingSet > 0 && usageMem == 0 {
			if nodeMem > 0 {
				podEvent.Put("memory.usage.node.pct", float64(workingSet)/nodeMem)
			}
			if podMemLimit > 0 {
				podEvent.Put("memory.usage.limit.pct", float64(workingSet)/podMemLimit)
			}
		}

		events = append(events, podEvent)
	}
	return events, nil
}
