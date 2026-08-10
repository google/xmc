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
	"time"

	"golang/protobuf/v2/proto/proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestMapWorkUnitToDashboardJob_RequestedResources(t *testing.T) {
	now := time.Now()
	wu := proto.Modify(baseWorkUnit, func(w *essmsgpb.WorkUnit) {
		w.Executable = &essmsgpb.Executable{
			Type: &essmsgpb.Executable_Jobset{
				Jobset: &essmsgpb.JobSetExecutable{
					Jobs: []*essmsgpb.KubernetesJob{
						{
							Replicas: 2,
							Spec: &essmsgpb.ExecutableSpec{
								Resources: &essmsgpb.ResourceSet{
									Resources: &essmsgpb.ResourceSet_GpuResources{
										GpuResources: &essmsgpb.GpuResources{
											Type:  "nvidia-tesla-h100",
											Count: 4,
										},
									},
								},
							},
						},
						{
							Replicas: 1,
							Spec: &essmsgpb.ExecutableSpec{
								Resources: &essmsgpb.ResourceSet{
									Resources: &essmsgpb.ResourceSet_GpuResources{
										GpuResources: &essmsgpb.GpuResources{
											Type:  "nvidia-tesla-h100",
											Count: 2,
										},
									},
								},
							},
						},
					},
				},
			},
		}
	})

	job := mapWorkUnitToDashboardJob(context.Background(), wu, now)
	if job == nil {
		t.Fatalf("mapWorkUnitToDashboardJob returned nil")
	}
	if len(job.GetRequestedResources()) != 1 {
		t.Fatalf("RequestedResources len = %d, want 1", len(job.GetRequestedResources()))
	}
	res := job.GetRequestedResources()[0]
	if res.GetResourceType() != "nvidia-tesla-h100" || res.GetCount() != 10.0 {
		t.Errorf("RequestedResource = %v %v, want nvidia-tesla-h100 10.0", res.GetResourceType(), res.GetCount())
	}
}

func TestExtractRequestedResources_NilAndEmpty(t *testing.T) {
	if extractRequestedResources(nil) != nil {
		t.Errorf("extractRequestedResources(nil) = %v, want nil", extractRequestedResources(nil))
	}
	if extractRequestedResources(&essmsgpb.WorkUnit{}) != nil {
		t.Errorf("extractRequestedResources(empty) = %v, want nil", extractRequestedResources(&essmsgpb.WorkUnit{}))
	}
}

func TestParseTotalChips(t *testing.T) {
	tests := []struct {
		topology string
		want     int64
	}{
		{"", 1},
		{"2x2", 4},
		{"2x2x4", 16},
		{"4x4", 16},
		{"8x8", 64},
		{"invalid", 1},
	}

	for _, tt := range tests {
		t.Run(tt.topology, func(t *testing.T) {
			if got := parseTotalChips(tt.topology); got != tt.want {
				t.Errorf("parseTotalChips(%q) = %d, want %d", tt.topology, got, tt.want)
			}
		})
	}
}
