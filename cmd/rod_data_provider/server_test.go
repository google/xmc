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

	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/api/resource/resource"
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	spb "xmanager_cloud/api/protos/rod"
	"xmanager_cloud/cmd/rod_data_provider/cache"
)

func TestGetFittingQueues(t *testing.T) {
	ctx := context.Background()
	c := cache.NewKueueCache()

	rfH100 := &kueuev1beta1.ResourceFlavor{
		ObjectMeta: metav1.ObjectMeta{Name: "flavor-h100"},
		Spec: kueuev1beta1.ResourceFlavorSpec{
			NodeLabels: map[string]string{
				"cloud.google.com/gke-accelerator": "nvidia-tesla-h100",
			},
		},
	}
	c.ResourceFlavors.Set("flavor-h100", rfH100)

	cq := &kueuev1beta1.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{Name: "cq-gpu"},
		Spec: kueuev1beta1.ClusterQueueSpec{
			ResourceGroups: []kueuev1beta1.ResourceGroup{
				{
					Flavors: []kueuev1beta1.FlavorQuotas{
						{
							Name: "flavor-h100",
							Resources: []kueuev1beta1.ResourceQuota{
								{
									Name:         corev1.ResourceName("nvidia.com/gpu"),
									NominalQuota: resource.MustParse("8"),
								},
							},
						},
					},
				},
			},
		},
	}
	c.ClusterQueues.Set("cq-gpu", cq)

	cfg := &Config{
		Port:     "8080",
		GRPCPort: "50051",
	}
	srv := newServer(cfg, c)

	req := &spb.GetFittingQueuesRequest{
		Requirements: []*spb.Resource{
			{ResourceType: "nvidia-tesla-h100", Count: 4},
		},
	}
	resp, err := srv.GetFittingQueues(ctx, req)
	if err != nil {
		t.Fatalf("GetFittingQueues failed: %v", err)
	}

	if len(resp.Queues) != 1 {
		t.Fatalf("expected 1 fitting queue in response, got %d", len(resp.Queues))
	}
	if resp.Queues[0].QueueName != "cq-gpu" {
		t.Errorf("expected queue name 'cq-gpu', got %q", resp.Queues[0].QueueName)
	}
}
