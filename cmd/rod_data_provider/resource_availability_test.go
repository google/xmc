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
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	rodpb "xmanager_cloud/api/protos/rod"
	"xmanager_cloud/cmd/rod_data_provider/cache"
)

func makeGPUNode(name, accel, zone, reservation, gpus string, unschedulable, ready bool) *corev1.Node {
	labels := map[string]string{}
	if zone != "" {
		labels["topology.kubernetes.io/zone"] = zone
	}
	if accel != "" {
		labels["cloud.google.com/gke-accelerator"] = accel
	}
	if reservation != "" {
		labels["cloud.google.com/reservation-name"] = reservation
	}
	condStatus := corev1.ConditionFalse
	if ready {
		condStatus = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec:       corev1.NodeSpec{Unschedulable: unschedulable},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				gpuResourceName: resource.MustParse(gpus),
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: condStatus},
			},
		},
	}
}

func makeGPUPod(name, nodeName, gpus string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							gpuResourceName: resource.MustParse(gpus),
						},
					},
				},
			},
		},
	}
}

func makeClusterQueue(name string, coveredResource corev1.ResourceName, flavorNames ...string) *kueuev1beta1.ClusterQueue {
	flavors := make([]kueuev1beta1.FlavorQuotas, 0, len(flavorNames))
	for _, fn := range flavorNames {
		flavors = append(flavors, kueuev1beta1.FlavorQuotas{Name: kueuev1beta1.ResourceFlavorReference(fn)})
	}
	return &kueuev1beta1.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: kueuev1beta1.ClusterQueueSpec{
			ResourceGroups: []kueuev1beta1.ResourceGroup{
				{
					CoveredResources: []corev1.ResourceName{coveredResource},
					Flavors:          flavors,
				},
			},
		},
	}
}

func makeResourceFlavor(name string, nodeLabels map[string]string) *kueuev1beta1.ResourceFlavor {
	return &kueuev1beta1.ResourceFlavor{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       kueuev1beta1.ResourceFlavorSpec{NodeLabels: nodeLabels},
	}
}

func TestGetResourceAvailabilityErrors(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name     string
		ctx      context.Context
		server   *server
		req      *rodpb.GetResourceAvailabilityRequest
		wantCode codes.Code
	}{
		{
			name:     "canceled context",
			ctx:      canceledCtx,
			server:   &server{physCache: cache.NewPhysicalInventoryCache(), kueueCache: cache.NewKueueCache()},
			req:      &rodpb.GetResourceAvailabilityRequest{},
			wantCode: codes.Canceled,
		},
		{
			name:     "nil request",
			ctx:      context.Background(),
			server:   &server{physCache: cache.NewPhysicalInventoryCache(), kueueCache: cache.NewKueueCache()},
			req:      nil,
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "nil physCache",
			ctx:      context.Background(),
			server:   &server{kueueCache: cache.NewKueueCache()},
			req:      &rodpb.GetResourceAvailabilityRequest{},
			wantCode: codes.Unavailable,
		},
		{
			name:     "nil physCache.Nodes",
			ctx:      context.Background(),
			server:   &server{physCache: &cache.PhysicalInventoryCache{}, kueueCache: cache.NewKueueCache()},
			req:      &rodpb.GetResourceAvailabilityRequest{},
			wantCode: codes.Unavailable,
		},
		{
			name:     "nil kueueCache",
			ctx:      context.Background(),
			server:   &server{physCache: cache.NewPhysicalInventoryCache()},
			req:      &rodpb.GetResourceAvailabilityRequest{},
			wantCode: codes.Unavailable,
		},
		{
			name:     "nil kueueCache.ClusterQueues",
			ctx:      context.Background(),
			server:   &server{physCache: cache.NewPhysicalInventoryCache(), kueueCache: &cache.KueueCache{}},
			req:      &rodpb.GetResourceAvailabilityRequest{},
			wantCode: codes.Unavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.server.GetResourceAvailability(tt.ctx, tt.req)
			if status.Code(err) != tt.wantCode {
				t.Fatalf("got code %v (err=%v), want %v", status.Code(err), err, tt.wantCode)
			}
		})
	}
}

func TestGetResourceAvailability(t *testing.T) {
	tests := []struct {
		name    string
		nodes   []*corev1.Node
		pods    []*corev1.Pod
		queues  []*kueuev1beta1.ClusterQueue
		flavors []*kueuev1beta1.ResourceFlavor
		want    *rodpb.GetResourceAvailabilityResponse
	}{
		{
			name: "basic gpu aggregation and non-gpu queue exclusion",
			nodes: []*corev1.Node{
				makeGPUNode("node-1", "nvidia-tesla-h100", "us-central1-a", "", "8", false, true),
			},
			pods: []*corev1.Pod{
				makeGPUPod("pod-1", "node-1", "2"),
			},
			queues: []*kueuev1beta1.ClusterQueue{
				makeClusterQueue("h100-queue", gpuResourceName, "h100-flavor"),
				makeClusterQueue("cpu-cluster-queue", corev1.ResourceCPU, "empty-flavor"),
				makeClusterQueue("tpu-cluster-queue", corev1.ResourceName("google.com/tpu"), "empty-flavor"),
			},
			flavors: []*kueuev1beta1.ResourceFlavor{
				makeResourceFlavor("h100-flavor", map[string]string{"cloud.google.com/gke-accelerator": "nvidia-tesla-h100"}),
				makeResourceFlavor("empty-flavor", map[string]string{}),
			},
			want: &rodpb.GetResourceAvailabilityResponse{
				Summaries: []*rodpb.AcceleratorSummary{
					{
						AcceleratorType:       "nvidia-tesla-h100",
						Zone:                  "us-central1-a",
						TotalNodes:            1,
						ActiveNodes:           1,
						TotalAccelerators:     8,
						ActiveAccelerators:    8,
						AllocatedAccelerators: 2,
						ClusterQueues:         []string{"h100-queue"},
					},
				},
			},
		},
		{
			name: "node liveness cordoned and not-ready nodes",
			nodes: []*corev1.Node{
				makeGPUNode("node-active", "nvidia-tesla-h100", "us-central1-a", "", "8", false, true),
				makeGPUNode("node-cordoned", "nvidia-tesla-h100", "us-central1-a", "", "8", true, true),
				makeGPUNode("node-not-ready", "nvidia-tesla-h100", "us-central1-a", "", "8", false, false),
			},
			want: &rodpb.GetResourceAvailabilityResponse{
				Summaries: []*rodpb.AcceleratorSummary{
					{
						AcceleratorType:    "nvidia-tesla-h100",
						Zone:               "us-central1-a",
						TotalNodes:         3,
						ActiveNodes:        1,
						TotalAccelerators:  24,
						ActiveAccelerators: 8,
					},
				},
			},
		},
		{
			name: "zone and reservation scoping with deterministic sorting",
			nodes: []*corev1.Node{
				makeGPUNode("node-b", "nvidia-tesla-h100", "us-central1-b", "reservation-b", "8", false, true),
				makeGPUNode("node-a-res", "nvidia-tesla-h100", "us-central1-a", "reservation-a", "8", false, true),
				makeGPUNode("node-a-unres", "nvidia-tesla-h100", "us-central1-a", "", "4", false, true),
				makeGPUNode("node-unknown", "", "us-central1-a", "", "2", false, true),
			},
			queues: []*kueuev1beta1.ClusterQueue{
				makeClusterQueue("h100-queue-res-a", gpuResourceName, "flavor-res-a"),
				makeClusterQueue("h100-queue-zone-b", gpuResourceName, "flavor-zone-b"),
			},
			flavors: []*kueuev1beta1.ResourceFlavor{
				makeResourceFlavor("flavor-res-a", map[string]string{
					"cloud.google.com/gke-accelerator":  "nvidia-tesla-h100",
					"topology.kubernetes.io/zone":       "us-central1-a",
					"cloud.google.com/reservation-name": "reservation-a",
				}),
				makeResourceFlavor("flavor-zone-b", map[string]string{
					"cloud.google.com/gke-accelerator": "nvidia-tesla-h100",
					"topology.kubernetes.io/zone":      "us-central1-b",
				}),
			},
			want: &rodpb.GetResourceAvailabilityResponse{
				Summaries: []*rodpb.AcceleratorSummary{
					{
						AcceleratorType:    "nvidia-tesla-h100",
						Zone:               "us-central1-a",
						Reservation:        "",
						TotalNodes:         1,
						ActiveNodes:        1,
						TotalAccelerators:  4,
						ActiveAccelerators: 4,
					},
					{
						AcceleratorType:    "nvidia-tesla-h100",
						Zone:               "us-central1-a",
						Reservation:        "reservation-a",
						TotalNodes:         1,
						ActiveNodes:        1,
						TotalAccelerators:  8,
						ActiveAccelerators: 8,
						ClusterQueues:      []string{"h100-queue-res-a"},
					},
					{
						AcceleratorType:    "nvidia-tesla-h100",
						Zone:               "us-central1-b",
						Reservation:        "reservation-b",
						TotalNodes:         1,
						ActiveNodes:        1,
						TotalAccelerators:  8,
						ActiveAccelerators: 8,
						ClusterQueues:      []string{"h100-queue-zone-b"},
					},
					{
						AcceleratorType:    "unknown",
						Zone:               "us-central1-a",
						TotalNodes:         1,
						ActiveNodes:        1,
						TotalAccelerators:  2,
						ActiveAccelerators: 2,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &server{
				physCache:  cache.NewPhysicalInventoryCache(),
				kueueCache: cache.NewKueueCache(),
			}
			for _, n := range tt.nodes {
				s.physCache.Nodes.Set(n.Name, n)
			}
			for _, p := range tt.pods {
				s.physCache.Pods.Set(p.Name, p)
			}
			for _, q := range tt.queues {
				s.kueueCache.ClusterQueues.Set(q.Name, q)
			}
			for _, f := range tt.flavors {
				s.kueueCache.ResourceFlavors.Set(f.Name, f)
			}

			got, err := s.GetResourceAvailability(context.Background(), &rodpb.GetResourceAvailabilityRequest{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("GetResourceAvailability() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGetResourceAvailabilityHandlesNilEntries(t *testing.T) {
	agg := make(map[availabilityKey]*rodpb.AcceleratorSummary)
	nodeMap := make(map[string]*corev1.Node)
	processNodes([]*corev1.Node{nil}, agg, nodeMap)
	processPods([]*corev1.Pod{nil}, nodeMap, agg)
	got := getCorrelatedQueues(
		availabilityKey{acceleratorType: "nvidia-tesla-h100", zone: "us-central1-a"},
		[]*kueuev1beta1.ClusterQueue{
			nil,
			makeClusterQueue("h100-queue", gpuResourceName, "nil-flavor"),
		},
		map[string]*kueuev1beta1.ResourceFlavor{"nil-flavor": nil},
	)
	if len(got) != 0 {
		t.Errorf("Expected no correlated queues for nil flavor, got %v", got)
	}
}
