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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	rodpb "xmanager_cloud/api/protos/rod"
)

// GetResourceAvailability aggregates physical GPU availability from the cache.
func (s *server) GetResourceAvailability(ctx context.Context, req *rodpb.GetResourceAvailabilityRequest) (*rodpb.GetResourceAvailabilityResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	if s.physCache == nil || s.physCache.Nodes == nil || s.physCache.Pods == nil {
		return nil, status.Error(codes.Unavailable, "physical inventory cache is uninitialized")
	}
	if s.kueueCache == nil || s.kueueCache.ClusterQueues == nil || s.kueueCache.ResourceFlavors == nil {
		return nil, status.Error(codes.Unavailable, "kueue cache is uninitialized")
	}

	resp := &rodpb.GetResourceAvailabilityResponse{}

	nodes := s.physCache.Nodes.List()
	pods := s.physCache.Pods.List()
	queues := s.kueueCache.ClusterQueues.List()
	flavors := s.kueueCache.ResourceFlavors.List()

	flavorMap := make(map[string]*kueuev1beta1.ResourceFlavor, len(flavors))
	for _, f := range flavors {
		flavorMap[f.Name] = f
	}

	agg := make(map[string]map[string]map[string]*rodpb.AcceleratorSummary)
	nodeMap := make(map[string]*corev1.Node, len(nodes))
	processNodes(nodes, agg, nodeMap)
	processPods(pods, nodeMap, agg)
	populateCorrelatedQueues(queues, flavorMap, agg, resp)

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

func processNodes(nodes [](*corev1.Node), agg map[string]map[string]map[string]*rodpb.AcceleratorSummary, nodeMap map[string]*corev1.Node) {
	for _, node := range nodes {
		qty, ok := node.Status.Capacity["nvidia.com/gpu"]
		if !ok || qty.Sign() <= 0 {
			continue
		}
		nodeMap[node.Name] = node
		gpuCount := qty.Value()

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
}

func processPods(pods [](*corev1.Pod), nodeMap map[string]*corev1.Node, agg map[string]map[string]map[string]*rodpb.AcceleratorSummary) {
	for _, pod := range pods {
		nodeName := pod.Spec.NodeName
		if nodeName == "" {
			continue
		}

		node, ok := nodeMap[nodeName]
		if !ok {
			continue
		}

		var gpuCount int64
		for _, container := range pod.Spec.Containers {
			if qty, ok := container.Resources.Requests["nvidia.com/gpu"]; ok {
				gpuCount += qty.Value()
			}
		}
		if gpuCount <= 0 {
			continue
		}

		zone := node.Labels["topology.kubernetes.io/zone"]
		accelType := node.Labels["cloud.google.com/gke-accelerator"]
		if accelType == "" {
			accelType = "unknown"
		}
		reservation := node.Labels["cloud.google.com/reservation-name"]

		if agg[accelType] != nil && agg[accelType][zone] != nil && agg[accelType][zone][reservation] != nil {
			agg[accelType][zone][reservation].AllocatedAccelerators += int32(gpuCount)
		}
	}
}

func getCorrelatedQueues(accelType string, reservation string, queues [](*kueuev1beta1.ClusterQueue), flavorMap map[string]*kueuev1beta1.ResourceFlavor) []string {
	var correlated []string
	for _, cq := range queues {
		for _, rf := range cq.Spec.ResourceGroups {
			for _, flavor := range rf.Flavors {
				f, ok := flavorMap[string(flavor.Name)]
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
	return unique
}

func populateCorrelatedQueues(queues [](*kueuev1beta1.ClusterQueue), flavorMap map[string]*kueuev1beta1.ResourceFlavor, agg map[string]map[string]map[string]*rodpb.AcceleratorSummary, resp *rodpb.GetResourceAvailabilityResponse) {
	for accelType, zones := range agg {
		for _, reservations := range zones {
			for reservation, summary := range reservations {
				summary.ClusterQueues = getCorrelatedQueues(accelType, reservation, queues, flavorMap)
				resp.Summaries = append(resp.Summaries, summary)
			}
		}
	}
}
