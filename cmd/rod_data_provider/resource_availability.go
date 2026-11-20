// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"sort"
	"strconv"

	rodpb "xmanager_cloud/api/protos/rod"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// GetResourceAvailability aggregates physical GPU availability from the cache.
func (s *server) GetResourceAvailability(ctx context.Context, req *rodpb.GetResourceAvailabilityRequest) (*rodpb.GetResourceAvailabilityResponse, error) {
	resp := &rodpb.GetResourceAvailabilityResponse{}

	nodes := s.physCache.Nodes.List()
	pods := s.physCache.Pods.List()
	queues := s.kueueCache.ClusterQueues.List()

	// Map of accelType -> zone -> reservation -> AcceleratorSummary
	agg := make(map[string]map[string]map[string]*rodpb.AcceleratorSummary)

	// Step 1: Process Nodes
	for _, node := range nodes {
		qty, ok := node.Status.Capacity["nvidia.com/gpu"]
		if !ok || qty.Cmp(resource.MustParse("0")) <= 0 {
			continue
		}
		gpuCount, _ := qty.AsInt64()

		zone := node.Labels["topology.kubernetes.io/zone"]
		accelType := node.Labels["cloud.google.com/gke-accelerator"]
		if accelType == "" {
			accelType = "unknown"
		}
		reservation := node.Labels["cloud.google.com/reservation-name"]

		if agg[accelType] == nil {
			agg[accelType] = make(map[string]map[string]*rodpb.AcceleratorSummary)
		}
		if agg[accelType][zone] == nil {
			agg[accelType][zone] = make(map[string]*rodpb.AcceleratorSummary)
		}
		if agg[accelType][zone][reservation] == nil {
			agg[accelType][zone][reservation] = &rodpb.AcceleratorSummary{
				AcceleratorType: accelType,
				Zone:            zone,
				Reservation:     reservation,
			}
		}

		summary := agg[accelType][zone][reservation]
		summary.TotalNodes++
		summary.TotalAccelerators += int32(gpuCount)

		// Check if node is allocatable (not cordoned, Ready)
		active := true
		if node.Spec.Unschedulable {
			active = false
		}
		ready := false
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				ready = true
				break
			}
		}
		if !ready {
			active = false
		}

		if active {
			summary.ActiveNodes++
			summary.ActiveAccelerators += int32(gpuCount)
		}
	}

	// Step 2: Process Pods and attribute allocated GPUs
	for _, pod := range pods {
		nodeName := pod.Spec.NodeName
		if nodeName == "" {
			continue
		}
		val, ok := pod.Annotations["xmc-effective-gpus-requested"]
		if !ok {
			continue
		}
		requestedGpus, err := strconv.ParseInt(val, 10, 32)
		if err != nil || requestedGpus <= 0 {
			continue
		}

		node, ok := s.physCache.Nodes.Get(nodeName)
		if !ok {
			continue
		}

		zone := node.Labels["topology.kubernetes.io/zone"]
		accelType := node.Labels["cloud.google.com/gke-accelerator"]
		if accelType == "" {
			accelType = "unknown"
		}
		reservation := node.Labels["cloud.google.com/reservation-name"]

		if agg[accelType] != nil && agg[accelType][zone] != nil && agg[accelType][zone][reservation] != nil {
			agg[accelType][zone][reservation].AllocatedAccelerators += int32(requestedGpus)
		}
	}

	// Step 3: Correlate with Kueue ClusterQueues and populate Response
	for accelType, zones := range agg {
		for _, reservations := range zones {
			for reservation, summary := range reservations {
				var correlated []string
				for _, cq := range queues {
					for _, rf := range cq.Spec.ResourceGroups {
						for _, flavor := range rf.Flavors {
							f, ok := s.kueueCache.ResourceFlavors.Get(string(flavor.Name))
							if !ok {
								continue
							}
							labels := f.Spec.NodeLabels
							match := true
							if expectedAccel, ok := labels["cloud.google.com/gke-accelerator"]; ok && expectedAccel != accelType {
								match = false
							}
							if resLabel, ok := labels["cloud.google.com/reservation-name"]; ok && resLabel != reservation {
								match = false
							}
							if match {
								correlated = append(correlated, cq.Name)
								break
							}
						}
					}
				}
				sort.Strings(correlated)
				var unique []string
				for i, name := range correlated {
					if i == 0 || name != correlated[i-1] {
						unique = append(unique, name)
					}
				}
				summary.ClusterQueues = unique
				resp.Summaries = append(resp.Summaries, summary)
			}
		}
	}

	// Sort summaries to ensure deterministic output
	sort.Slice(resp.Summaries, func(i, j int) bool {
		if resp.Summaries[i].AcceleratorType != resp.Summaries[j].AcceleratorType {
			return resp.Summaries[i].AcceleratorType < resp.Summaries[j].AcceleratorType
		}
		if resp.Summaries[i].Zone != resp.Summaries[j].Zone {
			return resp.Summaries[i].Zone < resp.Summaries[j].Zone
		}
		return resp.Summaries[i].Reservation < resp.Summaries[j].Reservation
	})

	return resp, nil
}
