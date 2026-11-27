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
	"cmp"
	"context"
	"slices"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	rodpb "xmanager_cloud/api/protos/rod"
)

const gpuResourceName = corev1.ResourceName("nvidia.com/gpu")

type availabilityKey struct {
	acceleratorType string
	zone            string
	reservation     string
}

// GetResourceAvailability aggregates physical GPU availability from the cache.
func (s *server) GetResourceAvailability(ctx context.Context, req *rodpb.GetResourceAvailabilityRequest) (*rodpb.GetResourceAvailabilityResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	if s.physCache == nil || s.physCache.Nodes == nil || s.physCache.Pods == nil {
		return nil, status.Error(codes.Unavailable, "physical inventory cache is uninitialized")
	}
	if s.kueueCache == nil || s.kueueCache.ClusterQueues == nil || s.kueueCache.ResourceFlavors == nil {
		return nil, status.Error(codes.Unavailable, "kueue cache is uninitialized")
	}

	nodes := s.physCache.Nodes.List()
	pods := s.physCache.Pods.List()
	queues := s.kueueCache.ClusterQueues.List()
	flavorMap := buildResourceFlavorMap(s.kueueCache)

	agg := make(map[availabilityKey]*rodpb.AcceleratorSummary)
	nodeMap := make(map[string]*corev1.Node, len(nodes))
	processNodes(nodes, agg, nodeMap)
	processPods(pods, nodeMap, agg)

	resp := &rodpb.GetResourceAvailabilityResponse{}
	populateCorrelatedQueues(queues, flavorMap, agg, resp)

	slices.SortFunc(resp.Summaries, func(a, b *rodpb.AcceleratorSummary) int {
		if c := cmp.Compare(a.AcceleratorType, b.AcceleratorType); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Zone, b.Zone); c != 0 {
			return c
		}
		return cmp.Compare(a.Reservation, b.Reservation)
	})

	return resp, nil
}

func nodeAvailabilityKey(node *corev1.Node) availabilityKey {
	accelType := node.Labels["cloud.google.com/gke-accelerator"]
	if accelType == "" {
		accelType = "unknown"
	}
	return availabilityKey{
		acceleratorType: accelType,
		zone:            node.Labels["topology.kubernetes.io/zone"],
		reservation:     node.Labels["cloud.google.com/reservation-name"],
	}
}

func isNodeActive(node *corev1.Node) bool {
	if node.Spec.Unschedulable {
		return false
	}
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func processNodes(nodes []*corev1.Node, agg map[availabilityKey]*rodpb.AcceleratorSummary, nodeMap map[string]*corev1.Node) {
	for _, node := range nodes {
		if node == nil {
			continue
		}
		qty, ok := node.Status.Capacity[gpuResourceName]
		if !ok || qty.Sign() <= 0 {
			continue
		}
		nodeMap[node.Name] = node
		gpuCount := int32(qty.Value())

		key := nodeAvailabilityKey(node)
		summary, exists := agg[key]
		if !exists {
			summary = &rodpb.AcceleratorSummary{
				AcceleratorType: key.acceleratorType,
				Zone:            key.zone,
				Reservation:     key.reservation,
			}
			agg[key] = summary
		}

		summary.TotalNodes++
		summary.TotalAccelerators += gpuCount
		if isNodeActive(node) {
			summary.ActiveNodes++
			summary.ActiveAccelerators += gpuCount
		}
	}
}

func processPods(pods []*corev1.Pod, nodeMap map[string]*corev1.Node, agg map[availabilityKey]*rodpb.AcceleratorSummary) {
	for _, pod := range pods {
		if pod == nil || pod.Spec.NodeName == "" {
			continue
		}
		node, ok := nodeMap[pod.Spec.NodeName]
		if !ok {
			continue
		}

		var gpuCount int64
		for _, container := range pod.Spec.Containers {
			if qty, ok := container.Resources.Requests[gpuResourceName]; ok {
				gpuCount += qty.Value()
			}
		}
		if gpuCount <= 0 {
			continue
		}

		if summary, ok := agg[nodeAvailabilityKey(node)]; ok {
			summary.AllocatedAccelerators += int32(gpuCount)
		}
	}
}

func resourceGroupCoversGPU(rg kueuev1beta1.ResourceGroup) bool {
	if slices.Contains(rg.CoveredResources, gpuResourceName) {
		return true
	}
	for _, flavor := range rg.Flavors {
		for _, r := range flavor.Resources {
			if r.Name == gpuResourceName {
				return true
			}
		}
	}
	return false
}

func flavorMatches(labels map[string]string, key availabilityKey) bool {
	if expectedAccel, ok := labels["cloud.google.com/gke-accelerator"]; ok && expectedAccel != key.acceleratorType {
		return false
	}
	if expectedZone, ok := labels["topology.kubernetes.io/zone"]; ok && expectedZone != key.zone {
		return false
	}
	if resLabel, ok := labels["cloud.google.com/reservation-name"]; ok && resLabel != key.reservation {
		return false
	}
	return true
}

func clusterQueueMatches(cq *kueuev1beta1.ClusterQueue, key availabilityKey, flavorMap map[string]*kueuev1beta1.ResourceFlavor) bool {
	if cq == nil {
		return false
	}
	for _, rf := range cq.Spec.ResourceGroups {
		if !resourceGroupCoversGPU(rf) {
			continue
		}
		for _, flavor := range rf.Flavors {
			f, ok := flavorMap[string(flavor.Name)]
			if !ok || f == nil {
				continue
			}
			if flavorMatches(f.Spec.NodeLabels, key) {
				return true
			}
		}
	}
	return false
}

func getCorrelatedQueues(key availabilityKey, queues []*kueuev1beta1.ClusterQueue, flavorMap map[string]*kueuev1beta1.ResourceFlavor) []string {
	var correlated []string
	for _, cq := range queues {
		if clusterQueueMatches(cq, key, flavorMap) {
			correlated = append(correlated, cq.Name)
		}
	}
	slices.Sort(correlated)
	return slices.Compact(correlated)
}

func populateCorrelatedQueues(queues []*kueuev1beta1.ClusterQueue, flavorMap map[string]*kueuev1beta1.ResourceFlavor, agg map[availabilityKey]*rodpb.AcceleratorSummary, resp *rodpb.GetResourceAvailabilityResponse) {
	for key, summary := range agg {
		summary.ClusterQueues = getCorrelatedQueues(key, queues, flavorMap)
		resp.Summaries = append(resp.Summaries, summary)
	}
}
